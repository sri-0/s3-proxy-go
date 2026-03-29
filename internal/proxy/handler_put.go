package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/sri/s3-proxy-go/internal/catalog"
	"github.com/sri/s3-proxy-go/internal/meta"
)

func handlePutObject(rc *RouteConfig, w http.ResponseWriter, r *http.Request) {
	bucket, key := extractBucketKey(rc, r)

	missing := validateMandatoryHeaders(r.Header, rc.MandatoryHeaders)
	if len(missing) > 0 {
		msg := fmt.Sprintf("Bad Request: missing required headers: %s", strings.Join(missing, ", "))
		http.Error(w, msg, http.StatusBadRequest)
		return
	}

	securityTag := r.Header.Get(rc.SecurityTagHeader)
	if !rc.AllowedTags[securityTag] {
		msg := fmt.Sprintf("Bad Request: security tag %q is not in the allowed list", securityTag)
		http.Error(w, msg, http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusInternalServerError)
		return
	}

	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	err = rc.Backend.PutObject(r.Context(), bucket, key, bytes.NewReader(body), int64(len(body)), contentType)
	if err != nil {
		log.Printf("PutObject %s/%s: backend error: %v", bucket, key, err)
		http.Error(w, "Bad gateway", http.StatusBadGateway)
		return
	}

	objMeta := meta.BuildFromHeaders(r.Header, rc.SecurityTagHeader, rc.ExcludedMetaHeaders)
	metaData, err := json.Marshal(objMeta)
	if err != nil {
		log.Printf("PutObject %s/%s: failed to marshal meta: %v", bucket, key, err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	err = rc.Backend.PutObject(r.Context(), bucket, key+".meta", bytes.NewReader(metaData), int64(len(metaData)), "application/json")
	if err != nil {
		log.Printf("PutObject %s/%s: failed to store .meta: %v", bucket, key, err)
		http.Error(w, "Failed to store metadata", http.StatusInternalServerError)
		return
	}

	if rc.Catalog != nil {
		recCtx := catalog.RecordContext{
			AccountName: rc.AccountName,
			Bucket:      bucket,
			Key:         key,
		}
		if err := rc.Catalog.RecordPut(r.Context(), recCtx, r.Header, securityTag); err != nil {
			log.Printf("PutObject %s/%s: catalog record failed: %v", bucket, key, err)
		}
	}

	w.WriteHeader(http.StatusOK)
}
