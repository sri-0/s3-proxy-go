package catalog

import (
	"context"
	"fmt"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/iceberg-go"
	icecat "github.com/apache/iceberg-go/catalog"
	icetable "github.com/apache/iceberg-go/table"
)

func (ic *IcebergCatalog) Query(ctx context.Context, params QueryParams, callerRoles []string) (*QueryResult, error) {
	if params.Limit <= 0 {
		params.Limit = 100
	}
	if params.Limit > 1000 {
		params.Limit = 1000
	}

	switch ic.cfg.TableStrategy {
	case "single":
		return ic.querySingleTable(ctx, params, callerRoles)
	case "two_tier":
		return ic.queryTwoTier(ctx, params, callerRoles)
	case "per_tag":
		return ic.queryPerTag(ctx, params, callerRoles)
	default:
		return nil, fmt.Errorf("unknown table strategy: %s", ic.cfg.TableStrategy)
	}
}

func (ic *IcebergCatalog) querySingleTable(ctx context.Context, params QueryParams, callerRoles []string) (*QueryResult, error) {
	filter := ic.buildFilter(params, callerRoles, true)
	return ic.scanTable(ctx, "object_metadata", filter, nil, params)
}

func (ic *IcebergCatalog) queryTwoTier(ctx context.Context, params QueryParams, callerRoles []string) (*QueryResult, error) {
	baseFilter := ic.buildFilter(params, nil, false)
	baseResult, err := ic.scanTable(ctx, "base_metadata", baseFilter, nil, params)
	if err != nil {
		return nil, err
	}

	if params.SecurityTag == "" {
		return baseResult, nil
	}

	detailFilter := ic.buildFilter(params, nil, false)
	detailTable := "detail_" + params.SecurityTag
	detailResult, err := ic.scanTable(ctx, detailTable, detailFilter, nil, params)
	if err != nil {
		return baseResult, nil
	}

	return mergeResults(baseResult, detailResult), nil
}

func (ic *IcebergCatalog) queryPerTag(ctx context.Context, params QueryParams, callerRoles []string) (*QueryResult, error) {
	var tags []string
	if params.SecurityTag != "" {
		tags = []string{params.SecurityTag}
	} else {
		tags = callerRoles
	}

	var allRows []map[string]interface{}
	for _, tag := range tags {
		tableName := "objects_" + tag
		filter := ic.buildFilter(params, nil, false)
		result, err := ic.scanTable(ctx, tableName, filter, nil, params)
		if err != nil {
			continue
		}
		allRows = append(allRows, result.Rows...)
	}

	limit := params.Limit
	hasMore := len(allRows) > limit
	if hasMore {
		allRows = allRows[:limit]
	}

	return &QueryResult{
		Rows:       allRows,
		TotalCount: len(allRows),
		HasMore:    hasMore,
	}, nil
}

func (ic *IcebergCatalog) buildFilter(params QueryParams, callerRoles []string, includeTagFilter bool) iceberg.BooleanExpression {
	var filters []iceberg.BooleanExpression

	if params.Bucket != "" {
		filters = append(filters, iceberg.EqualTo(iceberg.Reference("bucket"), params.Bucket))
	}
	if params.KeyPrefix != "" {
		filters = append(filters, iceberg.StartsWith(iceberg.Reference("object_key"), params.KeyPrefix))
	}
	if params.Account != "" {
		filters = append(filters, iceberg.EqualTo(iceberg.Reference("account_name"), params.Account))
	}
	if !params.IncludeDeleted {
		filters = append(filters, iceberg.IsNull(iceberg.Reference("deleted_at")))
	}

	if includeTagFilter && len(callerRoles) > 0 {
		if params.SecurityTag != "" {
			filters = append(filters, iceberg.EqualTo(iceberg.Reference("security_tag_id"), params.SecurityTag))
		} else {
			filters = append(filters, iceberg.IsIn(iceberg.Reference("security_tag_id"), callerRoles...))
		}
	}

	for colName, colVal := range params.Filters {
		filters = append(filters, iceberg.EqualTo(iceberg.Reference(colName), colVal))
	}

	if len(filters) == 0 {
		return iceberg.AlwaysTrue{}
	}

	result := filters[0]
	for _, f := range filters[1:] {
		result = iceberg.NewAnd(result, f)
	}
	return result
}

