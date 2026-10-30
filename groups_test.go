package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAdminGroupManagementAndPersistence(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "ding.wav"), []byte("test"), 0644)
	srv := NewServer(dir, func(string) (time.Duration, error) { return 4 * time.Second, nil })
	if _, err := srv.lib.Scan(); err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()
	req := func(method, path, payload string, intWant int) {
		t.Helper()
		rec := requestAdmin(t, h, method, path, payload, "Alex", "application/json")
		if rec.Code != intWant {
			t.Fatalf("%s %s => %d %s (wanted %d)", method, path, rec.Code, rec.Body.String(), intWant)
		}
	}
	req("POST", "/api/admin/groups", `{"name":"Reaktionen","color":"#aabbcc"}`, 200)
	req("PUT", "/api/admin/sounds/ding.wav", `{"name":"Ding","group":"Reaktionen"}`, 200)
	req("PUT", "/api/admin/sounds/ding.wav", `{"name":"Ding","group":"unknown"}`, 400)
	req("PUT", "/api/admin/groups/Reaktionen", `{"name":"Reaction","color":"#123456"}`, 200)
	sound, _ := srv.lib.Get("ding.wav")
	if sound.Group != "Reaction" {
		t.Fatalf("rename did not remap sound: %+v", sound)
	}
	if gs := srv.lib.Groups(); len(gs) != 2 || gs[1].Color != "#123456" {
		t.Fatalf("groups: %+v", gs)
	}
	_ = srv.lib.WriteMetadata(srv.lib.Metadata())
	cold := NewLibrary(dir, func(string) (time.Duration, error) { return 4 * time.Second, nil })
	if _, err := cold.Scan(); err != nil {
		t.Fatal(err)
	}
	if gs := cold.Groups(); len(gs) != 2 || gs[1].Name != "Reaction" || gs[1].Color != "#123456" {
		t.Fatalf("group persistence %+v", gs)
	}
	req("DELETE", "/api/admin/groups/Reaction", "", 200)
	sound, _ = srv.lib.Get("ding.wav")
	if sound.Group != "default" {
		t.Fatalf("delete did not move sound to default: %+v", sound)
	}
	if gs := srv.lib.Groups(); len(gs) != 1 {
		t.Fatalf("deleted group reappeared: %+v", gs)
	}
	data, err := os.ReadFile(filepath.Join(dir, "groups.json"))
	if err != nil {
		t.Fatal(err)
	}
	var colors map[string]string
	if err := json.Unmarshal(data, &colors); err != nil {
		t.Fatal(err)
	}
	if _, ok := colors["Reaction"]; ok {
		t.Fatal("deleted group persisted")
	}
}
func TestLegacyMetadataGroupAppearsWithoutGroupsFile(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "sound.wav"), []byte("x"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "metadata.json"), []byte(`{"sound.wav":{"group":"legacy"}}`), 0644)
	lib := NewLibrary(dir, func(string) (time.Duration, error) { return time.Second, nil })
	if _, err := lib.Scan(); err != nil {
		t.Fatal(err)
	}
	groups := lib.Groups()
	if len(groups) != 2 || groups[1].Name != "legacy" {
		t.Fatalf("legacy migration %+v", groups)
	}
}
func TestQueueCountsByParticipant(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.wav"), []byte("x"), 0644)
	lib := NewLibrary(dir, func(string) (time.Duration, error) { return 20 * time.Second, nil })
	_, _ = lib.Scan()
	q := NewQueue(lib, func(QueueState, *Playing) {})
	defer q.Close()
	_, _ = q.Enqueue("a.wav", "Alex", "alex-id", "#ff0000")
	_, _ = q.Enqueue("a.wav", "Alex", "alex-id", "#ff0000")
	_, _ = q.Enqueue("a.wav", "Sam", "sam-id", "#00ff00")
	state := q.State()
	if state.Waiting != 2 || state.WaitingByID["alex-id"] != 1 || state.WaitingByID["sam-id"] != 1 {
		t.Fatalf("counts %+v", state)
	}
	if state.Playing == nil || state.Playing.ByID != "alex-id" {
		t.Fatal("current sound attribution wrong")
	}
}
