package proxy

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"

	"github.com/sri/s3-proxy-go/internal/auth"
	"github.com/sri/s3-proxy-go/internal/backend"
	"github.com/sri/s3-proxy-go/internal/catalog"
	"github.com/sri/s3-proxy-go/internal/meta"
)

func handleDeleteObject(rc *RouteConfig, w http.ResponseWriter, r *http.Request) {
	bucket, key := extractBucketKey(rc, r)

	token, err := auth.ExtractBearerToken(r.Header.Get("Authorization"))
	if err != nil {
		http.Error(w, "Unauthorized: "+err.Error(), http.StatusUnauthorized)
		return
	}

	roles, err := auth.ExtractRolesFromToken(token)
	if err != nil {
		http.Error(w, "Unauthorized: invalid token", http.StatusUnauthorized)
		return
	}

	metaRC, _, err := rc.Backend.GetObject(r.Context(), bucket, key+".meta")
	if err != nil {
		if errors.Is(err, backend.ErrNotFound) {
			log.Printf("DeleteObject %s/%s: .meta not found", bucket, key)
			http.Error(w, "Forbidden: no security metadata for object", http.StatusForbidden)
			return
		}
		log.Printf("DeleteObject %s/%s: failed to fetch .meta: %v", bucket, key, err)
		http.Error(w, "Forbidden: unable to verify security tag", http.StatusForbidden)
		return
	}

	metaBytes, err := io.ReadAll(metaRC)
	metaRC.Close()
	if err != nil {
		http.Error(w, "Internal error reading metadata", http.StatusInternalServerError)
		return
	}

	var objMeta meta.ObjectMeta
	if err := json.Unmarshal(metaBytes, &objMeta); err != nil {
		log.Printf("DeleteObject %s/%s: invalid .meta JSON: %v", bucket, key, err)
		http.Error(w, "Forbidden: corrupted security metadata", http.StatusForbidden)
		return
	}

	if !auth.ValidateSecurityTag(objMeta.SecurityTagID, roles) {
		log.Printf("DeleteObject %s/%s: role mismatch, tag=%s roles=%v", bucket, key, objMeta.SecurityTagID, roles)
		http.Error(w, "Forbidden: security tag mismatch", http.StatusForbidden)
		return
	}

	if err := rc.Backend.RemoveObject(r.Context(), bucket, key); err != nil {
		log.Printf("DeleteObject %s/%s: backend error: %v", bucket, key, err)
		http.Error(w, "Bad gateway", http.StatusBadGateway)
		return
	}

	if err := rc.Backend.RemoveObject(r.Context(), bucket, key+".meta"); err != nil {
		log.Printf("DeleteObject %s/%s: failed to remove .meta: %v", bucket, key, err)
	}

	if rc.Catalog != nil {
		recCtx := catalog.RecordContext{
			AccountName: rc.AccountName,
			Bucket:      bucket,
			Key:         key,
		}
		if err := rc.Catalog.RecordDelete(r.Context(), recCtx, objMeta.SecurityTagID); err != nil {
			log.Printf("DeleteObject %s/%s: catalog record failed: %v", bucket, key, err)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}
