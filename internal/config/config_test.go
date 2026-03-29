package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_Valid(t *testing.T) {
	yaml := `
listenAddr: ":9090"
allowedSecurityTags:
  - C1000
  - C1001
securityTagHeader: X-Securitytagid
accounts:
  - name: primary
    endpoint: localhost:9000
    accessKeyEnvVar: TEST_AK
    secretKeyEnvVar: TEST_SK
    pathPrefix: /primary
    allowedOperations: [GET, PUT]
    buckets:
      - name: docs
`
	path := writeTempConfig(t, yaml)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.ListenAddr != ":9090" {
		t.Errorf("listenAddr = %s, want :9090", cfg.ListenAddr)
	}
	if len(cfg.Accounts) != 1 {
		t.Fatalf("accounts = %d, want 1", len(cfg.Accounts))
	}
	if cfg.Accounts[0].Name != "primary" {
		t.Errorf("account name = %s, want primary", cfg.Accounts[0].Name)
	}
}

func TestLoad_Defaults(t *testing.T) {
	yaml := `
allowedSecurityTags:
  - C1000
accounts:
  - name: test
    endpoint: localhost:9000
    accessKeyEnvVar: TEST_AK
    secretKeyEnvVar: TEST_SK
`
	path := writeTempConfig(t, yaml)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.ListenAddr != ":8080" {
		t.Errorf("default listenAddr = %s, want :8080", cfg.ListenAddr)
	}
	if cfg.SecurityTagHeader != "X-Securitytagid" {
		t.Errorf("default securityTagHeader = %s", cfg.SecurityTagHeader)
	}
	if len(cfg.MandatoryPutHeaders) != 1 || cfg.MandatoryPutHeaders[0] != "X-Securitytagid" {
		t.Errorf("default mandatoryPutHeaders = %v", cfg.MandatoryPutHeaders)
	}
	if len(cfg.ExcludedMetaHeaders) != 4 {
		t.Errorf("default excludedMetaHeaders = %v", cfg.ExcludedMetaHeaders)
	}
	if len(cfg.Accounts[0].AllowedOperations) != 5 {
		t.Errorf("default allowedOperations = %v", cfg.Accounts[0].AllowedOperations)
	}
}

func TestValidate_NoAccounts(t *testing.T) {
	cfg := &Config{
		AllowedSecurityTags: []string{"C1000"},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for no accounts")
	}
}

func TestValidate_NoAllowedTags(t *testing.T) {
	cfg := &Config{
		Accounts: []AccountConfig{
			{Name: "test", Endpoint: "localhost:9000", AccessKeyEnvVar: "AK", SecretKeyEnvVar: "SK", AllowedOperations: []string{"GET"}},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for no allowed tags")
	}
}

func TestValidate_DuplicateAccountName(t *testing.T) {
	cfg := &Config{
		AllowedSecurityTags: []string{"C1000"},
		Accounts: []AccountConfig{
			{Name: "test", Endpoint: "localhost:9000", AccessKeyEnvVar: "AK", SecretKeyEnvVar: "SK", AllowedOperations: []string{"GET"}},
			{Name: "test", Endpoint: "localhost:9001", AccessKeyEnvVar: "AK2", SecretKeyEnvVar: "SK2", AllowedOperations: []string{"GET"}},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for duplicate account name")
	}
}

func TestValidate_InvalidOperation(t *testing.T) {
	cfg := &Config{
		AllowedSecurityTags: []string{"C1000"},
		Accounts: []AccountConfig{
			{Name: "test", Endpoint: "localhost:9000", AccessKeyEnvVar: "AK", SecretKeyEnvVar: "SK", AllowedOperations: []string{"GET", "YEET"}},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for invalid operation")
	}
}

func TestValidate_RouteClash(t *testing.T) {
	cfg := &Config{
		AllowedSecurityTags: []string{"C1000"},
		Accounts: []AccountConfig{
			{
				Name: "a1", Endpoint: "localhost:9000",
				AccessKeyEnvVar: "AK", SecretKeyEnvVar: "SK",
				PathPrefix: "/data", AllowedOperations: []string{"GET"},
				Buckets: []BucketConfig{{Name: "docs"}},
			},
			{
				Name: "a2", Endpoint: "localhost:9001",
				AccessKeyEnvVar: "AK2", SecretKeyEnvVar: "SK2",
				PathPrefix: "/data", AllowedOperations: []string{"GET"},
				Buckets: []BucketConfig{{Name: "docs"}},
			},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected route clash error")
	}
}

func TestValidate_NoClashDifferentPrefix(t *testing.T) {
	cfg := &Config{
		AllowedSecurityTags: []string{"C1000"},
		Accounts: []AccountConfig{
			{
				Name: "a1", Endpoint: "localhost:9000",
				AccessKeyEnvVar: "AK", SecretKeyEnvVar: "SK",
				PathPrefix: "/primary", AllowedOperations: []string{"GET"},
				Buckets: []BucketConfig{{Name: "docs"}},
			},
			{
				Name: "a2", Endpoint: "localhost:9001",
				AccessKeyEnvVar: "AK2", SecretKeyEnvVar: "SK2",
				PathPrefix: "/secondary", AllowedOperations: []string{"GET"},
				Buckets: []BucketConfig{{Name: "docs"}},
			},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBucketConfig_ResolveOps(t *testing.T) {
	accountOps := []string{"GET", "LIST"}

	bktDefault := BucketConfig{Name: "b"}
	if ops := bktDefault.ResolveOps(accountOps); len(ops) != 2 {
		t.Errorf("expected account ops, got %v", ops)
	}

	bktOverride := BucketConfig{Name: "b", AllowedOperations: []string{"GET", "PUT", "DELETE"}}
	if ops := bktOverride.ResolveOps(accountOps); len(ops) != 3 {
		t.Errorf("expected bucket ops, got %v", ops)
	}
}

func TestAllowedTagSet(t *testing.T) {
	cfg := &Config{AllowedSecurityTags: []string{"C1000", "C1001", "C1002"}}
	set := cfg.AllowedTagSet()

	if len(set) != 3 {
		t.Fatalf("expected 3 tags, got %d", len(set))
	}
	for _, tag := range []string{"C1000", "C1001", "C1002"} {
		if !set[tag] {
			t.Errorf("tag %s not in set", tag)
		}
	}
}

func TestExcludedHeaderSet(t *testing.T) {
	cfg := &Config{ExcludedMetaHeaders: []string{"Authorization", "X-Internal"}}
	set := cfg.ExcludedHeaderSet()

	if !set["Authorization"] || !set["X-Internal"] {
		t.Errorf("set = %v", set)
	}
}

func TestOpsSet(t *testing.T) {
	set := OpsSet([]string{"GET", "LIST"})
	if !set["GET"] || !set["LIST"] {
		t.Errorf("set = %v", set)
	}
	if set["DELETE"] {
		t.Error("DELETE should not be in set")
	}
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}
