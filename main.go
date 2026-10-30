package main

import (
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

//go:embed web/index.html web/style.css web/audio.js web/app.js web/admin.html web/admin.js web/admin.css
var webFiles embed.FS

//go:embed VERSION
var buildVersion string

type Server struct {
	lib      *Library
	queue    *Queue
	instant  *InstantPlayer
	hub      *Hub
	requests atomic.Uint64
	adminMu  sync.Mutex
}

func NewServer(dir string, probe func(string) (time.Duration, error)) *Server {
	srv := &Server{lib: NewLibrary(dir, probe), hub: NewHub()}
	srv.queue = NewQueue(srv.lib, func(state QueueState, started *Playing) {
		srv.hub.Broadcast(marshal(map[string]any{"type": "state", "state": state}))
		if started != nil {
			srv.hub.Broadcast(marshal(map[string]any{"type": "play", "playing": started}))
		}
	})
	srv.instant = NewInstantPlayer(srv.lib, func(state InstantState, start bool) {
		srv.hub.Broadcast(marshal(map[string]any{"type": "instant", "state": state, "start": start}))
	})
	return srv
}
func marshal(v any) []byte { b, _ := json.Marshal(v); return b }
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("/api/sounds", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			http.Error(w, "method not allowed", 405)
			return
		}
		sounds, rev := s.lib.Snapshot()
		writeJSON(w, map[string]any{"sounds": sounds, "groups": s.lib.Groups(), "revision": rev})
	})
	mux.HandleFunc("/api/audio/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			http.Error(w, "method not allowed", 405)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/api/audio/")
		if !safeName(name) {
			http.NotFound(w, r)
			return
		}
		sound, ok := s.lib.Get(name)
		if !ok {
			http.NotFound(w, r)
			return
		}
		if version := r.URL.Query().Get("v"); version != "" && version != sound.Version {
			http.Error(w, "sound changed", 409)
			return
		}
		f, err := os.Open(filepath.Join(s.lib.dir, name))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			http.NotFound(w, r)
			return
		}
		switch strings.ToLower(filepath.Ext(name)) {
		case ".mp3":
			w.Header().Set("Content-Type", "audio/mpeg")
		case ".wav":
			w.Header().Set("Content-Type", "audio/wav")
		case ".ogg", ".opus":
			w.Header().Set("Content-Type", "audio/ogg")
		case ".m4a":
			w.Header().Set("Content-Type", "audio/mp4")
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "private, max-age=300")
		http.ServeContent(w, r, name, info.ModTime(), f)
	})
	mux.HandleFunc("/ws", s.handleWS)
	// This endpoint must be protected by the same proxy rules as /admin/.
	mux.HandleFunc("/admin/ws", func(w http.ResponseWriter, r *http.Request) { s.handleWSMode(w, r, true) })
	mux.HandleFunc("/admin/", s.handleAdmin)
	mux.HandleFunc("/admin/admin.js", s.serveAdminStatic)
	mux.HandleFunc("/admin/admin.css", s.serveAdminStatic)
	mux.HandleFunc("/api/admin/upload", s.handleUpload)
	mux.HandleFunc("/api/admin/import-url", s.handleURLImport)
	mux.HandleFunc("/api/admin/resync", s.handleResync)
	mux.HandleFunc("/api/admin/groups", s.handleGroups)
	mux.HandleFunc("/api/admin/groups/", s.handleGroups)
	mux.HandleFunc("/api/admin/sounds", s.handleAdminSounds)
	mux.HandleFunc("/api/admin/sounds/", s.handleAdminSounds)
	static, err := fs.Sub(webFiles, "web")
	if err != nil {
		panic(err)
	}
	files := http.FileServer(http.FS(static))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/style.css" && r.URL.Path != "/audio.js" && r.URL.Path != "/app.js" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// Serve the index via the original "/" URL. Go's FileServer
		// redirects explicit "/index.html" requests to "./"; rewriting
		// r.URL.Path to "/index.html" here creates a redirect loop.
		files.ServeHTTP(w, r)
	})
	return mux
}
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) { s.handleWSMode(w, r, false) }
func (s *Server) handleWSMode(w http.ResponseWriter, r *http.Request, isAdmin bool) {
	c, err := upgradeWS(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.hub.JoinWithMode(c, isAdmin)
	stopHeartbeat := make(chan struct{})
	go c.Heartbeat(stopHeartbeat)
	defer func() { close(stopHeartbeat); s.hub.Leave(c) }()
	sounds, rev := s.lib.Snapshot()
	if err := c.Send(marshal(map[string]any{"type": "init", "groups": s.lib.Groups(), "sounds": sounds, "revision": rev, "state": s.queue.State(), "instant": s.instant.State(), "users": s.hub.Presence()})); err != nil {
		return
	}
	// A lightweight per-connection rate limit, independent of any proxy authentication.
	tokens := 6
	lastRefill := time.Now()
	for {
		data, err := c.ReadMessage()
		if err != nil {
			return
		}
		var msg struct {
			Type          string `json:"type"`
			Filename      string `json:"filename"`
			Name          string `json:"name"`
			ParticipantID string `json:"participantId"`
			RequestID     uint64 `json:"requestId"`
		}
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		if msg.Type == "set_name" {
			name := cleanNickname(msg.Name)
			if name == "" {
				_ = c.Send(marshal(map[string]any{"type": "name_error", "error": "Bitte einen Namen eingeben (maximal 32 Zeichen)."}))
				continue
			}
			s.hub.SetIdentity(c, name, msg.ParticipantID)
			_ = c.Send(marshal(map[string]any{"type": "name_ack", "name": name}))
			continue
		}
		if msg.Type != "enqueue" && msg.Type != "instant" {
			continue
		}
		who := s.hub.Identity(c)
		nick := who.Name
		if nick == "" {
			_ = c.Send(marshal(map[string]any{"type": "ack", "requestId": msg.RequestID, "ok": false, "error": "Bitte zuerst einen Nickname wählen"}))
			continue
		}
		now := time.Now()
		if now.Sub(lastRefill) >= 3*time.Second {
			tokens = 6
			lastRefill = now
		}
		if tokens <= 0 {
			_ = c.Send(marshal(map[string]any{"type": "ack", "requestId": msg.RequestID, "ok": false, "error": "Zu viele Klicks"}))
			continue
		}
		tokens--
		if msg.Type == "instant" {
			_, err := s.instant.Trigger(msg.Filename, nick, who.ID, who.Color)
			response := map[string]any{"type": "instant_ack", "requestId": msg.RequestID, "ok": err == nil}
			if err != nil {
				response["error"] = err.Error()
			}
			if err := c.Send(marshal(response)); err != nil {
				return
			}
			continue
		}
		pos, err := s.queue.Enqueue(msg.Filename, nick, who.ID, who.Color)
		response := map[string]any{"type": "ack", "requestId": msg.RequestID, "ok": err == nil, "position": pos}
		if err != nil {
			response["error"] = err.Error()
		}
		if err == nil {
			s.hub.Broadcast(marshal(map[string]any{"type": "queued", "filename": msg.Filename, "by": nick, "byId": who.ID, "byColor": who.Color}))
		}
		if err := c.Send(marshal(response)); err != nil {
			return
		}
	}
}
func cleanNickname(name string) string {
	for _, r := range name {
		if r < 32 || r == 127 {
			return ""
		}
	}
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 32 {
		return ""
	}
	return name
}

func (s *Server) serveAdminHTML(w http.ResponseWriter, r *http.Request) {
	static, _ := fs.Sub(webFiles, "web")
	data, err := fs.ReadFile(static, "admin.html")
	if err != nil {
		http.Error(w, "admin unavailable", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}
func (s *Server) serveAdminStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", 405)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/admin/")
	if name != "admin.js" && name != "admin.css" {
		http.NotFound(w, r)
		return
	}
	static, _ := fs.Sub(webFiles, "web")
	data, err := fs.ReadFile(static, name)
	if err != nil {
		http.Error(w, "admin asset unavailable", 500)
		return
	}
	if name == "admin.js" {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	} else {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}
func main() {
	showVersion := flag.Bool("version", false, "print Soundboard version and exit")
	addr := flag.String("addr", envOr("LISTEN_ADDR", ":8080"), "HTTP listen address")
	dir := flag.String("sounds", envOr("SOUNDS_DIR", "/data/sounds"), "flat audio directory")
	flag.Parse()
	if *showVersion {
		_, _ = os.Stdout.WriteString(strings.TrimSpace(buildVersion) + "\n")
		return
	}
	if err := os.MkdirAll(*dir, 0755); err != nil {
		log.Fatalf("sounds directory: %v", err)
	}
	srv := NewServer(*dir, probeFF)
	if _, err := srv.lib.Scan(); err != nil {
		log.Printf("startup scan: %v", err)
	}
	sounds, _ := srv.lib.Snapshot()
	log.Printf("Soundboard %s: %d sound(s) from %s", strings.TrimSpace(buildVersion), len(sounds), *dir)
	httpServer := &http.Server{Addr: *addr, Handler: srv.Handler(), ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 1 << 16}
	log.Printf("listening on %s", *addr)
	err := httpServer.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
func envOr(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}
