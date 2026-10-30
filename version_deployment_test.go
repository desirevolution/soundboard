package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestVersionAndRepositoryLinks(t *testing.T) {
	if v := strings.TrimSpace(buildVersion); v != "dev" && !regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`).MatchString(v) {
		t.Fatalf("unexpected build version %q: expected dev or a release tag", v)
	}
	if _, err := os.Stat("VERSION"); !os.IsNotExist(err) {
		t.Fatalf("source VERSION file must not be required: %v", err)
	}
	srv := NewServer(t.TempDir(), func(string) (time.Duration, error) { return time.Second, nil })
	for _, path := range []string{"/", "/admin/"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: %d", path, rec.Code)
		}
		html := rec.Body.String()
		for _, val := range []string{"https://github.com/desirevolution/soundboard", `target="_blank"`, `rel="noopener noreferrer"`, `class="repo-link"`} {
			if !strings.Contains(html, val) {
				t.Errorf("GET %s missing %q", path, val)
			}
		}
	}
	if _, err := os.Stat("compose.dev.yml"); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat("compose.yml"); err != nil {
		t.Error(err)
	}
}
