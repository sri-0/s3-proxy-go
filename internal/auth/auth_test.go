package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func makeTestToken(t *testing.T, roles []string) string {
	t.Helper()
	claims := jwt.MapClaims{
		"sub":   "user1",
		"roles": roles,
		"exp":   time.Now().Add(time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, err := token.SignedString([]byte("test-secret"))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return s
}

func TestExtractRolesFromToken(t *testing.T) {
	token := makeTestToken(t, []string{"C1000", "C1001"})
	roles, err := ExtractRolesFromToken(token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(roles) != 2 {
		t.Fatalf("got %d roles, want 2", len(roles))
	}
	found := map[string]bool{}
	for _, r := range roles {
		found[r] = true
	}
	if !found["C1000"] || !found["C1001"] {
		t.Errorf("roles = %v, want [C1000 C1001]", roles)
	}
}

func TestExtractRolesFromToken_NoRoles(t *testing.T) {
	claims := jwt.MapClaims{"sub": "user1", "exp": time.Now().Add(time.Hour).Unix()}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, _ := token.SignedString([]byte("test-secret"))

	roles, err := ExtractRolesFromToken(s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(roles) != 0 {
		t.Errorf("expected empty roles, got %v", roles)
	}
}

func TestValidateSecurityTag(t *testing.T) {
	tests := []struct {
		name  string
		tag   string
		roles []string
		want  bool
	}{
		{"match", "C1000", []string{"C1000", "C1001"}, true},
		{"no match", "C1002", []string{"C1000"}, false},
		{"empty roles", "C1000", []string{}, false},
		{"empty tag", "", []string{"C1000"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidateSecurityTag(tt.tag, tt.roles); got != tt.want {
				t.Errorf("ValidateSecurityTag(%s, %v) = %v, want %v", tt.tag, tt.roles, got, tt.want)
			}
		})
	}
}

func TestExtractBearerToken(t *testing.T) {
	token, err := ExtractBearerToken("Bearer abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != "abc123" {
		t.Errorf("got %q, want %q", token, "abc123")
	}

	if _, err := ExtractBearerToken(""); err == nil {
		t.Error("expected error for empty header")
	}
	if _, err := ExtractBearerToken("Basic abc"); err == nil {
		t.Error("expected error for Basic auth")
	}
}
