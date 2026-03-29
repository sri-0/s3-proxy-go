package auth

import (
	"fmt"

	"github.com/golang-jwt/jwt/v5"
)

// ExtractRolesFromToken parses a JWT without signature verification
// (the proxy trusts that an upstream gateway has already validated the token)
// and returns the "roles" claim as a string slice.
func ExtractRolesFromToken(tokenString string) ([]string, error) {
	parser := jwt.NewParser(jwt.WithoutClaimsValidation())

	token, _, err := parser.ParseUnverified(tokenString, jwt.MapClaims{})
	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %w", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("unexpected claims type")
	}

	rawRoles, exists := claims["roles"]
	if !exists {
		return nil, nil
	}

	roleSlice, ok := rawRoles.([]interface{})
	if !ok {
		return nil, fmt.Errorf("roles claim is not an array")
	}

	roles := make([]string, 0, len(roleSlice))
	for _, r := range roleSlice {
		if s, ok := r.(string); ok {
			roles = append(roles, s)
		}
	}

	return roles, nil
}

// ValidateSecurityTag checks whether the given securityTagId exists in the user's roles.
func ValidateSecurityTag(securityTag string, roles []string) bool {
	if securityTag == "" {
		return false
	}
	for _, r := range roles {
		if r == securityTag {
			return true
		}
	}
	return false
}

// ExtractBearerToken pulls the token from "Authorization: Bearer <token>".
func ExtractBearerToken(authHeader string) (string, error) {
	if len(authHeader) < 7 || authHeader[:7] != "Bearer " {
		return "", fmt.Errorf("missing or malformed Authorization Bearer header")
	}
	return authHeader[7:], nil
}
