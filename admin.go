package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxFileSize = 20 << 20
const maxImportRequest = 105 << 20

func apiError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": message})
}
func (s *Server) publishLibrary() (int, error) {
	changed, err := s.lib.Scan()
	if changed {
		s.queue.Prune()
		sounds, rev := s.lib.Snapshot()
		s.hub.Broadcast(marshal(map[string]any{"type": "library", "groups": s.lib.Groups(), "sounds": sounds, "revision": rev}))
	}
	sounds, _ := s.lib.Snapshot()
	return len(sounds), err
}
func adminNickname(r *http.Request) string {
	n := cleanNickname(r.Header.Get("X-Soundboard-Nickname"))
	if n == "" {
		return "Unbekannt"
	}
	return n
}
func readSmallJSON(r *http.Request, dest any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 8193))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.New("unexpected additional JSON data")
	}
	return nil
}
func uniqueName(name string, occupied map[string]bool) string {
	stem, ext := strings.TrimSuffix(name, filepath.Ext(name)), filepath.Ext(name)
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d%s", stem, i, ext)
		if !occupied[candidate] {
			return candidate
		}
	}
}
func validateIncomingFilename(name string) error {
	if !safeName(name) || strings.HasPrefix(name, ".") || !validExtension(name) || len(name) > 180 {
		return errors.New("unsupported or invalid audio filename")
	}
	return nil
}
func (s *Server) stageFile(filename string, input io.Reader) (stagedAudio, error) {
	if err := validateIncomingFilename(filename); err != nil {
		return stagedAudio{}, err
	}
	f, err := os.CreateTemp(s.lib.dir, ".upload-*")
	if err != nil {
		return stagedAudio{}, err
	}
	item := stagedAudio{name: filename, path: f.Name()}
	n, err := io.Copy(f, io.LimitReader(input, maxFileSize+1))
	if err != nil {
		f.Close()
		os.Remove(item.path)
		return stagedAudio{}, err
	}
	if err := f.Close(); err != nil {
		os.Remove(item.path)
		return stagedAudio{}, err
	}
	if n == 0 || n > maxFileSize {
		os.Remove(item.path)
		return stagedAudio{}, fmt.Errorf("file must be 1 byte to %d MB", maxFileSize>>20)
	}
	duration, err := s.lib.probe(item.path)
	if err != nil || duration <= 0 || duration > 30*time.Second {
		os.Remove(item.path)
		return stagedAudio{}, fmt.Errorf("invalid or too long audio (maximum 30 seconds): %v", err)
	}
	return item, nil
}

type stagedAudio struct{ name, path string }

