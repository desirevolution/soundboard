package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInstantReplacesGloballyWithoutChangingQueue(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"first.wav", "second.wav"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("stub"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	lib := NewLibrary(dir, func(string) (time.Duration, error) { return 150 * time.Millisecond, nil })
	if _, err := lib.Scan(); err != nil {
		t.Fatal(err)
	}
	changes := make(chan InstantState, 16)
	inst := NewInstantPlayer(lib, func(state InstantState, _ bool) { changes <- state })
	defer inst.Close()
	q := NewQueue(lib, func(QueueState, *Playing) {})
	defer q.Close()
	if _, err := q.Enqueue("first.wav", "Alex", "client-abc", "#000000"); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Enqueue("second.wav", "Alex", "client-abc", "#000000"); err != nil {
		t.Fatal(err)
	}
	before := q.State()
	first, err := inst.Trigger("first.wav", "Alex", "client-abc", "#00aa00")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != 1 || first.By != "Alex" || first.ByID != "client-abc" {
		t.Fatalf("first trigger %+v", first)
	}
	second, err := inst.Trigger("second.wav", "Sam", "client-def", "#bb0000")
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != 2 || second.By != "Sam" {
		t.Fatalf("second trigger %+v", second)
	}
	if got := inst.State(); got.Playing == nil || got.Playing.Filename != "second.wav" || got.Sequence != 2 {
		t.Fatalf("replacement %+v", got)
	}
	if got := q.State(); got.Waiting != before.Waiting || got.Playing.Filename != before.Playing.Filename {
		t.Fatalf("instant modified FIFO state: %+v", got)
	}
	// Old first sound's timer must not stop the newer trigger.
	time.Sleep(90 * time.Millisecond)
	if inst.State().Playing == nil {
		t.Fatal("newer instant was stopped prematurely")
	}
	time.Sleep(130 * time.Millisecond)
	if got := inst.State(); got.Playing != nil || got.Sequence != 3 {
		t.Fatalf("instant should finish and advance sequence: %+v", got)
	}
	select {
	case <-changes:
	default:
		t.Fatal("no change broadcast")
	}
}

func TestInstantSameSoundRestartsAndMissingSoundRejected(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.wav"), []byte("stub"), 0644); err != nil {
		t.Fatal(err)
	}
	lib := NewLibrary(dir, func(string) (time.Duration, error) { return time.Second, nil })
	if _, err := lib.Scan(); err != nil {
		t.Fatal(err)
	}
	inst := NewInstantPlayer(lib, func(InstantState, bool) {})
	defer inst.Close()
	a, _ := inst.Trigger("x.wav", "A", "a-client-1", "red")
	time.Sleep(10 * time.Millisecond)
	b, _ := inst.Trigger("x.wav", "A", "a-client-1", "red")
	if b.ID <= a.ID || b.StartedAt < a.StartedAt || b.EndsAt <= a.EndsAt {
		t.Fatalf("not restarted: %+v %+v", a, b)
	}
	if _, err := inst.Trigger("absent.wav", "A", "a-client-1", "red"); err == nil {
		t.Fatal("accepted absent sound")
	}
	if inst.State().Playing.ID != b.ID {
		t.Fatal("rejected sound interrupted active instant")
	}
}

func TestPresenceAdminAcrossTabs(t *testing.T) {
	hub := NewHub()
	a, b := &WSClient{}, &WSClient{}
	hub.JoinWithMode(a, false)
	hub.JoinWithMode(b, true)
	hub.mu.Lock()
	hub.clients[a] = participant{ID: "browser-1111", Name: "Alex", Color: "#aabbcc"}
	hub.clients[b] = participant{ID: "browser-1111", Name: "Alex", Color: "#aabbcc", Admin: true}
	hub.mu.Unlock()
	presence := hub.Presence()
	if len(presence) != 1 || !presence[0].Admin || presence[0].Connections != 2 {
		t.Fatalf("admin across tabs: %+v", presence)
	}
	hub.mu.Lock()
	delete(hub.clients, b)
	hub.mu.Unlock() // don't close nil test conns
	presence = hub.Presence()
	if len(presence) != 1 || presence[0].Admin {
		t.Fatalf("admin mode persisted after last admin tab: %+v", presence)
	}
}
