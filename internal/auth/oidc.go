package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// OIDCConfig holds the configuration for an OpenID Connect provider.
type OIDCConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

// OIDCProvider handles OIDC token verification and user info extraction.
type OIDCProvider struct {
	config    OIDCConfig
	client    *http.Client
	mu        sync.RWMutex
	keys      map[string]*rsa.PublicKey
	keyExpiry time.Time
}

// NewOIDCProvider creates an OIDC provider from configuration.
func NewOIDCProvider(config OIDCConfig) (*OIDCProvider, error) {
	config.Issuer = strings.TrimSuffix(strings.TrimSpace(config.Issuer), "/")
	if config.Issuer == "" {
		return nil, fmt.Errorf("oidc issuer is required")
	}
	if config.ClientID == "" {
		return nil, fmt.Errorf("oidc client id is required")
	}
	return &OIDCProvider{
		config: config,
		client: &http.Client{Timeout: 10 * time.Second},
	}, nil
}

// OIDCDiscovery holds the OIDC discovery document fields we need.
type OIDCDiscovery struct {
	Issuer           string `json:"issuer"`
	AuthorizationURL string `json:"authorization_endpoint"`
	TokenURL         string `json:"token_endpoint"`
	JWKURL           string `json:"jwks_uri"`
	UserInfoURL      string `json:"userinfo_endpoint"`
}

// Discover fetches the OIDC discovery document from the issuer's well-known endpoint.
func (p *OIDCProvider) Discover(ctx context.Context) (*OIDCDiscovery, error) {
	url := p.config.Issuer + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch oidc discovery: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oidc discovery returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var discovery OIDCDiscovery
	if err := json.Unmarshal(body, &discovery); err != nil {
		return nil, fmt.Errorf("decode oidc discovery: %w", err)
	}
	if discovery.Issuer != p.config.Issuer {
		return nil, fmt.Errorf("oidc issuer mismatch: got %s, want %s", discovery.Issuer, p.config.Issuer)
	}
	return &discovery, nil
}

// OIDCTokenClaims represents the verified claims from an OIDC ID token.
type OIDCTokenClaims struct {
	Sub        string   `json:"sub"`
	Email      string   `json:"email,omitempty"`
	Name       string   `json:"name,omitempty"`
	Issuer     string   `json:"iss"`
	Audience   []string `json:"aud"`
	Expiry     int64    `json:"exp"`
	IssuedAt   int64    `json:"iat"`
	Subject    string   `json:"sub"`
	Roles      []string `json:"roles,omitempty"`
	ProjectID  string   `json:"projectId,omitempty"`
}

// VerifyIDToken verifies an OIDC ID token and returns the claims.
// This is a simplified verification for MVP - production should use a proper JWT library.
func (p *OIDCProvider) VerifyIDToken(ctx context.Context, tokenString string) (*OIDCTokenClaims, error) {
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid id token format: expected 3 parts, got %d", len(parts))
	}

	// Decode payload (second part)
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode id token payload: %w", err)
	}

	var claims OIDCTokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("parse id token claims: %w", err)
	}

	// Verify issuer
	if claims.Issuer != p.config.Issuer {
		return nil, fmt.Errorf("id token issuer mismatch: got %s, want %s", claims.Issuer, p.config.Issuer)
	}

	// Verify audience includes our client ID
	validAud := false
	for _, aud := range claims.Audience {
		if aud == p.config.ClientID {
			validAud = true
			break
		}
	}
	if !validAud {
		return nil, fmt.Errorf("id token audience does not include client id %s", p.config.ClientID)
	}

	// Verify expiry
	if time.Now().Unix() > claims.Expiry {
		return nil, fmt.Errorf("id token expired at %d", claims.Expiry)
	}

	// Normalize roles
	if len(claims.Roles) == 0 {
		claims.Roles = []string{"viewer"}
	}

	return &claims, nil
}

// OIDCUserInfo holds user info from the OIDC userinfo endpoint.
type OIDCUserInfo struct {
	Sub           string   `json:"sub"`
	Email         string   `json:"email,omitempty"`
	Name          string   `json:"name,omitempty"`
	EmailVerified bool     `json:"email_verified,omitempty"`
	Roles         []string `json:"roles,omitempty"`
	ProjectID     string   `json:"projectId,omitempty"`
}

// FetchUserInfo retrieves user info from the OIDC provider's userinfo endpoint.
func (p *OIDCProvider) FetchUserInfo(ctx context.Context, accessToken string, userInfoURL string) (*OIDCUserInfo, error) {
	if userInfoURL == "" {
		return nil, fmt.Errorf("userinfo url is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, userInfoURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch oidc userinfo: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("userinfo returned status %d: %s", resp.StatusCode, body)
	}
	var info OIDCUserInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("decode oidc userinfo: %w", err)
	}
	return &info, nil
}

// ToPrincipal converts OIDC claims to an auth Principal.
func (claims *OIDCTokenClaims) ToPrincipal() Principal {
	actor := claims.Sub
	if claims.Email != "" {
		actor = claims.Email
	}
	roles := claims.Roles
	if len(roles) == 0 {
		roles = []string{"viewer"}
	}
	return Principal{
		Actor:     actor,
		Roles:     roles,
		ProjectID: claims.ProjectID,
	}
}

// RBACPolicy defines project-level access control.
type RBACPolicy struct {
	ProjectRoles map[string]map[string][]string // projectID -> subject -> roles
}

