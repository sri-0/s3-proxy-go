package backend

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrNotFound is returned when an object does not exist.
var ErrNotFound = errors.New("object not found")

// ObjectInfo holds metadata about a stored object.
type ObjectInfo struct {
	Key          string
	Size         int64
	ContentType  string
	ETag         string
	LastModified time.Time
}

// Backend abstracts S3 operations for a single storage account.
type Backend interface {
	GetObject(ctx context.Context, bucket, key string) (io.ReadCloser, ObjectInfo, error)
	PutObject(ctx context.Context, bucket, key string, data io.Reader, size int64, contentType string) error
	RemoveObject(ctx context.Context, bucket, key string) error
	ListObjects(ctx context.Context, bucket, prefix string) ([]ObjectInfo, error)
}
