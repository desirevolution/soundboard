package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A previous release accidentally replaced the entire admin.css file with
// HTML, while retaining a successful HTTP status and CSS Content-Type.
func TestAdminCSSContainsStylesNotHTML(t *testing.T) {
	s := NewServer(t.TempDir(), func(string) (time.Duration, error) { return time.Second, nil })
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/admin.css", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("admin css: HTTP %d, type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	css := strings.TrimSpace(rec.Body.String())
	if strings.Contains(strings.ToLower(css), "<!doctype") || strings.Contains(strings.ToLower(css), "<html") || !strings.Contains(css, ".admin-shell") || !strings.Contains(css, ".admin-tabs") {
		t.Fatalf("admin css is missing layout declarations or contains HTML (prefix %q)", css[:min(120, len(css))])
	}
}

func TestAdminUsesSharedAudioWithoutDebugPlayer(t *testing.T) {
	s := NewServer(t.TempDir(), func(string) (time.Duration, error) { return time.Second, nil })
	h := s.Handler()
	check := func(path string) string {
		t.Helper()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", path, rec.Code)
		}
		return rec.Body.String()
	}
	admin := check("/admin/")
	index := check("/")
	for _, page := range []string{admin, index} {
		if !strings.Contains(page, `src="/audio.js"`) {
			t.Error("shared player script missing")
		}
	}
	if strings.Contains(admin, `id="preview-audio"`) || strings.Contains(admin, `id="preview-compat"`) || strings.Contains(admin, `id="preview-panel"`) {
		t.Fatal("admin still displays diagnostic player or mp3 mode")
	}
	audioJS := check("/audio.js")
	if !strings.Contains(audioJS, "SoundboardAudio") || !strings.Contains(audioJS, "/api/audio/") || strings.Contains(audioJS, "/api/admin/preview/") {
		t.Fatal("shared player does not use original audio endpoint")
	}
	adminJS := check("/admin/admin.js")
	appJS := check("/app.js")
	for _, js := range []string{adminJS, appJS} {
		if !strings.Contains(js, "SoundboardAudio.play(") || !strings.Contains(js, "SoundboardAudio.stop(") {
			t.Error("player not shared")
		}
	}
	if strings.Contains(adminJS, "switchToMP3") || strings.Contains(adminJS, "preview-compat") {
		t.Fatal("old preview mode remains")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/admin/preview/sample.wav", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("obsolete conversion endpoint: %d", rec.Code)
	}
}

func TestNativeAudioEndpointCanServePreviewSource(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.wav"), testWAV(), 0644); err != nil {
		t.Fatal(err)
	}
	s := NewServer(dir, func(string) (time.Duration, error) { return time.Second, nil })
	if _, err := s.lib.Scan(); err != nil {
		t.Fatal(err)
	}
	sound, ok := s.lib.Get("sample.wav")
	if !ok {
		t.Fatal("sound missing")
	}
	path := "/api/audio/sample.wav?v=" + sound.Version
	for _, rangeHeader := range []string{"", "bytes=0-99"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if rangeHeader != "" {
			req.Header.Set("Range", rangeHeader)
		}
		s.Handler().ServeHTTP(rec, req)
		status := http.StatusOK
		if rangeHeader != "" {
			status = http.StatusPartialContent
		}
		if rec.Code != status || rec.Header().Get("Content-Type") != "audio/wav" {
			t.Fatalf("range %q: HTTP %d type %s", rangeHeader, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
}
