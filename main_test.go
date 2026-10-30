package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStaticRoutesDoNotRedirect(t *testing.T) {
	srv := NewServer(t.TempDir(), func(string) (time.Duration, error) { return time.Second, nil })
	handler := srv.Handler()
	tests := []struct {
		path, method, contentType, snippet string
		status                             int
	}{
		{"/", http.MethodGet, "text/html", "<title>Soundboard</title>", http.StatusOK},
		{"/", http.MethodHead, "text/html", "", http.StatusOK},
		{"/style.css", http.MethodGet, "text/css", "", http.StatusOK},
		{"/app.js", http.MethodGet, "javascript", "", http.StatusOK},
		{"/something-else", http.MethodGet, "", "", http.StatusNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d (Location: %q)", rec.Code, tc.status, rec.Header().Get("Location"))
			}
			if loc := rec.Header().Get("Location"); loc != "" {
				t.Fatalf("unexpected redirect Location: %s", loc)
			}
			if tc.contentType != "" && !strings.Contains(rec.Header().Get("Content-Type"), tc.contentType) {
				t.Fatalf("Content-Type = %q, want %q", rec.Header().Get("Content-Type"), tc.contentType)
			}
			if tc.snippet != "" && !strings.Contains(rec.Body.String(), tc.snippet) {
				t.Fatalf("response does not contain %q", tc.snippet)
			}
			if tc.method == http.MethodHead && rec.Body.Len() != 0 {
				t.Fatalf("HEAD returned a body of %d bytes", rec.Body.Len())
			}
		})
	}
}
