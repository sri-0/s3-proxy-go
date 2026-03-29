package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/sri/s3-proxy-go/internal/backend"
	"github.com/sri/s3-proxy-go/internal/config"
)

func makeToken(roles []string) string {
	claims := jwt.MapClaims{
		"sub":   "user1",
		"roles": roles,
		"exp":   time.Now().Add(time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, _ := token.SignedString([]byte("test-secret"))
	return s
}

func seedMeta(t *testing.T, be *backend.MemoryBackend, bucket, key, tag string) {
	t.Helper()
	meta := map[string]string{"securityTagId": tag}
	data, _ := json.Marshal(meta)
	be.PutObject(context.Background(), bucket, key+".meta", bytes.NewReader(data), int64(len(data)), "application/json")
}

func setupTestRouter(t *testing.T) (*httptest.Server, *backend.MemoryBackend, *backend.MemoryBackend) {
	t.Helper()

	primaryBE := backend.NewMemoryBackend()
	archiveBE := backend.NewMemoryBackend()

	cfg := &config.Config{
		ListenAddr:          ":0",
		AllowedSecurityTags: []string{"C1000", "C1001", "C1003"},
		SecurityTagHeader:   "X-Securitytagid",
		MandatoryPutHeaders: []string{"X-Securitytagid"},
		ExcludedMetaHeaders: []string{"Authorization", "Content-Length", "Host", "Connection"},
		Accounts: []config.AccountConfig{
			{
				Name:              "primary",
				PathPrefix:        "/primary",
				AllowedOperations: []string{"LIST", "GET", "PUT", "DELETE", "HEAD"},
				Buckets: []config.BucketConfig{
					{Name: "docs", AllowedOperations: []string{"LIST", "GET", "PUT", "DELETE", "HEAD"}},
					{Name: "images", AllowedOperations: []string{"LIST", "GET", "PUT", "HEAD"}},
				},
			},
			{
				Name:              "archive",
				PathPrefix:        "",
				AllowedOperations: []string{"LIST", "GET", "HEAD"},
				Buckets: []config.BucketConfig{
					{Name: "backups"},
				},
			},
		},
	}

	backends := map[string]backend.Backend{
		"primary": primaryBE,
		"archive": archiveBE,
	}

	router := BuildRouter(cfg, backends)
	ts := httptest.NewServer(router)
	t.Cleanup(ts.Close)

	return ts, primaryBE, archiveBE
}

// --- PutObject ---

func TestPutObject_Success(t *testing.T) {
	ts, primaryBE, _ := setupTestRouter(t)

	req, _ := http.NewRequest("PUT", ts.URL+"/primary/docs/file.txt", strings.NewReader("hello"))
	req.Header.Set("X-Securitytagid", "C1000")
	req.Header.Set("Content-Type", "text/plain")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT status = %d, want 200, body: %s", resp.StatusCode, body)
	}

	rc, _, err := primaryBE.GetObject(context.Background(), "docs", "file.txt")
	if err != nil {
		t.Fatalf("object not found: %v", err)
	}
	data, _ := io.ReadAll(rc)
	if string(data) != "hello" {
		t.Errorf("object data = %q, want hello", data)
	}

	metaRC, _, err := primaryBE.GetObject(context.Background(), "docs", "file.txt.meta")
	if err != nil {
		t.Fatal(".meta not found")
	}
	metaData, _ := io.ReadAll(metaRC)
	var meta map[string]interface{}
	json.Unmarshal(metaData, &meta)
	if meta["securityTagId"] != "C1000" {
		t.Errorf("meta securityTagId = %v", meta["securityTagId"])
	}
}

func TestPutObject_MissingMandatoryHeader(t *testing.T) {
	ts, _, _ := setupTestRouter(t)

	req, _ := http.NewRequest("PUT", ts.URL+"/primary/docs/file.txt", strings.NewReader("data"))
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 400 {
		t.Errorf("PUT without header: status = %d, want 400", resp.StatusCode)
	}
}

func TestPutObject_DisallowedSecurityTag(t *testing.T) {
	ts, _, _ := setupTestRouter(t)

	req, _ := http.NewRequest("PUT", ts.URL+"/primary/docs/file.txt", strings.NewReader("data"))
	req.Header.Set("X-Securitytagid", "C9999")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 400 {
		t.Errorf("PUT with disallowed tag: status = %d, want 400", resp.StatusCode)
	}
}

// --- GetObject ---

func TestGetObject_Authorized(t *testing.T) {
	ts, primaryBE, _ := setupTestRouter(t)

	primaryBE.PutObject(context.Background(), "docs", "report.txt",
		strings.NewReader("report content"), 14, "text/plain")
	seedMeta(t, primaryBE, "docs", "report.txt", "C1000")

	req, _ := http.NewRequest("GET", ts.URL+"/primary/docs/report.txt", nil)
	req.Header.Set("Authorization", "Bearer "+makeToken([]string{"C1000"}))
	resp, _ := http.DefaultClient.Do(req)

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET status = %d, want 200, body: %s", resp.StatusCode, body)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "report content" {
		t.Errorf("body = %q", body)
	}
}

