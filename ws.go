package main

// Small RFC 6455 subset for same-origin browser clients: text messages, ping/pong,
// close frames and continuation frames. No external runtime dependencies.

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
const maxWSMessage = 4096

type WSClient struct {
	conn     net.Conn
	reader   *bufio.Reader
	writeMu  sync.Mutex
	once     sync.Once
	lastPong atomic.Int64
}

func (c *WSClient) Close() { c.once.Do(func() { _ = c.conn.Close() }) }
func (c *WSClient) writeFrame(op byte, data []byte) error {
	if len(data) > 65535 {
		return errors.New("frame too large")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(4 * time.Second))
	hdr := []byte{0x80 | op}
	if len(data) < 126 {
		hdr = append(hdr, byte(len(data)))
	} else {
		hdr = append(hdr, 126, byte(len(data)>>8), byte(len(data)))
	}
	if _, err := c.conn.Write(hdr); err != nil {
		return err
	}
	if len(data) > 0 {
		_, err := c.conn.Write(data)
		return err
	}
	return nil
}
func (c *WSClient) Send(data []byte) error { return c.writeFrame(1, data) }
func (c *WSClient) ReadMessage() ([]byte, error) {
	result := make([]byte, 0, 512)
	inFragment := false
	for {
		_ = c.conn.SetReadDeadline(time.Now().Add(70 * time.Second))
		var header [2]byte
		if _, err := io.ReadFull(c.reader, header[:]); err != nil {
			return nil, err
		}
		fin := header[0]&0x80 != 0
		op := header[0] & 0x0f
		if header[0]&0x70 != 0 {
			return nil, errors.New("extensions unsupported")
		}
		if header[1]&0x80 == 0 {
			return nil, errors.New("client frame not masked")
		}
		size := uint64(header[1] & 0x7f)
		if size == 126 {
			var n [2]byte
			if _, err := io.ReadFull(c.reader, n[:]); err != nil {
				return nil, err
			}
			size = uint64(binary.BigEndian.Uint16(n[:]))
		}
		if size == 127 {
			var n [8]byte
			if _, err := io.ReadFull(c.reader, n[:]); err != nil {
				return nil, err
			}
			size = binary.BigEndian.Uint64(n[:])
		}
		if size > maxWSMessage {
			return nil, errors.New("message too large")
		}
		if op >= 8 && (!fin || size > 125) {
			return nil, errors.New("invalid control frame")
		}
		var mask [4]byte
		if _, err := io.ReadFull(c.reader, mask[:]); err != nil {
			return nil, err
		}
		data := make([]byte, int(size))
		if _, err := io.ReadFull(c.reader, data); err != nil {
			return nil, err
		}
		for i := range data {
			data[i] ^= mask[i%4]
		}
		switch op {
		case 8:
			_ = c.writeFrame(8, data)
			return nil, io.EOF
		case 9:
			if err := c.writeFrame(10, data); err != nil {
				return nil, err
			}
			continue
		case 10:
			c.lastPong.Store(time.Now().UnixNano())
			continue
		case 1:
			if inFragment {
				return nil, errors.New("unexpected text fragment")
			}
			result = append(result, data...)
			if fin {
				return result, nil
			}
			inFragment = true
		case 0:
			if !inFragment {
				return nil, errors.New("unexpected continuation")
			}
			if len(result)+len(data) > maxWSMessage {
				return nil, errors.New("message too large")
			}
			result = append(result, data...)
			if fin {
				return result, nil
			}
		default:
			return nil, errors.New("unsupported frame opcode")
		}
	}
}
func sameOrigin(r *http.Request) bool {
	raw := r.Header.Get("Origin")
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && strings.EqualFold(u.Host, r.Host)
}
func upgradeWS(w http.ResponseWriter, r *http.Request) (*WSClient, error) {
	if r.Method != http.MethodGet || !sameOrigin(r) || !headerHasToken(r.Header.Get("Connection"), "upgrade") || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") || r.Header.Get("Sec-WebSocket-Version") != "13" {
		return nil, errors.New("invalid websocket upgrade")
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	decoded, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(decoded) != 16 {
		return nil, errors.New("invalid websocket key")
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("hijacking unsupported")
	}
	conn, rw, err := hijacker.Hijack()
	if err != nil {
		return nil, err
	}
	accept := sha1.Sum([]byte(key + wsGUID))
	_, err = fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(accept[:]))
	if err == nil {
		err = rw.Flush()
	}
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	c := &WSClient{conn: conn, reader: rw.Reader}
	c.lastPong.Store(time.Now().UnixNano())
	return c, nil
}
func headerHasToken(header, token string) bool {
	for _, part := range strings.Split(header, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

// Browser WebSockets respond automatically to server ping control frames.
// A one-second interval offers prompt liveness detection while a generous
// timeout avoids dropping mobile connections for momentary stalls.
func (c *WSClient) Heartbeat(stop <-chan struct{}) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if time.Since(time.Unix(0, c.lastPong.Load())) > 15*time.Second {
				c.Close()
				return
			}
			if err := c.writeFrame(9, nil); err != nil {
				c.Close()
				return
			}
		}
	}
}

type PresenceEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Color       string `json:"color"`
	Connections int    `json:"connections"`
	Admin       bool   `json:"admin"`
}
type participant struct {
	ID, Name, Color string
	Admin           bool
}

var participantPalette = []string{
	"#3cb38c", "#e69d48", "#8a79dd", "#4d9ed9", "#e269a2", "#9aa84b",
	"#cf7559", "#56b6b4", "#c78bd1", "#a99c4d", "#6293d2", "#d06d74",
}

func colorForParticipant(id string) string {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(id))
	return participantPalette[int(hash.Sum32())%len(participantPalette)]
}
func validParticipantID(id string) bool {
	if len(id) < 8 || len(id) > 80 {
		return false
	}
	for _, r := range id {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-') {
			return false
		}
	}
	return true
}

// Online participants are keyed by browser-generated ID. Separate tabs share identity.
// Nicknames are display-only and can be spoofed, not authentication.
type Hub struct {
	mu      sync.RWMutex
	clients map[*WSClient]participant
}

func NewHub() *Hub              { return &Hub{clients: map[*WSClient]participant{}} }
func (h *Hub) Join(c *WSClient) { h.JoinWithMode(c, false) }
func (h *Hub) JoinWithMode(c *WSClient, admin bool) {
	h.mu.Lock()
	h.clients[c] = participant{Admin: admin}
	h.mu.Unlock()
}
func (h *Hub) Identity(c *WSClient) participant {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.clients[c]
}
func (h *Hub) SetIdentity(c *WSClient, name, id string) {
	h.mu.Lock()
	if !validParticipantID(id) {
		id = fmt.Sprintf("connection-%p", c)
	}
	old, ok := h.clients[c]
	if ok {
		h.clients[c] = participant{ID: id, Name: name, Color: colorForParticipant(id), Admin: old.Admin}
	}
	h.mu.Unlock()
	if ok && (old.Name != name || old.ID != id) {
		h.BroadcastPresence()
	}
}
func (h *Hub) Leave(c *WSClient) {
	h.mu.Lock()
	old, ok := h.clients[c]
	delete(h.clients, c)
	h.mu.Unlock()
	c.Close()
	if ok && old.Name != "" {
		h.BroadcastPresence()
	}
}
func (h *Hub) Presence() []PresenceEntry {
	h.mu.RLock()
	groups := map[string]PresenceEntry{}
	for _, p := range h.clients {
		if p.Name == "" {
			continue
		}
		entry, ok := groups[p.ID]
		if !ok {
			entry = PresenceEntry{ID: p.ID, Name: p.Name, Color: p.Color}
		}
		entry.Connections++
		entry.Admin = entry.Admin || p.Admin
		groups[p.ID] = entry
	}
	h.mu.RUnlock()
	out := make([]PresenceEntry, 0, len(groups))
	for _, e := range groups {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].ID < out[j].ID
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}
func (h *Hub) BroadcastPresence() {
	h.Broadcast(marshal(map[string]any{"type": "presence", "users": h.Presence()}))
}
func (h *Hub) Broadcast(message []byte) {
	h.mu.RLock()
	clients := make([]*WSClient, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.RUnlock()
	for _, c := range clients {
		if err := c.Send(message); err != nil {
			h.Leave(c)
		}
	}
}
