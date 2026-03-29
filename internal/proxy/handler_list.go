package proxy

import (
	"encoding/xml"
	"log"
	"net/http"
	"strings"
	"time"
)

type listBucketResult struct {
	XMLName     xml.Name    `xml:"ListBucketResult"`
	Xmlns       string      `xml:"xmlns,attr"`
	Name        string      `xml:"Name"`
	Prefix      string      `xml:"Prefix"`
	MaxKeys     int         `xml:"MaxKeys"`
	IsTruncated bool        `xml:"IsTruncated"`
	Contents    []s3Content `xml:"Contents"`
}

type s3Content struct {
	Key          string `xml:"Key"`
	LastModified string `xml:"LastModified"`
	Size         int64  `xml:"Size"`
	ETag         string `xml:"ETag"`
	StorageClass string `xml:"StorageClass"`
}

func handleListObjects(rc *RouteConfig, w http.ResponseWriter, r *http.Request) {
	bucket := extractBucket(rc, r)
	prefix := r.URL.Query().Get("prefix")

	objects, err := rc.Backend.ListObjects(r.Context(), bucket, prefix)
	if err != nil {
		log.Printf("ListObjects %s: backend error: %v", bucket, err)
		http.Error(w, "Bad gateway", http.StatusBadGateway)
		return
	}

	var contents []s3Content
	for _, obj := range objects {
		if strings.HasSuffix(obj.Key, ".meta") {
			continue
		}
		contents = append(contents, s3Content{
			Key:          obj.Key,
			LastModified: obj.LastModified.UTC().Format(time.RFC3339),
			Size:         obj.Size,
			ETag:         obj.ETag,
			StorageClass: "STANDARD",
		})
	}

	result := listBucketResult{
		Xmlns:       "http://s3.amazonaws.com/doc/2006-03-01/",
		Name:        bucket,
		Prefix:      prefix,
		MaxKeys:     1000,
		IsTruncated: false,
		Contents:    contents,
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(xml.Header))
	xml.NewEncoder(w).Encode(result)
}