func TestGetObject_WrongRole(t *testing.T) {
	ts, primaryBE, _ := setupTestRouter(t)

	primaryBE.PutObject(context.Background(), "docs", "report.txt",
		strings.NewReader("data"), 4, "text/plain")
	seedMeta(t, primaryBE, "docs", "report.txt", "C1001")

	req, _ := http.NewRequest("GET", ts.URL+"/primary/docs/report.txt", nil)
	req.Header.Set("Authorization", "Bearer "+makeToken([]string{"C1003"}))
	resp, _ := http.DefaultClient.Do(req)

	if resp.StatusCode != 403 {
		t.Errorf("GET wrong role: status = %d, want 403", resp.StatusCode)
	}
}

func TestGetObject_NoAuth(t *testing.T) {
	ts, primaryBE, _ := setupTestRouter(t)

	primaryBE.PutObject(context.Background(), "docs", "file.txt",
		strings.NewReader("data"), 4, "text/plain")
	seedMeta(t, primaryBE, "docs", "file.txt", "C1000")

	req, _ := http.NewRequest("GET", ts.URL+"/primary/docs/file.txt", nil)
	resp, _ := http.DefaultClient.Do(req)

	if resp.StatusCode != 401 {
		t.Errorf("GET no auth: status = %d, want 401", resp.StatusCode)
	}
}

func TestGetObject_NoMeta(t *testing.T) {
	ts, primaryBE, _ := setupTestRouter(t)

	primaryBE.PutObject(context.Background(), "docs", "nometa.txt",
		strings.NewReader("data"), 4, "text/plain")

	req, _ := http.NewRequest("GET", ts.URL+"/primary/docs/nometa.txt", nil)
	req.Header.Set("Authorization", "Bearer "+makeToken([]string{"C1000"}))
	resp, _ := http.DefaultClient.Do(req)

	if resp.StatusCode != 403 {
		t.Errorf("GET no meta: status = %d, want 403", resp.StatusCode)
	}
}

// --- DeleteObject ---

func TestDeleteObject_Allowed(t *testing.T) {
	ts, primaryBE, _ := setupTestRouter(t)

	primaryBE.PutObject(context.Background(), "docs", "todelete.txt",
		strings.NewReader("data"), 4, "text/plain")
	seedMeta(t, primaryBE, "docs", "todelete.txt", "C1000")

	req, _ := http.NewRequest("DELETE", ts.URL+"/primary/docs/todelete.txt", nil)
	req.Header.Set("Authorization", "Bearer "+makeToken([]string{"C1000"}))
	resp, _ := http.DefaultClient.Do(req)

	if resp.StatusCode != 204 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("DELETE status = %d, want 204, body: %s", resp.StatusCode, body)
	}

	_, _, err := primaryBE.GetObject(context.Background(), "docs", "todelete.txt")
	if err == nil {
		t.Error("object should have been deleted")
	}
}

func TestDeleteObject_NotPermittedByBucketOps(t *testing.T) {
	ts, primaryBE, _ := setupTestRouter(t)

	// "images" bucket only allows LIST, GET, PUT, HEAD — no DELETE
	primaryBE.PutObject(context.Background(), "images", "photo.jpg",
		strings.NewReader("data"), 4, "image/jpeg")
	seedMeta(t, primaryBE, "images", "photo.jpg", "C1000")

	req, _ := http.NewRequest("DELETE", ts.URL+"/primary/images/photo.jpg", nil)
	req.Header.Set("Authorization", "Bearer "+makeToken([]string{"C1000"}))
	resp, _ := http.DefaultClient.Do(req)

	if resp.StatusCode != 405 {
		t.Errorf("DELETE not permitted: status = %d, want 405", resp.StatusCode)
	}
}

func TestDeleteObject_WrongRole(t *testing.T) {
	ts, primaryBE, _ := setupTestRouter(t)

	primaryBE.PutObject(context.Background(), "docs", "todelete.txt",
		strings.NewReader("data"), 4, "text/plain")
	seedMeta(t, primaryBE, "docs", "todelete.txt", "C1001")

	req, _ := http.NewRequest("DELETE", ts.URL+"/primary/docs/todelete.txt", nil)
	req.Header.Set("Authorization", "Bearer "+makeToken([]string{"C1003"}))
	resp, _ := http.DefaultClient.Do(req)

	if resp.StatusCode != 403 {
		t.Errorf("DELETE wrong role: status = %d, want 403", resp.StatusCode)
	}
}

