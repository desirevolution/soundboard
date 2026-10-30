package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLibraryFallbackAndMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.wav")
	if err := os.WriteFile(path, []byte("fake"), 0644); err != nil {
		t.Fatal(err)
	}
	lib := NewLibrary(dir, func(string) (time.Duration, error) { return time.Second, nil })
	if _, err := lib.Scan(); err != nil {
		t.Fatal(err)
	}
	sounds, _ := lib.Snapshot()
	if len(sounds) != 1 {
		t.Fatal("explicit scan must index valid files immediately")
	}
	if _, err := lib.Scan(); err != nil {
		t.Fatal(err)
	}
	sounds, _ = lib.Snapshot()
	if len(sounds) != 1 || sounds[0].Name != "hello" || sounds[0].Group != "default" {
		t.Fatalf("fallback: %+v", sounds)
	}
	if err := os.WriteFile(filepath.Join(dir, "metadata.json"), []byte(`{"hello.wav":{"name":"Guten Tag","group":"gruesse"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	changed, err := lib.Scan()
	if err != nil || !changed {
		t.Fatalf("metadata update: %v, %v", changed, err)
	}
	sounds, _ = lib.Snapshot()
	if len(sounds) != 1 || sounds[0].Name != "Guten Tag" || sounds[0].Group != "gruesse" {
		t.Fatalf("metadata: %+v", sounds)
	}
}

func TestQueueAttributionAndLimit(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "one.wav"), []byte("test"), 0644)
	lib := NewLibrary(dir, func(string) (time.Duration, error) { return 10 * time.Second, nil })
	_, _ = lib.Scan()
	_, _ = lib.Scan()
	q := NewQueue(lib, func(QueueState, *Playing) {})
	defer q.Close()
	pos, err := q.Enqueue("one.wav", "Alex")
	if err != nil || pos != 0 {
		t.Fatalf("initial: %d, %v", pos, err)
	}
	state := q.State()
	if state.Playing == nil || state.Playing.By != "Alex" {
		t.Fatalf("attribution: %+v", state)
	}
	for i := 0; i < maxWaiting; i++ {
		if _, err := q.Enqueue("one.wav", "Mina"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := q.Enqueue("one.wav", "Mina"); err == nil {
		t.Fatal("must enforce global waiting limit")
	}
	if state := q.State(); state.Waiting != maxWaiting {
		t.Fatalf("waiting count: %+v", state)
	}
}

func TestNicknameValidation(t *testing.T) {
	for _, v := range []string{"", "   ", "a\n", "a\x00", "abcdefghijklmnopqrstuvwxyz123456789"} {
		if cleanNickname(v) != "" {
			t.Fatalf("accepted invalid %q", v)
		}
	}
	if cleanNickname("  Alex  ") != "Alex" {
		t.Fatal("nickname trimming")
	}
}
