package catalog

import "fmt"

var validIcebergTypes = map[string]bool{
	"string":      true,
	"long":        true,
	"int":         true,
	"boolean":     true,
	"float":       true,
	"double":      true,
	"timestamptz": true,
	"date":        true,
	"binary":      true,
	"uuid":        true,
}

var validSecurityLevels = map[string]bool{
	"base":     true,
	"elevated": true,
}

var validPartitionTransforms = map[string]bool{
	"identity": true,
	"year":     true,
	"month":    true,
	"day":      true,
	"hour":     true,
	// bucket[N] and truncate[N] validated by prefix
}

var validSortOrders = map[string]bool{
	"asc":  true,
	"desc": true,
	"":     true,
}

var validTableStrategies = map[string]bool{
	"single":   true,
	"two_tier": true,
	"per_tag":  true,
}

type CatalogConfig struct {
	Enabled          bool          `yaml:"enabled"`
	Storage          StorageConfig `yaml:"storage"`
	CatalogType      string        `yaml:"catalogType"`
	SQLitePath       string        `yaml:"sqlitePath"`
	CatalogURI       string        `yaml:"catalogURI"`
	CatalogNamespace string        `yaml:"catalogNamespace"`
	TableStrategy    string        `yaml:"tableStrategy"`
	BaseSecurityTag  string        `yaml:"baseSecurityTag"`
	FailOnError      bool          `yaml:"failOnError"`
	Columns          ColumnsConfig `yaml:"columns"`
}

type StorageConfig struct {
	Endpoint        string `yaml:"endpoint"`
	Region          string `yaml:"region"`
	Bucket          string `yaml:"bucket"`
	AccessKeyEnvVar string `yaml:"accessKeyEnvVar"`
	SecretKeyEnvVar string `yaml:"secretKeyEnvVar"`
	UseSSL          bool   `yaml:"useSSL"`
	WarehousePath   string `yaml:"warehousePath"`
}

type ColumnsConfig struct {
	Mandatory []ColumnDef `yaml:"mandatory"`
	Optional  []ColumnDef `yaml:"optional"`
}

type ColumnDef struct {
	Name          string `yaml:"name"`
	Source        string `yaml:"source"`
	IcebergType   string `yaml:"icebergType"`
	SecurityLevel string `yaml:"securityLevel"`
	PartitionBy   string `yaml:"partitionBy,omitempty"`
	BloomFilter   bool   `yaml:"bloomFilter,omitempty"`
	SortOrder     string `yaml:"sortOrder,omitempty"`
}

func (c *CatalogConfig) Validate() error {
	if !c.Enabled {
		return nil
	}

	if c.CatalogType != "sql" && c.CatalogType != "rest" {
		return fmt.Errorf("catalog.catalogType must be 'sql' or 'rest', got %q", c.CatalogType)
	}
	if c.CatalogType == "sql" && c.SQLitePath == "" {
		return fmt.Errorf("catalog.sqlitePath is required when catalogType is 'sql'")
	}
	if c.CatalogType == "rest" && c.CatalogURI == "" {
		return fmt.Errorf("catalog.catalogURI is required when catalogType is 'rest'")
	}
	if c.CatalogNamespace == "" {
		return fmt.Errorf("catalog.catalogNamespace is required")
	}
	if !validTableStrategies[c.TableStrategy] {
		return fmt.Errorf("catalog.tableStrategy must be 'single', 'two_tier', or 'per_tag', got %q", c.TableStrategy)
	}
	if c.Storage.Endpoint == "" {
		return fmt.Errorf("catalog.storage.endpoint is required")
	}
	if c.Storage.Bucket == "" {
		return fmt.Errorf("catalog.storage.bucket is required")
	}
	if c.Storage.AccessKeyEnvVar == "" {
		return fmt.Errorf("catalog.storage.accessKeyEnvVar is required")
	}
	if c.Storage.SecretKeyEnvVar == "" {
		return fmt.Errorf("catalog.storage.secretKeyEnvVar is required")
	}
	if c.Storage.WarehousePath == "" {
		return fmt.Errorf("catalog.storage.warehousePath is required")
	}

	if len(c.Columns.Mandatory) == 0 {
		return fmt.Errorf("catalog.columns.mandatory must have at least one column")
	}

	allCols := append(c.Columns.Mandatory, c.Columns.Optional...)
	names := make(map[string]bool)
	for _, col := range allCols {
		if err := validateColumnDef(col); err != nil {
			return fmt.Errorf("column %q: %w", col.Name, err)
		}
		if names[col.Name] {
			return fmt.Errorf("duplicate column name %q", col.Name)
		}
		names[col.Name] = true
	}

	return nil
}

func validateColumnDef(col ColumnDef) error {
	if col.Name == "" {
		return fmt.Errorf("name is required")
	}
	if col.Source == "" {
		return fmt.Errorf("source is required")
	}
	if !validIcebergTypes[col.IcebergType] {
		return fmt.Errorf("invalid icebergType %q", col.IcebergType)
	}
	if !validSecurityLevels[col.SecurityLevel] {
		return fmt.Errorf("invalid securityLevel %q", col.SecurityLevel)
	}
	if !validSortOrders[col.SortOrder] {
		return fmt.Errorf("invalid sortOrder %q", col.SortOrder)
	}
	if col.PartitionBy != "" {
		if !validPartitionTransforms[col.PartitionBy] &&
			!hasPrefix(col.PartitionBy, "bucket[") &&
			!hasPrefix(col.PartitionBy, "truncate[") {
			return fmt.Errorf("invalid partitionBy %q", col.PartitionBy)
		}
	}
	return nil
}

func hasPrefix(s, prefix string) bool {
	return len(s) > len(prefix) && s[:len(prefix)] == prefix
}

func (c *ColumnsConfig) AllColumns() []ColumnDef {
	all := make([]ColumnDef, 0, len(c.Mandatory)+len(c.Optional))
	all = append(all, c.Mandatory...)
	all = append(all, c.Optional...)
	return all
}

func (c *ColumnsConfig) BaseColumns() []ColumnDef {
	var cols []ColumnDef
	for _, col := range c.AllColumns() {
		if col.SecurityLevel == "base" {
			cols = append(cols, col)
		}
	}
	return cols
}

func (c *ColumnsConfig) ElevatedColumns() []ColumnDef {
	var cols []ColumnDef
	for _, col := range c.AllColumns() {
		if col.SecurityLevel == "elevated" {
			cols = append(cols, col)
		}
	}
	return cols
}

func (c *ColumnsConfig) IsMandatory(name string) bool {
	for _, col := range c.Mandatory {
		if col.Name == name {
			return true
		}
	}
	return false
}
