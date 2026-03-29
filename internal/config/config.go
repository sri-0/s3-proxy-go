package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

var validOperations = map[string]bool{
	"LIST":   true,
	"GET":    true,
	"PUT":    true,
	"DELETE": true,
	"HEAD":   true,
}

type Config struct {
	ListenAddr          string          `yaml:"listenAddr"`
	AllowedSecurityTags []string        `yaml:"allowedSecurityTags"`
	SecurityTagHeader   string          `yaml:"securityTagHeader"`
	MandatoryPutHeaders []string        `yaml:"mandatoryPutHeaders"`
	ExcludedMetaHeaders []string        `yaml:"excludedMetaHeaders"`
	Accounts            []AccountConfig `yaml:"accounts"`
}

type AccountConfig struct {
	Name              string         `yaml:"name"`
	Endpoint          string         `yaml:"endpoint"`
	Region            string         `yaml:"region"`
	AccessKeyEnvVar   string         `yaml:"accessKeyEnvVar"`
	SecretKeyEnvVar   string         `yaml:"secretKeyEnvVar"`
	PathPrefix        string         `yaml:"pathPrefix"`
	UseSSL            bool           `yaml:"useSSL"`
	AllowedOperations []string       `yaml:"allowedOperations"`
	Buckets           []BucketConfig `yaml:"buckets"`
}

type BucketConfig struct {
	Name              string   `yaml:"name"`
	AllowedOperations []string `yaml:"allowedOperations,omitempty"`
}

func (b BucketConfig) ResolveOps(accountOps []string) []string {
	if len(b.AllowedOperations) > 0 {
		return b.AllowedOperations
	}
	return accountOps
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":8080"
	}
	if cfg.SecurityTagHeader == "" {
		cfg.SecurityTagHeader = "X-Securitytagid"
	}
	if len(cfg.MandatoryPutHeaders) == 0 {
		cfg.MandatoryPutHeaders = []string{cfg.SecurityTagHeader}
	}
	if len(cfg.ExcludedMetaHeaders) == 0 {
		cfg.ExcludedMetaHeaders = []string{"Authorization", "Content-Length", "Host", "Connection"}
	}

	for i := range cfg.Accounts {
		cfg.Accounts[i].PathPrefix = strings.TrimSuffix(cfg.Accounts[i].PathPrefix, "/")
		if len(cfg.Accounts[i].AllowedOperations) == 0 {
			cfg.Accounts[i].AllowedOperations = []string{"LIST", "GET", "PUT", "DELETE", "HEAD"}
		}
		for j, op := range cfg.Accounts[i].AllowedOperations {
			cfg.Accounts[i].AllowedOperations[j] = strings.ToUpper(op)
		}
		for k := range cfg.Accounts[i].Buckets {
			for l, op := range cfg.Accounts[i].Buckets[k].AllowedOperations {
				cfg.Accounts[i].Buckets[k].AllowedOperations[l] = strings.ToUpper(op)
			}
		}
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

func (c *Config) Validate() error {
	if len(c.Accounts) == 0 {
		return fmt.Errorf("at least one account is required")
	}
	if len(c.AllowedSecurityTags) == 0 {
		return fmt.Errorf("at least one allowed security tag is required")
	}

	names := make(map[string]bool)
	for _, acc := range c.Accounts {
		if acc.Name == "" {
			return fmt.Errorf("account name is required")
		}
		if names[acc.Name] {
			return fmt.Errorf("duplicate account name: %s", acc.Name)
		}
		names[acc.Name] = true

		if acc.Endpoint == "" {
			return fmt.Errorf("account %s: endpoint is required", acc.Name)
		}
		if acc.AccessKeyEnvVar == "" {
			return fmt.Errorf("account %s: accessKeyEnvVar is required", acc.Name)
		}
		if acc.SecretKeyEnvVar == "" {
			return fmt.Errorf("account %s: secretKeyEnvVar is required", acc.Name)
		}
		if err := validateOps(acc.AllowedOperations); err != nil {
			return fmt.Errorf("account %s: %w", acc.Name, err)
		}
		for _, bkt := range acc.Buckets {
			if err := validateOps(bkt.AllowedOperations); err != nil {
				return fmt.Errorf("account %s, bucket %s: %w", acc.Name, bkt.Name, err)
			}
		}
	}

	return c.validateRouteClashes()
}

func validateOps(ops []string) error {
	for _, op := range ops {
		if !validOperations[op] {
			return fmt.Errorf("invalid operation %q, must be one of: LIST, GET, PUT, DELETE, HEAD", op)
		}
	}
	return nil
}

func (c *Config) validateRouteClashes() error {
	type route struct {
		prefix string
		bucket string
	}

	routes := make(map[route]string)

	for _, acc := range c.Accounts {
		if len(acc.Buckets) > 0 {
			for _, b := range acc.Buckets {
				r := route{prefix: acc.PathPrefix, bucket: b.Name}
				if existing, ok := routes[r]; ok {
					return fmt.Errorf("route clash: bucket %q under prefix %q claimed by both %q and %q",
						b.Name, acc.PathPrefix, existing, acc.Name)
				}
				routes[r] = acc.Name
			}
		} else {
			r := route{prefix: acc.PathPrefix, bucket: "*"}
			if existing, ok := routes[r]; ok {
				return fmt.Errorf("route clash: wildcard bucket under prefix %q claimed by both %q and %q",
					acc.PathPrefix, existing, acc.Name)
			}
			routes[r] = acc.Name
		}
	}

	return nil
}

func (c *Config) AllowedTagSet() map[string]bool {
	set := make(map[string]bool, len(c.AllowedSecurityTags))
	for _, tag := range c.AllowedSecurityTags {
		set[tag] = true
	}
	return set
}

func (c *Config) ExcludedHeaderSet() map[string]bool {
	set := make(map[string]bool, len(c.ExcludedMetaHeaders))
	for _, h := range c.ExcludedMetaHeaders {
		set[h] = true
	}
	return set
}

func OpsSet(ops []string) map[string]bool {
	set := make(map[string]bool, len(ops))
	for _, op := range ops {
		set[op] = true
	}
	return set
}
