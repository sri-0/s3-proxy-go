package meta

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type ObjectMeta struct {
	SecurityTagID string            `json:"securityTagId"`
	Extra         map[string]string `json:"-"`
}

func (m *ObjectMeta) MarshalJSON() ([]byte, error) {
	obj := make(map[string]string)
	obj["securityTagId"] = m.SecurityTagID
	for k, v := range m.Extra {
		obj[k] = v
	}
	return json.Marshal(obj)
}

func (m *ObjectMeta) UnmarshalJSON(data []byte) error {
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("invalid meta JSON: %w", err)
	}

	m.Extra = make(map[string]string)

	for k, v := range raw {
		if k == "securityTagId" {
			if s, ok := v.(string); ok {
				m.SecurityTagID = s
			}
		} else {
			if s, ok := v.(string); ok {
				m.Extra[k] = s
			}
		}
	}

	return nil
}

func Parse(data []byte) (*ObjectMeta, error) {
	var m ObjectMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// BuildFromHeaders constructs an ObjectMeta from HTTP request headers.
// securityTagHeader is the header name that carries the security tag ID.
// excludedHeaders is the set of headers to omit from the .meta file.
func BuildFromHeaders(headers http.Header, securityTagHeader string, excludedHeaders map[string]bool) *ObjectMeta {
	m := &ObjectMeta{
		Extra: make(map[string]string),
	}

	canonicalTag := http.CanonicalHeaderKey(securityTagHeader)

	for name, values := range headers {
		canonical := http.CanonicalHeaderKey(name)
		if excludedHeaders[canonical] {
			continue
		}

		val := strings.Join(values, ", ")

		if canonical == canonicalTag {
			m.SecurityTagID = val
		} else {
			m.Extra[canonical] = val
		}
	}

	return m
}
