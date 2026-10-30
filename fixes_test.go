package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAdminSoundsAlwaysIncludesDefaultGroup(t *testing.T) {
	s := NewServer(t.TempDir(), func(string) (time.Duration, error) { return time.Second, nil })
	if _, err := s.lib.Scan(); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/admin/sounds", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		Groups []Group `json:"groups"`
		Sounds []Sound `json:"sounds"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Groups) != 1 || out.Groups[0].Name != "default" || out.Groups[0].Color != "#8492a9" {
		t.Fatalf("missing default group: %+v", out.Groups)
	}
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("DELETE", "/api/admin/groups/default", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("default group deletable: %d", w.Code)
	}
}

func TestGroupSuggestionRandomAndPersisted(t *testing.T) {
	dir := t.TempDir()
	s := NewServer(dir, func(string) (time.Duration, error) { return time.Second, nil })
	if _, err := s.lib.Scan(); err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/admin/groups/suggest-color", nil))
	if w.Code != 200 {
		t.Fatalf("suggest endpoint: %d %s", w.Code, w.Body.String())
	}
	var suggestion struct {
		Color string `json:"color"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &suggestion); err != nil {
		t.Fatal(err)
	}
	if !hexColor.MatchString(suggestion.Color) || strings.EqualFold(suggestion.Color, "#8492a9") {
		t.Fatalf("invalid or already used: %s", suggestion.Color)
	}
	// Server chooses a color even if client omits one.
	w = httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/admin/groups", strings.NewReader(`{"name":"Gruen"}`))
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("create failed: %d %s", w.Code, w.Body.String())
	}
	groups := s.lib.Groups()
	if len(groups) != 2 || groups[1].Name != "Gruen" || groups[1].Color == groups[0].Color {
		t.Fatalf("not distinct: %+v", groups)
	}
	cold := NewLibrary(dir, func(string) (time.Duration, error) { return time.Second, nil })
	if _, err := cold.Scan(); err != nil {
		t.Fatal(err)
	}
	if cold.Groups()[1].Color != groups[1].Color {
		t.Fatalf("color not persisted %+v", cold.Groups())
	}
}

func TestMissingLegacyGroupGetsPersistedRandomColor(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sound.wav"), testWAV(), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "metadata.json"), []byte(`{"sound.wav":{"group":"my group"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	l := NewLibrary(dir, func(string) (time.Duration, error) { return time.Second, nil })
	if _, err := l.Scan(); err != nil {
		t.Fatal(err)
	}
	groups := l.Groups()
	if len(groups) != 2 || groups[1].Name != "my group" || groups[1].Color == groups[0].Color {
		t.Fatalf("bad groups: %+v", groups)
	}
	if _, err := os.Stat(filepath.Join(dir, "groups.json")); err != nil {
		t.Fatalf("random color was not persisted: %v", err)
	}
	cold := NewLibrary(dir, func(string) (time.Duration, error) { return time.Second, nil })
	if _, err := cold.Scan(); err != nil {
		t.Fatal(err)
	}
	if cold.Groups()[1].Color != groups[1].Color {
		t.Fatalf("random color changed: %+v", cold.Groups())
	}
}
