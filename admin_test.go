package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func requestAdmin(t *testing.T, handler http.Handler, method, path, body, actor string, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if actor != "" {
		req.Header.Set("X-Soundboard-Nickname", actor)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
func uploadTestSound(t *testing.T, h http.Handler, name, policy, actor string) *httptest.ResponseRecorder {
	t.Helper()
	var data bytes.Buffer
	mw := multipart.NewWriter(&data)
	_ = mw.WriteField("onConflict", policy)
	_ = mw.WriteField("group", "effects")
	_ = mw.WriteField("name", "Custom name")
	part, err := mw.CreateFormFile("files", name)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("fake audio data"))
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/admin/upload", &data)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Soundboard-Nickname", actor)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
func TestAdminRoutesAndLifecycle(t *testing.T) {
	dir := t.TempDir()
	srv := NewServer(dir, func(string) (time.Duration, error) { return 2 * time.Second, nil })
	h := srv.Handler()
	for _, path := range []string{"/admin/", "/admin/admin.css", "/admin/admin.js"} {
		rec := requestAdmin(t, h, "GET", path, "", "", "")
		if rec.Code != 200 || rec.Header().Get("Location") != "" {
			t.Fatalf("%s: status %d, redirect %s", path, rec.Code, rec.Header().Get("Location"))
		}
	}
	for _, group := range []string{"effects", "reactions"} {
		res := requestAdmin(t, h, "POST", "/api/admin/groups", `{"name":"`+group+`"}`, "Alex", "application/json")
		if res.Code != 200 {
			t.Fatalf("create group %s: %d %s", group, res.Code, res.Body.String())
		}
	}
	added := uploadTestSound(t, h, "wow.wav", "", "Alex")
	if added.Code != 200 {
		t.Fatalf("upload: %d %s", added.Code, added.Body.String())
	}
	snd, ok := srv.lib.Get("wow.wav")
	if !ok || snd.Name != "Custom name" || snd.Group != "effects" || snd.UploadedBy != "Alex" || snd.UploadedAt == "" {
		t.Fatalf("attribution: %+v", snd)
	}
	conflict := uploadTestSound(t, h, "wow.wav", "", "Sam")
	if conflict.Code != 409 {
		t.Fatalf("missing conflict: %d %s", conflict.Code, conflict.Body.String())
	}
	replaced := uploadTestSound(t, h, "wow.wav", "replace", "Sam")
	if replaced.Code != 200 {
		t.Fatalf("replace: %d %s", replaced.Code, replaced.Body.String())
	}
	snd, _ = srv.lib.Get("wow.wav")
	if snd.Name != "Custom name" || snd.Group != "effects" || snd.UploadedBy != "Alex" || snd.UpdatedBy != "Sam" {
		t.Fatalf("replace metadata: %+v", snd)
	}
	renamed := uploadTestSound(t, h, "wow.wav", "rename", "Sam")
	if renamed.Code != 200 {
		t.Fatalf("rename: %d %s", renamed.Code, renamed.Body.String())
	}
	newSound, ok := srv.lib.Get("wow-2.wav")
	if !ok || newSound.UploadedBy != "Sam" {
		t.Fatalf("rename metadata: %+v", newSound)
	}
	edit := requestAdmin(t, h, "PUT", "/api/admin/sounds/wow.wav", `{"name":"Aplaus","group":"reactions"}`, "Kim", "application/json")
	if edit.Code != 200 {
		t.Fatalf("edit: %d %s", edit.Code, edit.Body.String())
	}
	snd, _ = srv.lib.Get("wow.wav")
	if snd.Name != "Aplaus" || snd.Group != "reactions" || snd.UploadedBy != "Alex" {
		t.Fatalf("edit mismatch %+v", snd)
	}
	del := requestAdmin(t, h, "DELETE", "/api/admin/sounds/wow.wav", "", "Kim", "")
	if del.Code != 200 {
		t.Fatalf("delete: %d %s", del.Code, del.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "wow.wav")); !os.IsNotExist(err) {
		t.Fatalf("file still exists: %v", err)
	}
	if _, ok := srv.lib.Get("wow.wav"); ok {
		t.Fatal("deleted sound still indexed")
	}
	meta, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if json.Unmarshal(meta, &m) != nil {
		t.Fatal("invalid metadata JSON")
	}
	if _, ok := m["wow.wav"]; ok {
		t.Fatal("deleted sound metadata remains")
	}
}
func TestExplicitResyncAndInvalidMetadataPreservesLastGood(t *testing.T) {
	dir := t.TempDir()
	srv := NewServer(dir, func(string) (time.Duration, error) { return time.Second, nil })
	h := srv.Handler()
	path := filepath.Join(dir, "manual.wav")
	if err := os.WriteFile(path, []byte("fake"), 0644); err != nil {
		t.Fatal(err)
	}
	if sounds, _ := srv.lib.Snapshot(); len(sounds) != 0 {
		t.Fatal("should not index files until explicit resync")
	}
	res := requestAdmin(t, h, "POST", "/api/admin/resync", "", "", "")
	if res.Code != 200 {
		t.Fatalf("resync: %d %s", res.Code, res.Body.String())
	}
	if _, ok := srv.lib.Get("manual.wav"); !ok {
		t.Fatal("resync did not index sound")
	}
	_ = os.WriteFile(filepath.Join(dir, "metadata.json"), []byte(`{"manual.wav":{"name":"Greeting","group":"greetings"}}`), 0644)
	_ = requestAdmin(t, h, "POST", "/api/admin/resync", "", "", "")
	_ = os.WriteFile(filepath.Join(dir, "metadata.json"), []byte(`{bad json`), 0644)
	bad := requestAdmin(t, h, "POST", "/api/admin/resync", "", "", "")
	if bad.Code != 422 {
		t.Fatalf("bad metadata: %d %s", bad.Code, bad.Body.String())
	}
	snd, _ := srv.lib.Get("manual.wav")
	if snd.Name != "Greeting" || snd.Group != "greetings" {
		t.Fatalf("lost valid metadata: %+v", snd)
	}
}
func TestURLImportRejectsLocalNetwork(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "audio") }))
	defer server.Close()
	srv := NewServer(t.TempDir(), func(string) (time.Duration, error) { return time.Second, nil })
	payload := `{"url":"` + server.URL + `/noise.wav"}`
	res := requestAdmin(t, srv.Handler(), "POST", "/api/admin/import-url", payload, "Alex", "application/json")
	if res.Code != 400 || !strings.Contains(res.Body.String(), "non-public") {
		t.Fatalf("SSRF protection: %d %s", res.Code, res.Body.String())
	}
}
func TestPresenceIdentityUsesBrowserID(t *testing.T) {
	hub := NewHub()
	a := &WSClient{}
	b := &WSClient{}
	c := &WSClient{}
	hub.clients[a] = participant{ID: "browser-1111", Name: "Alex", Color: colorForParticipant("browser-1111")}
	hub.clients[b] = participant{ID: "browser-1111", Name: "Alex", Color: colorForParticipant("browser-1111")}
	hub.clients[c] = participant{ID: "browser-2222", Name: "Alex", Color: colorForParticipant("browser-2222")}
	members := hub.Presence()
	if len(members) != 2 {
		t.Fatalf("expected two identities, got %+v", members)
	}
	for _, p := range members {
		if p.ID == "browser-1111" && p.Connections != 2 {
			t.Fatalf("multiple tabs %+v", p)
		}
	}
}
