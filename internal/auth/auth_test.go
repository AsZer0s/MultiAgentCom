package auth

import (
	"testing"
	"time"
)

func TestAuthenticatorRequired(t *testing.T) {
	// No tokens configured - auth not required
	a, err := New("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if a.Required() {
		t.Error("expected auth not required when no tokens configured")
	}

	// With API token - auth required
	a, err = New("test-token", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !a.Required() {
		t.Error("expected auth required when API token configured")
	}
}

func TestAuthenticatorAuthenticateFallbackToken(t *testing.T) {
	a, err := New("test-token", "", "")
	if err != nil {
		t.Fatal(err)
	}

	// Valid token
	principal, ok := a.Authenticate("test-token", time.Now())
	if !ok {
		t.Fatal("expected authentication to succeed")
	}
	if principal.Actor != "api-token" {
		t.Errorf("expected actor 'api-token', got %q", principal.Actor)
	}
	if !principal.HasAnyRole("admin") {
		t.Error("expected admin role for fallback token")
	}

	// Invalid token
	_, ok = a.Authenticate("wrong-token", time.Now())
	if ok {
		t.Error("expected authentication to fail with wrong token")
	}

	// Empty token
	_, ok = a.Authenticate("", time.Now())
	if ok {
		t.Error("expected authentication to fail with empty token")
	}
}

func TestAuthenticatorAuthenticateStructuredTokens(t *testing.T) {
	tokensJSON := `[{"tokenHash":"a]9f8e7d6c5b4a3210fedcba9876543210","actor":"alice","roles":["operator","viewer"]}]`
	a, err := New("", tokensJSON, "")
	if err != nil {
		t.Fatal(err)
	}

	if !a.Required() {
		t.Error("expected auth required when structured tokens configured")
	}
}

func TestAuthenticatorAuthenticateExpiredToken(t *testing.T) {
	// Token with expiry in the past
	tokensJSON := `[{"tokenHash":"expired123","actor":"bob","roles":["viewer"],"expiresAt":"2020-01-01T00:00:00Z"}]`
	a, err := New("", tokensJSON, "")
	if err != nil {
		t.Fatal(err)
	}

	_, ok := a.Authenticate("expired123", time.Now())
	if ok {
		t.Error("expected authentication to fail for expired token")
	}
}

func TestAuthenticatorAuthenticateDisabledToken(t *testing.T) {
	tokensJSON := `[{"tokenHash":"disabled123","actor":"charlie","roles":["viewer"],"disabled":true}]`
	a, err := New("", tokensJSON, "")
	if err != nil {
		t.Fatal(err)
	}

	_, ok := a.Authenticate("disabled123", time.Now())
	if ok {
		t.Error("expected authentication to fail for disabled token")
	}
}

func TestPrincipalHasAnyRole(t *testing.T) {
	p := Principal{Actor: "test", Roles: []string{"operator", "viewer"}}

	if !p.HasAnyRole("operator") {
		t.Error("expected HasAnyRole('operator') to be true")
	}
	if !p.HasAnyRole("viewer") {
		t.Error("expected HasAnyRole('viewer') to be true")
	}
	if p.HasAnyRole("admin") {
		t.Error("expected HasAnyRole('admin') to be false")
	}

	// Admin role bypasses all checks
	admin := Principal{Actor: "admin", Roles: []string{"admin"}}
	if !admin.HasAnyRole("operator") {
		t.Error("expected admin HasAnyRole('operator') to be true")
	}
}

func TestPrincipalAllowsProject(t *testing.T) {
	// Unrestricted principal (empty project ID)
	unrestricted := Principal{Actor: "test", Roles: []string{"viewer"}, ProjectID: ""}
	if !unrestricted.AllowsProject("proj-1") {
		t.Error("expected unrestricted principal to allow any project")
	}

	// Restricted principal
	restricted := Principal{Actor: "test", Roles: []string{"viewer"}, ProjectID: "proj-1"}
	if !restricted.AllowsProject("proj-1") {
		t.Error("expected restricted principal to allow its own project")
	}
	if restricted.AllowsProject("proj-2") {
		t.Error("expected restricted principal to deny other projects")
	}
}

func TestRoleNormalization(t *testing.T) {
	p := Principal{Actor: "test", Roles: []string{"ADMIN", "Operator"}}
	if !p.HasAnyRole("admin") {
		t.Error("expected role normalization to lowercase")
	}
	if !p.HasAnyRole("operator") {
		t.Error("expected role normalization to lowercase")
	}
}
