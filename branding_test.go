package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The custom monogram must be embedded and served alongside the UI, including
// both the player and the admin page. No external image host is required.
func TestBrandAssets(t *testing.T) {
	s := NewServer(t.TempDir(), func(string) (time.Duration, error) { return time.Second, nil })
	h := s.Handler()
	for _, path := range []string{"/favicon.ico", "/icon-32.png", "/icon-192.png", "/icon-512.png", "/apple-touch-icon.png"} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
			if rec.Code != http.StatusOK {
				t.Errorf("%s %s: HTTP %d", method, path, rec.Code)
				continue
			}
			if !strings.HasPrefix(rec.Header().Get("Content-Type"), "image/") {
				t.Errorf("%s %s: unexpected type %q", method, path, rec.Header().Get("Content-Type"))
			}
			if method == http.MethodGet && len(rec.Body.Bytes()) < 100 {
				t.Errorf("%s: image empty", path)
			}
			if method == http.MethodHead && rec.Body.Len() != 0 {
				t.Errorf("%s: HEAD unexpectedly sent a body", path)
			}
		}
	}
	for _, path := range []string{"/", "/admin/"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		page := rec.Body.String()
		if rec.Code != http.StatusOK || !strings.Contains(page, `src="/icon-192.png"`) ||
			!strings.Contains(page, `href="/favicon.ico"`) ||
			!strings.Contains(page, `href="https://github.com/desirevolution/soundboard"`) {
			t.Errorf("%s: missing brand/favicon/repository link", path)
		}
		if strings.Contains(page, `<svg viewBox="0 0 24 24"`) {
			t.Errorf("%s: old GitHub logo still present", path)
		}
	}
}

func TestTactileButtonsAreUnprinted(t *testing.T) {
	s := NewServer(t.TempDir(), func(string) (time.Duration, error) { return time.Second, nil })
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/style.css", nil))
	css := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(css, `.sound-trigger:is(:active, .is-pressed) .sound-cap`) ||
		!strings.Contains(css, `transform: translateY(6px)`) || strings.Contains(css, `scale(.985)`) ||
		strings.Contains(css, `.sound-trigger.is-instant .sound-cap::after{content:"⚡"`) {
		t.Fatal("pressed button has lost tactile effect or contains old extra symbol/scale")
	}
}
