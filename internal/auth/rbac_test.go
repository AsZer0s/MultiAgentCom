package auth

import (
	"testing"
)

func TestRBACPolicyAssignAndCheck(t *testing.T) {
	p := NewRBACPolicy()

	p.AssignRole("proj-1", "alice", "operator")
	if !p.HasRole("proj-1", "alice", "operator") {
		t.Error("expected alice to have operator role in proj-1")
	}
	if p.HasRole("proj-1", "alice", "admin") {
		t.Error("expected alice to not have admin role in proj-1")
	}
	if p.HasRole("proj-2", "alice", "operator") {
		t.Error("expected alice to not have operator role in proj-2")
	}
}

func TestRBACPolicyRevokeRole(t *testing.T) {
	p := NewRBACPolicy()

	p.AssignRole("proj-1", "bob", "operator")
	p.AssignRole("proj-1", "bob", "viewer")
	if !p.HasRole("proj-1", "bob", "operator") {
		t.Fatal("expected bob to have operator role")
	}

	p.RevokeRole("proj-1", "bob", "operator")
	if p.HasRole("proj-1", "bob", "operator") {
		t.Error("expected bob's operator role to be revoked")
	}
	if !p.HasRole("proj-1", "bob", "viewer") {
		t.Error("expected bob to still have viewer role")
	}
}

func TestRBACPolicyIdempotentAssign(t *testing.T) {
	p := NewRBACPolicy()

	p.AssignRole("proj-1", "charlie", "operator")
	p.AssignRole("proj-1", "charlie", "operator") // duplicate
	roles := p.ProjectRolesFor("proj-1", "charlie")
	if len(roles) != 1 {
		t.Errorf("expected 1 role, got %d", len(roles))
	}
}

func TestRBACPolicyCaseInsensitive(t *testing.T) {
	p := NewRBACPolicy()

	p.AssignRole("proj-1", "Alice", "OPERATOR")
	if !p.HasRole("proj-1", "alice", "operator") {
		t.Error("expected case-insensitive matching")
	}
}

func TestRBACPolicyEffectiveRoles(t *testing.T) {
	p := NewRBACPolicy()
	p.AssignRole("proj-1", "alice", "operator")

	principal := Principal{Actor: "alice", Roles: []string{"viewer"}}
	effective := p.EffectiveRoles(principal, "proj-1")

	hasViewer := false
	hasOperator := false
	for _, r := range effective {
		if r == "viewer" {
			hasViewer = true
		}
		if r == "operator" {
			hasOperator = true
		}
	}
	if !hasViewer {
		t.Error("expected viewer in effective roles")
	}
	if !hasOperator {
		t.Error("expected operator in effective roles")
	}
}

func TestRBACPolicyCheckProjectAccess(t *testing.T) {
	p := NewRBACPolicy()
	p.AssignRole("proj-1", "alice", "operator")

	// Admin always passes
	admin := Principal{Actor: "admin", Roles: []string{"admin"}}
	if !p.CheckProjectAccess(admin, "proj-1", "operator") {
		t.Error("expected admin to have access")
	}

	// User with matching role
	user := Principal{Actor: "alice", Roles: []string{"viewer"}}
	if !p.CheckProjectAccess(user, "proj-1", "operator") {
		t.Error("expected alice to have operator access via project role")
	}

	// User without matching role
	other := Principal{Actor: "bob", Roles: []string{"viewer"}}
	if p.CheckProjectAccess(other, "proj-1", "operator") {
		t.Error("expected bob to not have operator access")
	}
}