// --- ListObjects ---

func TestListObjects(t *testing.T) {
	ts, primaryBE, _ := setupTestRouter(t)

	primaryBE.PutObject(context.Background(), "docs", "file1.txt",
		strings.NewReader("a"), 1, "text/plain")
	primaryBE.PutObject(context.Background(), "docs", "file2.txt",
		strings.NewReader("b"), 1, "text/plain")
	seedMeta(t, primaryBE, "docs", "file1.txt", "C1000")

	req, _ := http.NewRequest("GET", ts.URL+"/primary/docs/", nil)
	resp, _ := http.DefaultClient.Do(req)

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("LIST status = %d, want 200, body: %s", resp.StatusCode, body)
	}

	body, _ := io.ReadAll(resp.Body)
	var result listBucketResult
	if err := xml.Unmarshal(body, &result); err != nil {
		t.Fatalf("XML unmarshal: %v\nbody: %s", err, body)
	}

	if result.Name != "docs" {
		t.Errorf("bucket name = %s, want docs", result.Name)
	}

	for _, c := range result.Contents {
		if strings.HasSuffix(c.Key, ".meta") {
			t.Errorf("meta file %s should be filtered from listing", c.Key)
		}
	}
	if len(result.Contents) != 2 {
		t.Errorf("got %d objects, want 2", len(result.Contents))
	}
}

// --- Cross-account routing ---

func TestRouting_DifferentAccounts(t *testing.T) {
	ts, _, archiveBE := setupTestRouter(t)

	archiveBE.PutObject(context.Background(), "backups", "old.tar.gz",
		strings.NewReader("archive data"), 12, "application/gzip")
	seedMeta(t, archiveBE, "backups", "old.tar.gz", "C1000")

	req, _ := http.NewRequest("GET", ts.URL+"/backups/old.tar.gz", nil)
	req.Header.Set("Authorization", "Bearer "+makeToken([]string{"C1000"}))
	resp, _ := http.DefaultClient.Do(req)

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET archive: status = %d, want 200, body: %s", resp.StatusCode, body)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "archive data" {
		t.Errorf("body = %q", body)
	}
}

func TestRouting_AccountOpsEnforced(t *testing.T) {
	ts, _, _ := setupTestRouter(t)

	// Archive account only allows LIST, GET, HEAD — PUT should be blocked
	req, _ := http.NewRequest("PUT", ts.URL+"/backups/new.txt", strings.NewReader("data"))
	req.Header.Set("X-Securitytagid", "C1000")
	resp, _ := http.DefaultClient.Do(req)

	if resp.StatusCode != 405 {
		t.Errorf("PUT on read-only account: status = %d, want 405", resp.StatusCode)
	}
}

// --- Full roundtrip ---

func TestPutThenGet_Roundtrip(t *testing.T) {
	ts, _, _ := setupTestRouter(t)

	putReq, _ := http.NewRequest("PUT", ts.URL+"/primary/docs/roundtrip.txt", strings.NewReader("roundtrip data"))
	putReq.Header.Set("X-Securitytagid", "C1001")
	putReq.Header.Set("Content-Type", "text/plain")
	putResp, _ := http.DefaultClient.Do(putReq)
	if putResp.StatusCode != 200 {
		body, _ := io.ReadAll(putResp.Body)
		t.Fatalf("PUT: %d, %s", putResp.StatusCode, body)
	}

	getReq, _ := http.NewRequest("GET", ts.URL+"/primary/docs/roundtrip.txt", nil)
	getReq.Header.Set("Authorization", "Bearer "+makeToken([]string{"C1001"}))
	getResp, _ := http.DefaultClient.Do(getReq)
	if getResp.StatusCode != 200 {
		body, _ := io.ReadAll(getResp.Body)
		t.Fatalf("GET: %d, %s", getResp.StatusCode, body)
	}
	body, _ := io.ReadAll(getResp.Body)
	if string(body) != "roundtrip data" {
		t.Errorf("roundtrip data = %q", body)
	}

	getReq2, _ := http.NewRequest("GET", ts.URL+"/primary/docs/roundtrip.txt", nil)
	getReq2.Header.Set("Authorization", "Bearer "+makeToken([]string{"C1003"}))
	getResp2, _ := http.DefaultClient.Do(getReq2)
	if getResp2.StatusCode != 403 {
		t.Errorf("GET wrong role: %d, want 403", getResp2.StatusCode)
	}
}
