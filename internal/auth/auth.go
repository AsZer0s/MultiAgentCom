package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"
)

type TokenRecord struct {
	TokenHash string    `json:"tokenHash"`
	Actor     string    `json:"actor"`
	Roles     []string  `json:"roles"`
	ProjectID string    `json:"projectId,omitempty"`
	Disabled  bool      `json:"disabled,omitempty"`
	ExpiresAt time.Time `json:"expiresAt,omitempty"`
}

type Principal struct {
	Actor     string
	Roles     []string
	ProjectID string
}

type contextKey struct{}

type Authenticator struct {
	fallbackToken string
	records       []TokenRecord
}

func New(fallbackToken, tokensJSON, tokensFile string) (*Authenticator, error) {
	a := &Authenticator{fallbackToken: strings.TrimSpace(fallbackToken)}
	if strings.TrimSpace(tokensFile) != "" {
		payload, err := os.ReadFile(strings.TrimSpace(tokensFile))
		if err != nil {
			return nil, err
		}
		if err := a.addRecords(payload); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(tokensJSON) != "" {
		if err := a.addRecords([]byte(tokensJSON)); err != nil {
			return nil, err
		}
	}
	return a, nil
}

func (a *Authenticator) Required() bool {
	return strings.TrimSpace(a.fallbackToken) != "" || len(a.records) > 0
}

func (a *Authenticator) Authenticate(token string, now time.Time) (Principal, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return Principal{}, false
	}
	if constantTimeStringEqual(token, a.fallbackToken) {
		return Principal{Actor: "api-token", Roles: []string{"admin"}}, true
	}
	sum := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(sum[:])
	for _, record := range a.records {
		if record.Disabled || strings.TrimSpace(record.TokenHash) == "" {
			continue
		}
		if !record.ExpiresAt.IsZero() && !now.Before(record.ExpiresAt) {
			continue
		}
		if !constantTimeHashEqual(tokenHash, record.TokenHash) {
			continue
		}
		actor := strings.TrimSpace(record.Actor)
		if actor == "" {
			actor = "api-token"
		}
		return Principal{Actor: actor, Roles: normalizeRoles(record.Roles), ProjectID: strings.TrimSpace(record.ProjectID)}, true
	}
	return Principal{}, false
}

func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, contextKey{}, principal)
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(contextKey{}).(Principal)
	return principal, ok
}

func (p Principal) HasAnyRole(roles ...string) bool {
	if hasRole(p.Roles, "admin") {
		return true
	}
	for _, role := range roles {
		if hasRole(p.Roles, role) {
			return true
		}
	}
	return false
}

func (p Principal) AllowsProject(projectID string) bool {
	return strings.TrimSpace(p.ProjectID) == "" || strings.TrimSpace(projectID) == "" || p.ProjectID == projectID
}

func (a *Authenticator) addRecords(payload []byte) error {
	var records []TokenRecord
	if err := json.Unmarshal(payload, &records); err != nil {
		return err
	}
	for idx := range records {
		records[idx].TokenHash = normalizeHash(records[idx].TokenHash)
		if records[idx].TokenHash == "" {
			return errors.New("auth token record missing tokenHash")
		}
		records[idx].Roles = normalizeRoles(records[idx].Roles)
	}
	a.records = append(a.records, records...)
	return nil
}

func normalizeHash(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "sha256:")
	return strings.ToLower(value)
}

func normalizeRoles(values []string) []string {
	roles := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		role := strings.ToLower(strings.TrimSpace(value))
		if role == "" {
			continue
		}
		if _, ok := seen[role]; ok {
			continue
		}
		seen[role] = struct{}{}
		roles = append(roles, role)
	}
	return roles
}

func hasRole(roles []string, role string) bool {
	role = strings.ToLower(strings.TrimSpace(role))
	for _, item := range roles {
		if item == role {
			return true
		}
	}
	return false
}

func constantTimeStringEqual(a, b string) bool {
	if strings.TrimSpace(b) == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func constantTimeHashEqual(a, b string) bool {
	a = normalizeHash(a)
	b = normalizeHash(b)
	if len(a) != len(b) || len(a) == 0 {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
