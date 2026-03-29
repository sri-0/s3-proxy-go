package catalog

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/apache/iceberg-go"
	"github.com/apache/iceberg-go/table"
)

func mapIcebergType(typeName string) iceberg.Type {
	switch typeName {
	case "string":
		return iceberg.PrimitiveTypes.String
	case "long":
		return iceberg.PrimitiveTypes.Int64
	case "int":
		return iceberg.PrimitiveTypes.Int32
	case "boolean":
		return iceberg.PrimitiveTypes.Bool
	case "float":
		return iceberg.PrimitiveTypes.Float32
	case "double":
		return iceberg.PrimitiveTypes.Float64
	case "timestamptz":
		return iceberg.PrimitiveTypes.TimestampTz
	case "date":
		return iceberg.PrimitiveTypes.Date
	case "binary":
		return iceberg.PrimitiveTypes.Binary
	case "uuid":
		return iceberg.PrimitiveTypes.UUID
	default:
		return iceberg.PrimitiveTypes.String
	}
}

func parseTransform(s string) (iceberg.Transform, error) {
	switch {
	case s == "identity":
		return iceberg.IdentityTransform{}, nil
	case s == "year":
		return iceberg.YearTransform{}, nil
	case s == "month":
		return iceberg.MonthTransform{}, nil
	case s == "day":
		return iceberg.DayTransform{}, nil
	case s == "hour":
		return iceberg.HourTransform{}, nil
	case strings.HasPrefix(s, "bucket[") && strings.HasSuffix(s, "]"):
		n, err := strconv.Atoi(s[7 : len(s)-1])
		if err != nil {
			return nil, fmt.Errorf("invalid bucket count: %w", err)
		}
		return iceberg.BucketTransform{NumBuckets: n}, nil
	case strings.HasPrefix(s, "truncate[") && strings.HasSuffix(s, "]"):
		n, err := strconv.Atoi(s[9 : len(s)-1])
		if err != nil {
			return nil, fmt.Errorf("invalid truncate width: %w", err)
		}
		return iceberg.TruncateTransform{Width: n}, nil
	default:
		return nil, fmt.Errorf("unknown transform %q", s)
	}
}

// BuildSchema creates an Iceberg schema from column definitions.
func BuildSchema(columns []ColumnDef, mandatory map[string]bool) *iceberg.Schema {
	fields := make([]iceberg.NestedField, len(columns))
	for i, col := range columns {
		fields[i] = iceberg.NestedField{
			ID:       i + 1,
			Name:     col.Name,
			Type:     mapIcebergType(col.IcebergType),
			Required: mandatory[col.Name],
		}
	}
	return iceberg.NewSchema(0, fields...)
}

// BuildPartitionSpec creates a partition spec from column definitions that have partitionBy set.
func BuildPartitionSpec(columns []ColumnDef, schema *iceberg.Schema) (iceberg.PartitionSpec, error) {
	var opts []iceberg.PartitionOption
	opts = append(opts, iceberg.WithSpecID(0))

	hasPartitions := false
	for _, col := range columns {
		if col.PartitionBy == "" {
			continue
		}
		transform, err := parseTransform(col.PartitionBy)
		if err != nil {
			return iceberg.PartitionSpec{}, fmt.Errorf("column %q: %w", col.Name, err)
		}

		field, ok := schema.FindFieldByName(col.Name)
		if !ok {
			return iceberg.PartitionSpec{}, fmt.Errorf("column %q not found in schema", col.Name)
		}

		partName := col.Name + "_part"
		if col.PartitionBy == "identity" {
			partName = col.Name
		}

		opts = append(opts, iceberg.AddPartitionFieldBySourceID(
			field.ID, partName, transform, schema, nil,
		))
		hasPartitions = true
	}

	if !hasPartitions {
		return *iceberg.UnpartitionedSpec, nil
	}

	return iceberg.NewPartitionSpecOpts(opts...)
}

// BuildSortOrder creates a sort order from column definitions that have sortOrder set.
func BuildSortOrder(columns []ColumnDef, schema *iceberg.Schema) (table.SortOrder, error) {
	var sortFields []table.SortField

	for _, col := range columns {
		if col.SortOrder == "" {
			continue
		}

		field, ok := schema.FindFieldByName(col.Name)
		if !ok {
			return table.UnsortedSortOrder, fmt.Errorf("column %q not found in schema", col.Name)
		}

		dir := table.SortASC
		nullOrder := table.NullsFirst
		if col.SortOrder == "desc" {
			dir = table.SortDESC
			nullOrder = table.NullsLast
		}

		sortFields = append(sortFields, table.SortField{
			SourceID:  field.ID,
			Transform: iceberg.IdentityTransform{},
			Direction: dir,
			NullOrder: nullOrder,
		})
	}

	if len(sortFields) == 0 {
		return table.UnsortedSortOrder, nil
	}

	return table.NewSortOrder(1, sortFields)
}

// BuildBloomFilterProperties returns Iceberg table properties for bloom filter config.
func BuildBloomFilterProperties(columns []ColumnDef) iceberg.Properties {
	props := iceberg.Properties{}
	hasAny := false

	for _, col := range columns {
		if col.BloomFilter {
			props["write.parquet.bloom-filter-enabled.column."+col.Name] = "true"
			hasAny = true
		}
	}

	if hasAny {
		props["write.parquet.bloom-filter-enabled"] = "true"
	}

	return props
}