// NewRBACPolicy creates an empty RBAC policy.
func NewRBACPolicy() *RBACPolicy {
	return &RBACPolicy{
		ProjectRoles: make(map[string]map[string][]string),
	}
}

// AssignRole assigns a role to a subject within a project.
func (p *RBACPolicy) AssignRole(projectID, subject, role string) {
	if _, ok := p.ProjectRoles[projectID]; !ok {
		p.ProjectRoles[projectID] = make(map[string][]string)
	}
	subject = strings.ToLower(strings.TrimSpace(subject))
	role = strings.ToLower(strings.TrimSpace(role))
	roles := p.ProjectRoles[projectID][subject]
	for _, r := range roles {
		if r == role {
			return
		}
	}
	p.ProjectRoles[projectID][subject] = append(roles, role)
}

// RevokeRole removes a role from a subject within a project.
func (p *RBACPolicy) RevokeRole(projectID, subject, role string) {
	subject = strings.ToLower(strings.TrimSpace(subject))
	role = strings.ToLower(strings.TrimSpace(role))
	roles := p.ProjectRoles[projectID][subject]
	filtered := make([]string, 0, len(roles))
	for _, r := range roles {
		if r != role {
			filtered = append(filtered, r)
		}
	}
	p.ProjectRoles[projectID][subject] = filtered
}

// HasRole checks if a subject has a specific role within a project.
func (p *RBACPolicy) HasRole(projectID, subject, role string) bool {
	subject = strings.ToLower(strings.TrimSpace(subject))
	roles, ok := p.ProjectRoles[projectID]
	if !ok {
		return false
	}
	return hasRole(roles[subject], role)
}

// ProjectRolesFor returns all roles for a subject within a project.
func (p *RBACPolicy) ProjectRolesFor(projectID, subject string) []string {
	subject = strings.ToLower(strings.TrimSpace(subject))
	if roles, ok := p.ProjectRoles[projectID]; ok {
		return roles[subject]
	}
	return nil
}

// EffectiveRoles returns the effective roles for a principal on a project,
// combining global roles with project-specific roles.
func (p *RBACPolicy) EffectiveRoles(principal Principal, projectID string) []string {
	globalRoles := principal.Roles
	projectRoles := p.ProjectRolesFor(projectID, principal.Actor)

	merged := make(map[string]struct{})
	for _, r := range globalRoles {
		merged[r] = struct{}{}
	}
	for _, r := range projectRoles {
		merged[r] = struct{}{}
	}
	result := make([]string, 0, len(merged))
	for r := range merged {
		result = append(result, r)
	}
	return result
}

// CheckProjectAccess verifies that a principal has the required role for a project.
func (p *RBACPolicy) CheckProjectAccess(principal Principal, projectID, requiredRole string) bool {
	// Admin has access to everything
	if principal.HasAnyRole("admin") {
		return true
	}
	// Check effective roles
	effective := p.EffectiveRoles(principal, projectID)
	return hasRole(effective, requiredRole)
}

// JWK representation for key fetching
type jwkKey struct {
	KID string `json:"kid"`
	KTY string `json:"kty"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type jwkSet struct {
	Keys []jwkKey `json:"keys"`
}

// OIDCTokenResponse holds the tokens returned by the OIDC token endpoint.
type OIDCTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
}

// ExchangeCode exchanges an authorization code for tokens at the OIDC token endpoint.
func (p *OIDCProvider) ExchangeCode(ctx context.Context, code, redirectURI string) (*OIDCTokenResponse, error) {
	discovery, err := p.Discover(ctx)
	if err != nil {
		return nil, fmt.Errorf("discover oidc: %w", err)
	}
	if discovery.TokenURL == "" {
		return nil, fmt.Errorf("oidc token endpoint not found in discovery document")
	}

	form := fmt.Sprintf("grant_type=authorization_code&code=%s&redirect_uri=%s&client_id=%s&client_secret=%s",
		code, redirectURI, p.config.ClientID, p.config.ClientSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, discovery.TokenURL, strings.NewReader(form))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("exchange oidc code: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oidc token endpoint returned status %d: %s", resp.StatusCode, body)
	}
	var tokenResp OIDCTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("decode oidc token response: %w", err)
	}
	return &tokenResp, nil
}

// FetchJWKS retrieves and caches the OIDC provider's JSON Web Key Set.
func (p *OIDCProvider) FetchJWKS(ctx context.Context, jwksURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURL, nil)
	if err != nil {
		return err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch jwks: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	var keySet jwkSet
	if err := json.Unmarshal(body, &keySet); err != nil {
		return fmt.Errorf("decode jwks: %w", err)
	}
	keys := make(map[string]*rsa.PublicKey)
	for _, key := range keySet.Keys {
		if key.KTY != "RSA" || key.Use != "sig" {
			continue
		}
		nBytes, err := base64.RawURLEncoding.DecodeString(key.N)
		if err != nil {
			continue
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(key.E)
		if err != nil {
			continue
		}
		n := new(big.Int).SetBytes(nBytes)
		e := new(big.Int).SetBytes(eBytes)
		keys[key.KID] = &rsa.PublicKey{N: n, E: int(e.Int64())}
	}
	p.mu.Lock()
	p.keys = keys
	p.keyExpiry = time.Now().Add(1 * time.Hour)
	p.mu.Unlock()
	return nil
}
