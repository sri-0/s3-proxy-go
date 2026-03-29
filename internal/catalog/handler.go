package catalog

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"github.com/sri/s3-proxy-go/internal/auth"
)

type Handler struct {
	catalog           Catalog
	securityTagHeader string
}

func NewHandler(cat Catalog, securityTagHeader string) *Handler {
	return &Handler{catalog: cat, securityTagHeader: securityTagHeader}
}

func (h *Handler) HandleQuery(w http.ResponseWriter, r *http.Request) {
	roles, ok := h.authenticate(w, r)
	if !ok {
		return
	}

	params := h.parseQueryParams(r)

	if params.SecurityTag != "" && !auth.ValidateSecurityTag(params.SecurityTag, roles) {
		http.Error(w, "Forbidden: insufficient role for requested security tag", http.StatusForbidden)
		return
	}

	result, err := h.catalog.Query(r.Context(), params, roles)
	if err != nil {
		log.Printf("catalog query error: %v", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (h *Handler) HandleObjectHistory(w http.ResponseWriter, r *http.Request) {
	roles, ok := h.authenticate(w, r)
	if !ok {
		return
	}

	// Extract bucket and key from URL: /catalog/objects/{bucket}/{key...}
	// Trim the prefix to get bucket/key
	path := r.URL.Path
	const prefix = "/catalog/objects/"
	if len(path) <= len(prefix) {
		http.Error(w, "Bad Request: missing bucket and key", http.StatusBadRequest)
		return
	}
	remainder := path[len(prefix):]

	// Split into bucket and key
	slashIdx := -1
	for i, c := range remainder {
		if c == '/' {
			slashIdx = i
			break
		}
	}
	if slashIdx < 0 {
		http.Error(w, "Bad Request: missing key", http.StatusBadRequest)
		return
	}

	bucket := remainder[:slashIdx]
	key := remainder[slashIdx+1:]

	params := QueryParams{
		Bucket:         bucket,
		KeyPrefix:      key,
		IncludeDeleted: true,
		Limit:          100,
		Filters:        map[string]string{"object_key": key},
	}

	result, err := h.catalog.Query(r.Context(), params, roles)
	if err != nil {
		log.Printf("catalog object history error: %v", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (h *Handler) HandleStats(w http.ResponseWriter, r *http.Request) {
	roles, ok := h.authenticate(w, r)
	if !ok {
		return
	}

	params := QueryParams{
		Bucket:         r.URL.Query().Get("bucket"),
		Account:        r.URL.Query().Get("account"),
		IncludeDeleted: false,
		Limit:          1000,
	}

	result, err := h.catalog.Query(r.Context(), params, roles)
	if err != nil {
		log.Printf("catalog stats error: %v", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	stats := map[string]interface{}{
		"totalObjects": result.TotalCount,
		"byContentType": groupBy(result.Rows, "content_type"),
		"byAccount":     groupBy(result.Rows, "account_name"),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) ([]string, bool) {
	token, err := auth.ExtractBearerToken(r.Header.Get("Authorization"))
	if err != nil {
		http.Error(w, "Unauthorized: "+err.Error(), http.StatusUnauthorized)
		return nil, false
	}

	roles, err := auth.ExtractRolesFromToken(token)
	if err != nil {
		http.Error(w, "Unauthorized: invalid token", http.StatusUnauthorized)
		return nil, false
	}
	if len(roles) == 0 {
		http.Error(w, "Forbidden: no roles in token", http.StatusForbidden)
		return nil, false
	}

	return roles, true
}

func (h *Handler) parseQueryParams(r *http.Request) QueryParams {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	includeDeleted := r.URL.Query().Get("includeDeleted") == "true"

	params := QueryParams{
		Bucket:         r.URL.Query().Get("bucket"),
		KeyPrefix:      r.URL.Query().Get("keyPrefix"),
		SecurityTag:    r.URL.Query().Get("securityTag"),
		Account:        r.URL.Query().Get("account"),
		IncludeDeleted: includeDeleted,
		Limit:          limit,
		Offset:         offset,
		Filters:        make(map[string]string),
	}

	knownParams := map[string]bool{
		"bucket": true, "keyPrefix": true, "securityTag": true,
		"account": true, "includeDeleted": true, "limit": true, "offset": true,
	}

	for key, values := range r.URL.Query() {
		if !knownParams[key] && len(values) > 0 {
			params.Filters[key] = values[0]
		}
	}

	return params
}

func groupBy(rows []map[string]interface{}, field string) map[string]int {
	counts := make(map[string]int)
	for _, row := range rows {
		val, ok := row[field]
		if !ok {
			continue
		}
		key, ok := val.(string)
		if !ok {
			continue
		}
		counts[key]++
	}
	return counts
}
