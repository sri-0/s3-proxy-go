package proxy

import (
	"net/http"
	"strings"
)

// validateMandatoryHeaders checks that all required headers are present
// and non-empty. Returns a slice of missing header names.
func validateMandatoryHeaders(headers http.Header, required []string) []string {
	var missing []string
	for _, h := range required {
		val := strings.TrimSpace(headers.Get(h))
		if val == "" {
			missing = append(missing, h)
		}
	}
	return missing
}
