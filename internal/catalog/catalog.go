package catalog

import (
	"context"
	"net/http"
	"time"
)

// RecordContext carries non-header data for a catalog entry.
type RecordContext struct {
	AccountName string
	Bucket      string
	Key         string
}

// Catalog tracks S3 object metadata in an Iceberg table.
type Catalog interface {
	// RecordPut upserts an object entry. If it already exists, updates
	// updated_at and metadata columns. If new, sets created_at.
	RecordPut(ctx context.Context, rc RecordContext, headers http.Header, securityTagID string) error

	// RecordDelete sets deleted_at on the object entry. The row is
	// retained so the catalog always has a record of the object existing.
	RecordDelete(ctx context.Context, rc RecordContext, securityTagID string) error

	// Query returns object entries the caller is authorized to see.
	Query(ctx context.Context, params QueryParams, callerRoles []string) (*QueryResult, error)

	// Close releases resources.
	Close() error
}

// QueryParams controls what the catalog query returns.
type QueryParams struct {
	Bucket         string
	KeyPrefix      string
	SecurityTag    string
	Account        string
	IncludeDeleted bool
	Limit          int
	Offset         int
	Filters        map[string]string
}

// QueryResult is a page of catalog entries.
type QueryResult struct {
	Rows       []map[string]interface{}
	TotalCount int
	HasMore    bool
}

// ObjectEntry represents the current state of a cataloged object.
type ObjectEntry struct {
	ObjectKey     string
	Bucket        string
	SecurityTagID string
	AccountName   string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	DeletedAt     *time.Time
	Metadata      map[string]string
}
