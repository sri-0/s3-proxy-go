package catalog

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/apache/iceberg-go"
	icecat "github.com/apache/iceberg-go/catalog"
)

// IcebergCatalog implements Catalog using Apache Iceberg tables.
type IcebergCatalog struct {
	cat              icecat.Catalog
	cfg              CatalogConfig
	allowedTags      []string
	securityTagHeader string
}

// New creates an IcebergCatalog, connecting to the catalog backend
// and creating tables if they don't already exist.
func New(cfg CatalogConfig, allowedTags []string, securityTagHeader string) (*IcebergCatalog, error) {
	var cat icecat.Catalog
	var err error

	switch cfg.CatalogType {
	case "sql":
		cat, err = newSQLCatalog(cfg)
	case "rest":
		cat, err = newRESTCatalog(cfg)
	default:
		return nil, fmt.Errorf("unsupported catalog type: %s", cfg.CatalogType)
	}
	if err != nil {
		return nil, fmt.Errorf("creating catalog: %w", err)
	}

	ic := &IcebergCatalog{
		cat:              cat,
		cfg:              cfg,
		allowedTags:      allowedTags,
		securityTagHeader: securityTagHeader,
	}

	if err := ic.initTables(context.Background()); err != nil {
		return nil, fmt.Errorf("initializing tables: %w", err)
	}

	return ic, nil
}

