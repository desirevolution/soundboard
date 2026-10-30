package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Sound struct {
	Filename   string `json:"filename"`
	Name       string `json:"name"`
	Group      string `json:"group"`
	Version    string `json:"version"`
	DurationMS int64  `json:"durationMs"`
	UploadedBy string `json:"uploadedBy,omitempty"`
	UploadedAt string `json:"uploadedAt,omitempty"`
	UpdatedBy  string `json:"updatedBy,omitempty"`
	UpdatedAt  string `json:"updatedAt,omitempty"`
}

type soundMeta struct {
	Name       string `json:"name,omitempty"`
	Group      string `json:"group,omitempty"`
	UploadedBy string `json:"uploadedBy,omitempty"`
	UploadedAt string `json:"uploadedAt,omitempty"`
	UpdatedBy  string `json:"updatedBy,omitempty"`
	UpdatedAt  string `json:"updatedAt,omitempty"`
}
type soundRecord struct {
	Sound
	signature string
}
type Library struct {
	mu       sync.RWMutex
	dir      string
	entries  map[string]soundRecord
	metadata map[string]soundMeta // last successfully parsed metadata
	groups   map[string]string    // persisted colors, last successfully parsed
	probe    func(string) (time.Duration, error)
	revision uint64
}

func NewLibrary(dir string, probe func(string) (time.Duration, error)) *Library {
	return &Library{dir: dir, entries: map[string]soundRecord{}, metadata: map[string]soundMeta{}, groups: map[string]string{}, probe: probe}
}
func validExtension(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mp3", ".wav", ".ogg", ".m4a", ".opus":
		return true
	default:
		return false
	}
}
func safeName(s string) bool {
	return s != "" && s != "." && s != ".." && filepath.Base(s) == s && !strings.ContainsAny(s, "\\/\x00")
}
func (l *Library) Snapshot() ([]Sound, uint64) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]Sound, 0, len(l.entries))
	for _, record := range l.entries {
		out = append(out, record.Sound)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name)
		if a == b {
			return out[i].Filename < out[j].Filename
		}
		return a < b
	})
	return out, l.revision
}
func (l *Library) Get(filename string) (Sound, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	e, ok := l.entries[filename]
	return e.Sound, ok
}
func (l *Library) Has(filename string) bool { _, ok := l.Get(filename); return ok }

// Scan is explicit (startup, administrator action or resync); there is no polling.
// An invalid metadata file never replaces the last successfully parsed values.
// Existing valid audio versions remain visible when a replacement cannot be probed.
func (l *Library) Scan() (bool, error) {
	files, err := os.ReadDir(l.dir)
	if err != nil {
		return false, err
	}
	l.mu.RLock()
	old := make(map[string]soundRecord, len(l.entries))
	for k, v := range l.entries {
		old[k] = v
	}
	meta := make(map[string]soundMeta, len(l.metadata))
	for k, v := range l.metadata {
		meta[k] = v
	}
	oldGroups := make(map[string]string, len(l.groups))
	for k, v := range l.groups {
		oldGroups[k] = v
	}
	l.mu.RUnlock()
	raw, err := os.ReadFile(filepath.Join(l.dir, "metadata.json"))
	if err == nil {
		updated := map[string]soundMeta{}
		if err := json.Unmarshal(raw, &updated); err != nil || updated == nil {
			if err == nil {
				err = errors.New("metadata must be a JSON object")
			}
			return false, fmt.Errorf("invalid metadata.json: %w", err)
		}
		meta = updated
	} else if errors.Is(err, os.ErrNotExist) {
		meta = map[string]soundMeta{}
	} else {
		return false, fmt.Errorf("read metadata.json: %w", err)
	}

	newGroups, groupErr := loadGroups(l.dir)
	if groupErr != nil {
		return false, groupErr
	}
	newGroups["default"] = "#8492a9"
	next := map[string]soundRecord{}
	var scanErrors []error
	for _, entry := range files {
		name := entry.Name()
		if !safeName(name) || !validExtension(name) || entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			continue
		}
		sig := fmt.Sprintf("%d-%d", info.Size(), info.ModTime().UnixNano())
		oldEntry, exists := old[name]
		var record soundRecord
		if exists && oldEntry.signature == sig {
			record = oldEntry
		} else {
			duration, err := l.probe(filepath.Join(l.dir, name))
			if err != nil || duration <= 0 {
				scanErrors = append(scanErrors, fmt.Errorf("%s: audio not readable: %v", name, err))
				if exists {
					record = oldEntry
				} else {
					continue
				}
			} else {
				record = soundRecord{Sound: Sound{Filename: name, DurationMS: duration.Milliseconds(), Version: sig}, signature: sig}
			}
		}
		m := meta[name]
		record.Name = strings.TrimSpace(m.Name)
		if record.Name == "" {
			record.Name = strings.TrimSuffix(name, filepath.Ext(name))
		}
		record.Group = strings.TrimSpace(m.Group)
		if record.Group == "" {
			record.Group = "default"
		}
		record.UploadedBy, record.UploadedAt = m.UploadedBy, m.UploadedAt
		record.UpdatedBy, record.UpdatedAt = m.UpdatedBy, m.UpdatedAt
		next[name] = record
	}
	// Existing installations may use groups only in metadata.json. Assign a
	// distinct random color once and persist it, rather than deriving colors
	// from group names (which may be renamed later).
	missing := []string{}
	seenMissing := map[string]bool{}
	for _, v := range next {
		if _, ok := newGroups[v.Group]; !ok && !seenMissing[v.Group] {
			missing = append(missing, v.Group)
			seenMissing[v.Group] = true
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		usedGroups := make([]Group, 0, len(newGroups)+len(missing))
		for name, color := range newGroups {
			usedGroups = append(usedGroups, Group{Name: name, Color: color})
		}
		for _, name := range missing {
			color := chooseGroupColor(usedGroups)
			newGroups[name] = color
			usedGroups = append(usedGroups, Group{Name: name, Color: color})
		}
		if err := l.WriteGroups(newGroups); err != nil {
			return false, fmt.Errorf("persist new group colors: %w", err)
		}
	}
	changed := len(next) != len(old) || !reflect.DeepEqual(oldGroups, newGroups)
	if !changed {
		for k, v := range next {
			if old[k] != v {
				changed = true
				break
			}
		}
	}
	l.mu.Lock()
	l.entries, l.metadata, l.groups = next, meta, newGroups
	if changed {
		l.revision++
	}
	l.mu.Unlock()
	return changed, errors.Join(scanErrors...)
}
func (l *Library) Metadata() map[string]soundMeta {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make(map[string]soundMeta, len(l.metadata))
	for k, v := range l.metadata {
		out[k] = v
	}
	return out
}

// WriteMetadata is serialized externally by the admin operations mutex.
func (l *Library) WriteMetadata(meta map[string]soundMeta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(l.dir, ".meta-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0644); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(l.dir, "metadata.json"))
}
func probeFF(path string) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", path).Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe: %w", err)
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil || seconds <= 0 || seconds > 36000 {
		return 0, fmt.Errorf("invalid duration %q", strings.TrimSpace(string(out)))
	}
	return time.Duration(seconds * float64(time.Second)), nil
}
