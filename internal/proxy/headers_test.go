package proxy

import (
	"net/http"
	"testing"
)

func TestValidateMandatoryHeaders(t *testing.T) {
	tests := []struct {
		name     string
		headers  http.Header
		required []string
		wantErr  bool
	}{
		{
			name:     "all present",
			headers:  http.Header{"X-Securitytagid": []string{"C1000"}},
			required: []string{"X-Securitytagid"},
			wantErr:  false,
		},
		{
			name:     "missing required",
			headers:  http.Header{},
			required: []string{"X-Securitytagid"},
			wantErr:  true,
		},
		{
			name: "multiple all present",
			headers: http.Header{
				"X-Securitytagid":  []string{"C1000"},
				"X-Classification": []string{"ALPHA"},
			},
			required: []string{"X-Securitytagid", "X-Classification"},
			wantErr:  false,
		},
		{
			name:     "one missing",
			headers:  http.Header{"X-Securitytagid": []string{"C1000"}},
			required: []string{"X-Securitytagid", "X-Classification"},
			wantErr:  true,
		},
		{
			name:     "empty value",
			headers:  http.Header{"X-Securitytagid": []string{""}},
			required: []string{"X-Securitytagid"},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			missing := validateMandatoryHeaders(tt.headers, tt.required)
			if tt.wantErr && len(missing) == 0 {
				t.Error("expected missing headers")
			}
			if !tt.wantErr && len(missing) > 0 {
				t.Errorf("unexpected missing: %v", missing)
			}
		})
	}
}