func (ic *IcebergCatalog) scanTable(ctx context.Context, tableName string, filter iceberg.BooleanExpression, selectedFields []string, params QueryParams) (*QueryResult, error) {
	ident := icecat.ToIdentifier(ic.cfg.CatalogNamespace, tableName)
	tbl, err := ic.cat.LoadTable(ctx, ident)
	if err != nil {
		return nil, fmt.Errorf("loading table %s: %w", tableName, err)
	}

	scanOpts := []icetable.ScanOption{
		icetable.WithRowFilter(filter),
		icetable.WithLimit(int64(params.Limit + 1)),
	}
	if len(selectedFields) > 0 {
		scanOpts = append(scanOpts, icetable.WithSelectedFields(selectedFields...))
	}

	scan := tbl.Scan(scanOpts...)

	arrowTable, err := scan.ToArrowTable(ctx)
	if err != nil {
		return nil, fmt.Errorf("scanning table %s: %w", tableName, err)
	}
	defer arrowTable.Release()

	rows := arrowTableToMaps(arrowTable)

	hasMore := len(rows) > params.Limit
	if hasMore {
		rows = rows[:params.Limit]
	}

	return &QueryResult{
		Rows:       rows,
		TotalCount: len(rows),
		HasMore:    hasMore,
	}, nil
}

func arrowTableToMaps(tbl arrow.Table) []map[string]interface{} {
	schema := tbl.Schema()
	numRows := int(tbl.NumRows())
	numCols := int(tbl.NumCols())

	rows := make([]map[string]interface{}, 0, numRows)

	for i := 0; i < numRows; i++ {
		row := make(map[string]interface{}, numCols)
		for j := 0; j < numCols; j++ {
			col := tbl.Column(j)
			fieldName := schema.Field(j).Name
			chunk := col.Data()
			if chunk.Len() == 0 {
				continue
			}

			offset := i
			var arr arrow.Array
			for _, c := range chunk.Chunks() {
				if offset < c.Len() {
					arr = c
					break
				}
				offset -= c.Len()
			}
			if arr == nil || arr.IsNull(offset) {
				continue
			}

			row[fieldName] = extractArrowValue(arr, offset)
		}
		rows = append(rows, row)
	}

	return rows
}

func extractArrowValue(arr arrow.Array, idx int) interface{} {
	switch a := arr.(type) {
	case *array.String:
		return a.Value(idx)
	case *array.Int64:
		return a.Value(idx)
	case *array.Int32:
		return a.Value(idx)
	case *array.Float32:
		return a.Value(idx)
	case *array.Float64:
		return a.Value(idx)
	case *array.Boolean:
		return a.Value(idx)
	case *array.Timestamp:
		return time.UnixMicro(int64(a.Value(idx))).UTC()
	case *array.Date32:
		return a.Value(idx)
	default:
		return fmt.Sprintf("%v", a)
	}
}

func mergeResults(base, detail *QueryResult) *QueryResult {
	detailByKey := make(map[string]map[string]interface{})
	for _, row := range detail.Rows {
		key := fmt.Sprintf("%v:%v", row["bucket"], row["object_key"])
		detailByKey[key] = row
	}

	for _, row := range base.Rows {
		key := fmt.Sprintf("%v:%v", row["bucket"], row["object_key"])
		if d, ok := detailByKey[key]; ok {
			for k, v := range d {
				if k != "object_key" && k != "bucket" && k != "created_at" {
					row[k] = v
				}
			}
		}
	}

	return base
}
