package proxy

import (
	"net/http"

	"github.com/gorilla/mux"
	"github.com/sri/s3-proxy-go/internal/backend"
)

type RouteConfig struct {
	Backend             backend.Backend
	AccountName         string
	Bucket              string // fixed bucket name, or empty if dynamic from URL
	AllowedOps          map[string]bool
	AllowedTags         map[string]bool
	MandatoryHeaders    []string
	SecurityTagHeader   string
	ExcludedMetaHeaders map[string]bool
}

func extractBucketKey(rc *RouteConfig, r *http.Request) (string, string) {
	vars := mux.Vars(r)
	bucket := rc.Bucket
	if bucket == "" {
		bucket = vars["bucket"]
	}
	key := vars["key"]
	return bucket, key
}

func extractBucket(rc *RouteConfig, r *http.Request) string {
	if rc.Bucket != "" {
		return rc.Bucket
	}
	return mux.Vars(r)["bucket"]
}
