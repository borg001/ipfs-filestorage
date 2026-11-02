package handler

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/borg001/ipfs-filestorage/internal/config"
)

// Exercise every entry point, including multipart fields rather than just files:
// a file-size check alone does not bound the request or its parser scratch space.
func TestUploadRequestBodyLimit(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("padding", strings.Repeat("x", 1024)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"single", "multiple", "video"} {
		for _, transfer := range []string{"length", "unknown-length", "understated-length"} {
			for _, overLimit := range []bool{false, true} {
				name := route + "/" + transfer + "/boundary"
				if overLimit {
					name = route + "/" + transfer + "/too-large"
				}
				t.Run(name, func(t *testing.T) {
					limit := int64(body.Len())
					if overLimit {
						limit--
					}
					h := &Handler{cfg: &config.Config{Upload: config.UploadConfig{MaxRequestSize: limit, MaxFileSize: 1024}}}
					req := httptest.NewRequest(http.MethodPost, "/upload", bytes.NewReader(body.Bytes()))
					req.Header.Set("Content-Type", writer.FormDataContentType())
					if transfer == "unknown-length" {
						req.ContentLength = -1
						req.TransferEncoding = []string{"chunked"}
					} else if transfer == "understated-length" {
						req.ContentLength = 1
					}
					response := httptest.NewRecorder()
					switch route {
					case "single":
						h.HandleUpload(response, req)
					case "multiple":
						h.HandleUploadMultiple(response, req)
					case "video":
						h.HandleUploadVideo(response, req)
					}
					var result uploadErrorResponse
					if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
						t.Fatal(err)
					}
					if overLimit {
						if response.Code != http.StatusRequestEntityTooLarge || result.Code != "upload_request_too_large" || result.Details["max_request_bytes"] != float64(limit) {
							t.Fatalf("status %d, response %+v", response.Code, result)
						}
					} else if response.Code != http.StatusBadRequest || result.Code != "upload_missing_file" {
						t.Fatalf("body at limit should reach file validation: status %d, response %+v", response.Code, result)
					}
				})
			}
		}
	}
}

func TestConfigPublishesTotalUploadLimit(t *testing.T) {
	h := &Handler{cfg: &config.Config{Upload: config.UploadConfig{MaxRequestSize: 100000000}}}
	response := httptest.NewRecorder()
	h.HandleConfig(response, httptest.NewRequest(http.MethodGet, "/config", nil))
	var result publicConfigResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Upload.MaxRequestBytes != 100000000 {
		t.Fatalf("max_request_bytes = %d", result.Upload.MaxRequestBytes)
	}
}

func TestUploadRequestLimitIncludesMultipartEpilogue(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("padding", "value"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	// Exceed the parser's read-ahead buffer so only reading the remaining body
	// can detect the overflow after a complete, otherwise valid multipart form.
	body.WriteString(strings.Repeat("x", 16384))
	h := &Handler{cfg: &config.Config{Upload: config.UploadConfig{MaxRequestSize: 8192}}}
	req := httptest.NewRequest(http.MethodPost, "/upload", bytes.NewReader(body.Bytes()))
	req.ContentLength = -1
	req.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	h.HandleUpload(response, req)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, body %s", response.Code, response.Body.String())
	}
}