func cleanupStaged(items []stagedAudio) {
	for _, x := range items {
		_ = os.Remove(x.path)
	}
}
func (s *Server) commitImported(items []stagedAudio, group, displayName, policy, actor string) ([]string, error) {
	// Caller holds s.adminMu. Stage and validate every file before modifying the library.
	occupied := map[string]bool{}
	existing, _ := s.lib.Snapshot()
	for _, x := range existing {
		occupied[x.Filename] = true
	}
	entries, err := os.ReadDir(s.lib.dir)
	if err != nil {
		return nil, err
	}
	for _, x := range entries {
		occupied[x.Name()] = true
	}
	conflicts := []string{}
	seen := map[string]bool{}
	for _, x := range items {
		if occupied[x.name] || seen[x.name] {
			conflicts = append(conflicts, x.name)
		}
		seen[x.name] = true
	}
	if len(conflicts) > 0 && policy != "replace" && policy != "rename" {
		return conflicts, nil
	}
	if group == "" {
		group = "default"
	}
	if !s.groupExists(group) {
		return nil, fmt.Errorf("group does not exist: %s", group)
	}
	meta := s.lib.Metadata()
	now := time.Now().UTC().Format(time.RFC3339)
	modified := false
	writtenThisBatch := map[string]bool{}
	for _, x := range items {
		name := x.name
		if occupied[name] && policy == "rename" {
			name = uniqueName(name, occupied)
		}
		// Duplicate names within an import are renamed even if replace is chosen.
		if writtenThisBatch[name] && policy == "replace" {
			name = uniqueName(name, occupied)
		}
		original := occupied[name]
		if original {
			m := meta[name]
			if m.UploadedBy == "" {
				m.UploadedBy = actor
				m.UploadedAt = now
			}
			m.UpdatedBy = actor
			m.UpdatedAt = now
			meta[name] = m
		} else {
			meta[name] = soundMeta{Name: strings.TrimSpace(displayName), Group: strings.TrimSpace(group), UploadedBy: actor, UploadedAt: now}
		}
		if err := os.Chmod(x.path, 0644); err != nil {
			return nil, err
		}
		if err := os.Rename(x.path, filepath.Join(s.lib.dir, name)); err != nil {
			return nil, err
		}
		occupied[name] = true
		writtenThisBatch[name] = true
		modified = true
	}
	if modified {
		if err := s.lib.WriteMetadata(meta); err != nil {
			return nil, fmt.Errorf("files stored but metadata update failed; use resync: %w", err)
		}
	}
	_, err = s.publishLibrary()
	return nil, err
}
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		apiError(w, 405, "method not allowed")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxImportRequest)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		apiError(w, 400, "invalid or oversized multipart upload")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	files := r.MultipartForm.File["files"]
	if len(files) == 0 || len(files) > 20 {
		apiError(w, 400, "select 1–20 files")
		return
	}
	if group := r.FormValue("group"); group != "" && !s.groupExists(group) {
		apiError(w, 400, "group does not exist")
		return
	}
	policy := r.FormValue("onConflict")
	items := make([]stagedAudio, 0, len(files))
	defer func() { cleanupStaged(items) }()
	s.adminMu.Lock()
	defer s.adminMu.Unlock()
	for _, header := range files {
		if header.Size > maxFileSize {
			apiError(w, 413, "file too large (20 MB maximum)")
			return
		}
		f, err := header.Open()
		if err != nil {
			apiError(w, 400, "cannot read uploaded file")
			return
		}
		item, err := s.stageFile(header.Filename, f)
		f.Close()
		if err != nil {
			apiError(w, 400, err.Error())
			return
		}
		items = append(items, item)
	}
	conflicts, err := s.commitImported(items, r.FormValue("group"), r.FormValue("name"), policy, adminNickname(r))
	if err != nil {
		apiError(w, 500, err.Error())
		return
	}
	if len(conflicts) > 0 {
		w.WriteHeader(http.StatusConflict)
		writeJSON(w, map[string]any{"error": "filename already exists", "conflicts": conflicts})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "imported": len(items)})
}
func (s *Server) stageURL(ctx context.Context, raw string) (stagedAudio, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
		return stagedAudio{}, errors.New("URL must be a public HTTP(S) URL without credentials")
	}
	filename := filepath.Base(u.Path)
	if err := validateIncomingFilename(filename); err != nil {
		return stagedAudio{}, err
	}
	host := u.Hostname()
	resolver := net.DefaultResolver
	checkIP := func(ip net.IP) bool {
		if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
			return false
		}
		if ip.To4() != nil {
			v := ip.To4()
			if v[0] == 0 || v[0] == 10 || v[0] == 127 || v[0] >= 224 || (v[0] == 100 && v[1] >= 64 && v[1] <= 127) || (v[0] == 169 && v[1] == 254) || (v[0] == 192 && v[1] == 0) || (v[0] == 198 && v[1] >= 18 && v[1] <= 19) || (v[0] == 192 && v[1] == 0 && v[2] == 2) || (v[0] == 198 && v[1] == 51 && v[2] == 100) || (v[0] == 203 && v[1] == 0 && v[2] == 113) {
				return false
			}
		}
		return true
	}
	dialer := &net.Dialer{Timeout: 8 * time.Second}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, TLSHandshakeTimeout: 8 * time.Second, ResponseHeaderTimeout: 12 * time.Second}
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		h, p, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(h, host) {
			return nil, errors.New("redirected host not allowed")
		}
		ips, err := resolver.LookupIPAddr(ctx, h)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, errors.New("host has no IP address")
		}
		for _, ip := range ips {
			if !checkIP(ip.IP) {
				return nil, errors.New("URL resolves to a non-public IP address")
			}
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), p))
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("redirects not supported for URL imports")
	}}
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return stagedAudio{}, err
	}
	res, err := client.Do(req)
	if err != nil {
		return stagedAudio{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return stagedAudio{}, fmt.Errorf("remote server returned %d", res.StatusCode)
	}
	if res.ContentLength > maxFileSize {
		return stagedAudio{}, errors.New("remote file too large")
	}
	return s.stageFile(filename, res.Body)
}
func (s *Server) handleURLImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		apiError(w, 405, "method not allowed")
		return
	}
	var in struct {
		URL        string `json:"url"`
		Name       string `json:"name"`
		Group      string `json:"group"`
		OnConflict string `json:"onConflict"`
	}
	if err := readSmallJSON(r, &in); err != nil {
		apiError(w, 400, "invalid JSON")
		return
	}
	s.adminMu.Lock()
	defer s.adminMu.Unlock()
	if in.Group != "" && !s.groupExists(in.Group) {
		apiError(w, 400, "group does not exist")
		return
	}
	item, err := s.stageURL(r.Context(), in.URL)
	if err != nil {
		apiError(w, 400, err.Error())
		return
	}
	defer os.Remove(item.path)
	conflicts, err := s.commitImported([]stagedAudio{item}, in.Group, in.Name, in.OnConflict, adminNickname(r))
	if err != nil {
		apiError(w, 500, err.Error())
		return
	}
	if len(conflicts) > 0 {
		w.WriteHeader(409)
		writeJSON(w, map[string]any{"error": "filename already exists", "conflicts": conflicts})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "imported": 1})
}
func (s *Server) handleAdminSounds(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/admin/sounds" && r.Method == http.MethodGet {
		sounds, rev := s.lib.Snapshot()
		writeJSON(w, map[string]any{"sounds": sounds, "groups": s.lib.Groups(), "revision": rev})
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/admin/sounds/") {
		apiError(w, 404, "not found")
		return
	}
	filename := strings.TrimPrefix(r.URL.Path, "/api/admin/sounds/")
	if err := validateIncomingFilename(filename); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	s.adminMu.Lock()
	defer s.adminMu.Unlock()
	if !s.lib.Has(filename) {
		apiError(w, 404, "sound not found")
		return
	}
	meta := s.lib.Metadata()
	switch r.Method {
	case http.MethodPut:
		var payload struct {
			Name  string `json:"name"`
			Group string `json:"group"`
		}
		if err := readSmallJSON(r, &payload); err != nil {
			apiError(w, 400, "invalid JSON")
			return
		}
		if len([]rune(payload.Name)) > 120 || len([]rune(payload.Group)) > 64 {
			apiError(w, 400, "metadata field too long")
			return
		}
		if payload.Group == "" {
			payload.Group = "default"
		}
		if !s.groupExists(payload.Group) {
			apiError(w, 400, "group does not exist")
			return
		}
		m := meta[filename]
		m.Name = strings.TrimSpace(payload.Name)
		m.Group = strings.TrimSpace(payload.Group)
		m.UpdatedBy = adminNickname(r)
		m.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		meta[filename] = m
		if err := s.lib.WriteMetadata(meta); err != nil {
			apiError(w, 500, err.Error())
			return
		}
	case http.MethodDelete:
		// First remove file; on metadata failure, a later resync will reconcile the orphan metadata.
		if err := os.Remove(filepath.Join(s.lib.dir, filename)); err != nil {
			apiError(w, 500, err.Error())
			return
		}
		delete(meta, filename)
		if err := s.lib.WriteMetadata(meta); err != nil {
			apiError(w, 500, err.Error())
			return
		}
	default:
		apiError(w, 405, "method not allowed")
		return
	}
	if _, err := s.publishLibrary(); err != nil {
		apiError(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}
func (s *Server) handleResync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		apiError(w, 405, "method not allowed")
		return
	}
	s.adminMu.Lock()
	defer s.adminMu.Unlock()
	before, _ := s.lib.Snapshot()
	count, err := s.publishLibrary()
	if err != nil {
		apiError(w, 422, err.Error())
		return
	}
	after, _ := s.lib.Snapshot()
	oldMap := map[string]Sound{}
	for _, x := range before {
		oldMap[x.Filename] = x
	}
	add, change := 0, 0
	for _, x := range after {
		if prev, ok := oldMap[x.Filename]; !ok {
			add++
		} else if prev != x {
			change++
		}
		delete(oldMap, x.Filename)
	}
	writeJSON(w, map[string]any{"ok": true, "total": count, "added": add, "updated": change, "removed": len(oldMap)})
}
func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	// Authentication/authorization of BOTH /admin/ and /api/admin/* is delegated to the reverse proxy.
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path == "/admin/" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		s.serveAdminHTML(w, r)
		return
	}
	apiError(w, 404, "not found")
}
