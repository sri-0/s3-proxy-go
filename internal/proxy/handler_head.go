package proxy

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/sri/s3-proxy-go/internal/backend"
)

func handleHeadObject(rc *RouteConfig, w http.ResponseWriter, r *http.Request) {
	bucket, key := extractBucketKey(rc, r)

	obj, info, err := rc.Backend.GetObject(r.Context(), bucket, key)
	if err != nil {
		if errors.Is(err, backend.ErrNotFound) {
			http.Error(w, "Not Found", http.StatusNotFound)
			return
		}
		http.Error(w, "Bad gateway", http.StatusBadGateway)
		return
	}
	obj.Close()

	if info.ContentType != "" {
		w.Header().Set("Content-Type", info.ContentType)
	}
	if info.ETag != "" {
		w.Header().Set("ETag", info.ETag)
	}
	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size))
	w.WriteHeader(http.StatusOK)
}