func (ic *IcebergCatalog) initTables(ctx context.Context) error {
	ns := icecat.ToIdentifier(ic.cfg.CatalogNamespace)

	exists, err := ic.cat.CheckNamespaceExists(ctx, ns)
	if err != nil {
		return fmt.Errorf("checking namespace: %w", err)
	}
	if !exists {
		if err := ic.cat.CreateNamespace(ctx, ns, nil); err != nil {
			return fmt.Errorf("creating namespace: %w", err)
		}
	}

	switch ic.cfg.TableStrategy {
	case "single":
		return ic.ensureTable(ctx, "object_metadata", ic.cfg.Columns.AllColumns())
	case "two_tier":
		if err := ic.ensureTable(ctx, "base_metadata", ic.cfg.Columns.BaseColumns()); err != nil {
			return err
		}
		for _, tag := range ic.allowedTags {
			cols := ic.detailColumns()
			if err := ic.ensureTable(ctx, "detail_"+tag, cols); err != nil {
				return err
			}
		}
		return nil
	case "per_tag":
		for _, tag := range ic.allowedTags {
			if err := ic.ensureTable(ctx, "objects_"+tag, ic.cfg.Columns.AllColumns()); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown table strategy: %s", ic.cfg.TableStrategy)
	}
}

func (ic *IcebergCatalog) detailColumns() []ColumnDef {
	joinKeys := []ColumnDef{
		{Name: "object_key", Source: "_key", IcebergType: "string", SecurityLevel: "elevated"},
		{Name: "bucket", Source: "_bucket", IcebergType: "string", SecurityLevel: "elevated"},
		{Name: "created_at", Source: "_timestamp", IcebergType: "timestamptz", SecurityLevel: "elevated"},
	}

	elevated := ic.cfg.Columns.ElevatedColumns()

	seen := map[string]bool{"object_key": true, "bucket": true, "created_at": true}
	var cols []ColumnDef
	cols = append(cols, joinKeys...)
	for _, col := range elevated {
		if !seen[col.Name] {
			cols = append(cols, col)
			seen[col.Name] = true
		}
	}
	return cols
}

func (ic *IcebergCatalog) ensureTable(ctx context.Context, tableName string, columns []ColumnDef) error {
	ident := icecat.ToIdentifier(ic.cfg.CatalogNamespace, tableName)

	exists, err := ic.cat.CheckTableExists(ctx, ident)
	if err != nil {
		return fmt.Errorf("checking table %s: %w", tableName, err)
	}
	if exists {
		return nil
	}

	mandatory := make(map[string]bool)
	for _, col := range ic.cfg.Columns.Mandatory {
		mandatory[col.Name] = true
	}

	schema := BuildSchema(columns, mandatory)

	partSpec, err := BuildPartitionSpec(columns, schema)
	if err != nil {
		return fmt.Errorf("building partition spec for %s: %w", tableName, err)
	}

	sortOrder, err := BuildSortOrder(columns, schema)
	if err != nil {
		return fmt.Errorf("building sort order for %s: %w", tableName, err)
	}

	props := BuildBloomFilterProperties(columns)
	props["format-version"] = "2"
	props["write.parquet.compression"] = "snappy"

	_, err = ic.cat.CreateTable(ctx, ident, schema,
		icecat.WithPartitionSpec(&partSpec),
		icecat.WithSortOrder(sortOrder),
		icecat.WithProperties(props),
	)
	if err != nil {
		return fmt.Errorf("creating table %s: %w", tableName, err)
	}

	return nil
}

func (ic *IcebergCatalog) RecordPut(ctx context.Context, rc RecordContext, headers http.Header, securityTagID string) error {
	now := time.Now().UTC()

	// First, try to delete the existing row (upsert = delete + insert)
	ic.deleteExistingRow(ctx, rc, securityTagID)

	switch ic.cfg.TableStrategy {
	case "single":
		return ic.appendRow(ctx, "object_metadata", ic.cfg.Columns.AllColumns(), rc, headers, securityTagID, now, nil)
	case "two_tier":
		if err := ic.appendRow(ctx, "base_metadata", ic.cfg.Columns.BaseColumns(), rc, headers, securityTagID, now, nil); err != nil {
			return err
		}
		detailTable := "detail_" + securityTagID
		return ic.appendRow(ctx, detailTable, ic.detailColumns(), rc, headers, securityTagID, now, nil)
	case "per_tag":
		tagTable := "objects_" + securityTagID
		return ic.appendRow(ctx, tagTable, ic.cfg.Columns.AllColumns(), rc, headers, securityTagID, now, nil)
	}
	return nil
}

func (ic *IcebergCatalog) RecordDelete(ctx context.Context, rc RecordContext, securityTagID string) error {
	now := time.Now().UTC()

	// Delete existing row and re-insert with deleted_at set
	ic.deleteExistingRow(ctx, rc, securityTagID)

	switch ic.cfg.TableStrategy {
	case "single":
		return ic.appendRow(ctx, "object_metadata", ic.cfg.Columns.AllColumns(), rc, nil, securityTagID, now, &now)
	case "two_tier":
		if err := ic.appendRow(ctx, "base_metadata", ic.cfg.Columns.BaseColumns(), rc, nil, securityTagID, now, &now); err != nil {
			return err
		}
		return ic.appendRow(ctx, "detail_"+securityTagID, ic.detailColumns(), rc, nil, securityTagID, now, &now)
	case "per_tag":
		return ic.appendRow(ctx, "objects_"+securityTagID, ic.cfg.Columns.AllColumns(), rc, nil, securityTagID, now, &now)
	}
	return nil
}

func (ic *IcebergCatalog) deleteExistingRow(ctx context.Context, rc RecordContext, securityTagID string) {
	filter := iceberg.NewAnd(
		iceberg.EqualTo(iceberg.Reference("object_key"), rc.Key),
		iceberg.EqualTo(iceberg.Reference("bucket"), rc.Bucket),
	)

	switch ic.cfg.TableStrategy {
	case "single":
		ic.deleteFromTable(ctx, "object_metadata", filter)
	case "two_tier":
		ic.deleteFromTable(ctx, "base_metadata", filter)
		ic.deleteFromTable(ctx, "detail_"+securityTagID, filter)
	case "per_tag":
		ic.deleteFromTable(ctx, "objects_"+securityTagID, filter)
	}
}

func (ic *IcebergCatalog) deleteFromTable(ctx context.Context, tableName string, filter iceberg.BooleanExpression) {
	ident := icecat.ToIdentifier(ic.cfg.CatalogNamespace, tableName)
	tbl, err := ic.cat.LoadTable(ctx, ident)
	if err != nil {
		return
	}
	tbl.Delete(ctx, filter, nil)
}

func (ic *IcebergCatalog) appendRow(ctx context.Context, tableName string, columns []ColumnDef, rc RecordContext, headers http.Header, securityTagID string, now time.Time, deletedAt *time.Time) error {
	ident := icecat.ToIdentifier(ic.cfg.CatalogNamespace, tableName)
	tbl, err := ic.cat.LoadTable(ctx, ident)
	if err != nil {
		return fmt.Errorf("loading table %s: %w", tableName, err)
	}

	record := ic.buildArrowRecord(columns, rc, headers, securityTagID, now, deletedAt, tbl.Schema())
	defer record.Release()

	_, err = tbl.AppendTable(ctx, buildArrowTable(tbl.Schema(), record), 1024, nil)
	return err
}

func (ic *IcebergCatalog) resolveValue(col ColumnDef, rc RecordContext, headers http.Header, securityTagID string, now time.Time, deletedAt *time.Time) (interface{}, bool) {
	switch col.Source {
	case "_key":
		return rc.Key, true
	case "_bucket":
		return rc.Bucket, true
	case "_account":
		return rc.AccountName, true
	case "_timestamp":
		return now, true
	case "_timestamp_updated":
		return now, true
	case "_timestamp_deleted":
		if deletedAt != nil {
			return *deletedAt, true
		}
		return nil, false
	default:
		if col.Source == ic.securityTagHeader || col.Name == "security_tag_id" {
			return securityTagID, true
		}
		if headers == nil {
			return nil, false
		}
		val := headers.Get(col.Source)
		if val == "" {
			return nil, false
		}
		return val, true
	}
}

func (ic *IcebergCatalog) buildArrowRecord(columns []ColumnDef, rc RecordContext, headers http.Header, securityTagID string, now time.Time, deletedAt *time.Time, schema *iceberg.Schema) arrow.Record {
	alloc := memory.NewGoAllocator()

	arrowFields := make([]arrow.Field, len(columns))
	for i, col := range columns {
		arrowFields[i] = arrow.Field{
			Name:     col.Name,
			Type:     icebergToArrowType(col.IcebergType),
			Nullable: !ic.cfg.Columns.IsMandatory(col.Name),
		}
	}
	arrowSchema := arrow.NewSchema(arrowFields, nil)

	bldr := array.NewRecordBuilder(alloc, arrowSchema)
	defer bldr.Release()

	for i, col := range columns {
		val, ok := ic.resolveValue(col, rc, headers, securityTagID, now, deletedAt)
		appendValue(bldr.Field(i), col.IcebergType, val, ok)
	}

	return bldr.NewRecord()
}

func icebergToArrowType(typeName string) arrow.DataType {
	switch typeName {
	case "string":
		return arrow.BinaryTypes.String
	case "long":
		return arrow.PrimitiveTypes.Int64
	case "int":
		return arrow.PrimitiveTypes.Int32
	case "boolean":
		return arrow.FixedWidthTypes.Boolean
	case "float":
		return arrow.PrimitiveTypes.Float32
	case "double":
		return arrow.PrimitiveTypes.Float64
	case "timestamptz":
		return arrow.FixedWidthTypes.Timestamp_us
	case "date":
		return arrow.FixedWidthTypes.Date32
	case "binary":
		return arrow.BinaryTypes.Binary
	default:
		return arrow.BinaryTypes.String
	}
}

func appendValue(builder array.Builder, icebergType string, val interface{}, present bool) {
	if !present || val == nil {
		builder.AppendNull()
		return
	}

	switch icebergType {
	case "string":
		builder.(*array.StringBuilder).Append(fmt.Sprintf("%v", val))
	case "long":
		switch v := val.(type) {
		case int64:
			builder.(*array.Int64Builder).Append(v)
		case string:
			var n int64
			fmt.Sscanf(v, "%d", &n)
			builder.(*array.Int64Builder).Append(n)
		default:
			builder.AppendNull()
		}
	case "int":
		switch v := val.(type) {
		case int32:
			builder.(*array.Int32Builder).Append(v)
		case int:
			builder.(*array.Int32Builder).Append(int32(v))
		default:
			builder.AppendNull()
		}
	case "boolean":
		switch v := val.(type) {
		case bool:
			builder.(*array.BooleanBuilder).Append(v)
		default:
			builder.AppendNull()
		}
	case "timestamptz":
		switch v := val.(type) {
		case time.Time:
			builder.(*array.TimestampBuilder).Append(arrow.Timestamp(v.UnixMicro()))
		default:
			builder.AppendNull()
		}
	default:
		builder.(*array.StringBuilder).Append(fmt.Sprintf("%v", val))
	}
}

func buildArrowTable(schema *iceberg.Schema, record arrow.Record) arrow.Table {
	return array.NewTableFromRecords(record.Schema(), []arrow.Record{record})
}

func (ic *IcebergCatalog) Close() error {
	return nil
}

func newSQLCatalog(cfg CatalogConfig) (icecat.Catalog, error) {
	props := iceberg.Properties{
		"uri":                  "sqlite://" + cfg.SQLitePath,
		"warehouse":           cfg.Storage.WarehousePath,
		"init_catalog_tables": "true",
	}
	return icecat.Load(context.Background(), "sql", props)
}

func newRESTCatalog(cfg CatalogConfig) (icecat.Catalog, error) {
	props := iceberg.Properties{
		"uri":       cfg.CatalogURI,
		"warehouse": cfg.Storage.WarehousePath,
	}
	return icecat.Load(context.Background(), "rest", props)
}
