package meta

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestParse(t *testing.T) {
	raw := `{"securityTagId":"C1000","X-Custom":"value"}`
	m, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.SecurityTagID != "C1000" {
		t.Errorf("securityTagId = %s, want C1000", m.SecurityTagID)
	}
	if m.Extra["X-Custom"] != "value" {
		t.Errorf("extra[X-Custom] = %v, want value", m.Extra["X-Custom"])
	}
}

func TestMarshalJSON_Flat(t *testing.T) {
	m := &ObjectMeta{
		SecurityTagID: "C1000",
		Extra:         map[string]string{"X-Custom": "val"},
	}

	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var parsed map[string]interface{}
	json.Unmarshal(data, &parsed)

	if parsed["securityTagId"] != "C1000" {
		t.Errorf("expected securityTagId=C1000")
	}
	if parsed["X-Custom"] != "val" {
		t.Errorf("expected X-Custom=val")
	}
}

func TestBuildFromHeaders(t *testing.T) {
	excluded := map[string]bool{
		"Authorization":  true,
		"Content-Length": true,
		"Host":           true,
		"Connection":     true,
	}

	headers := http.Header{
		"X-Securitytagid": []string{"C1000"},
		"X-Custom":        []string{"val1"},
		"Content-Type":    []string{"application/octet-stream"},
		"Authorization":   []string{"Bearer xxx"},
	}

	m := BuildFromHeaders(headers, "X-Securitytagid", excluded)

	if m.SecurityTagID != "C1000" {
		t.Errorf("securityTagId = %s, want C1000", m.SecurityTagID)
	}
	if _, ok := m.Extra["Authorization"]; ok {
		t.Error("Authorization should be excluded")
	}
	if m.Extra["Content-Type"] != "application/octet-stream" {
		t.Errorf("Content-Type not in meta")
	}
}

func TestBuildFromHeaders_CustomTagHeader(t *testing.T) {
	excluded := map[string]bool{"Authorization": true}

	headers := http.Header{
		"X-Data-Label":  []string{"C1001"},
		"Authorization": []string{"Bearer xxx"},
		"X-Custom":      []string{"val"},
	}

	m := BuildFromHeaders(headers, "X-Data-Label", excluded)

	if m.SecurityTagID != "C1001" {
		t.Errorf("securityTagId = %s, want C1001", m.SecurityTagID)
	}
	if m.Extra["X-Custom"] != "val" {
		t.Errorf("extra missing X-Custom")
	}
}

func TestRoundTrip(t *testing.T) {
	original := &ObjectMeta{
		SecurityTagID: "C1001",
		Extra:         map[string]string{"X-Origin": "test", "Content-Type": "text/plain"},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	parsed, err := Parse(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if parsed.SecurityTagID != original.SecurityTagID {
		t.Errorf("securityTagId mismatch")
	}
	if parsed.Extra["X-Origin"] != "test" {
		t.Errorf("extra mismatch")
	}
}
