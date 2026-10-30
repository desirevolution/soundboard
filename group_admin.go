package main

import (
	"fmt"
	"net/http"
	"strings"
)

func (s *Server) handleGroups(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/admin/groups/suggest-color" {
		if r.Method != http.MethodGet {
			apiError(w, 405, "method not allowed")
			return
		}
		current := s.lib.Groups()
		if excluded := r.URL.Query().Get("exclude"); hexColor.MatchString(excluded) {
			current = append(current, Group{Color: excluded})
		}
		writeJSON(w, map[string]string{"color": chooseGroupColor(current)})
		return
	}
	if r.URL.Path == "/api/admin/groups" {
		if r.Method == http.MethodGet {
			writeJSON(w, map[string]any{"groups": s.lib.Groups()})
			return
		}
		if r.Method != http.MethodPost {
			apiError(w, 405, "method not allowed")
			return
		}
		var in struct {
			Name  string `json:"name"`
			Color string `json:"color"`
		}
		if readSmallJSON(r, &in) != nil {
			apiError(w, 400, "invalid JSON")
			return
		}
		in.Name = strings.TrimSpace(in.Name)
		if !validGroupName(in.Name) {
			apiError(w, 400, "invalid group name")
			return
		}
		s.adminMu.Lock()
		defer s.adminMu.Unlock()
		groups := s.lib.Groups()
		for _, g := range groups {
			if strings.EqualFold(g.Name, in.Name) {
				apiError(w, 409, "group already exists")
				return
			}
		}
		if in.Color == "" {
			in.Color = chooseGroupColor(groups)
		}
		if !hexColor.MatchString(in.Color) {
			apiError(w, 400, "invalid color; use #RRGGBB")
			return
		}
		colors := map[string]string{}
		for _, g := range groups {
			colors[g.Name] = g.Color
		}
		colors[in.Name] = in.Color
		if err := s.lib.WriteGroups(colors); err != nil {
			apiError(w, 500, err.Error())
			return
		}
		if _, err := s.publishLibrary(); err != nil {
			apiError(w, 500, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/admin/groups/") {
		apiError(w, 404, "not found")
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/admin/groups/")
	if !validGroupName(name) {
		apiError(w, 400, "invalid group name")
		return
	}
	s.adminMu.Lock()
	defer s.adminMu.Unlock()
	groups := s.lib.Groups()
	colors := map[string]string{}
	found := false
	for _, g := range groups {
		colors[g.Name] = g.Color
		if g.Name == name {
			found = true
		}
	}
	if !found {
		apiError(w, 404, "group not found")
		return
	}
	if name == "default" {
		apiError(w, 400, "default group is reserved")
		return
	}
	meta := s.lib.Metadata()
	switch r.Method {
	case http.MethodPut:
		var in struct {
			Name  string `json:"name"`
			Color string `json:"color"`
		}
		if readSmallJSON(r, &in) != nil {
			apiError(w, 400, "invalid JSON")
			return
		}
		in.Name = strings.TrimSpace(in.Name)
		if !validGroupName(in.Name) || !hexColor.MatchString(in.Color) || in.Name == "default" {
			apiError(w, 400, "invalid group name or color")
			return
		}
		for _, g := range groups {
			if g.Name != name && strings.EqualFold(g.Name, in.Name) {
				apiError(w, 409, "group already exists")
				return
			}
		}
		delete(colors, name)
		colors[in.Name] = in.Color
		if in.Name != name {
			for filename, m := range meta {
				if m.Group == name {
					m.Group = in.Name
					meta[filename] = m
				}
			}
			if err := s.lib.WriteMetadata(meta); err != nil {
				apiError(w, 500, err.Error())
				return
			}
		}
	case http.MethodDelete:
		delete(colors, name)
		for filename, m := range meta {
			if m.Group == name {
				m.Group = "default"
				meta[filename] = m
			}
		}
		if err := s.lib.WriteMetadata(meta); err != nil {
			apiError(w, 500, err.Error())
			return
		}
	default:
		apiError(w, 405, "method not allowed")
		return
	}
	if err := s.lib.WriteGroups(colors); err != nil {
		apiError(w, 500, fmt.Sprintf("group changes partially saved; use resync: %v", err))
		return
	}
	if _, err := s.publishLibrary(); err != nil {
		apiError(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}
func (s *Server) groupExists(name string) bool {
	if name == "" {
		name = "default"
	}
	for _, g := range s.lib.Groups() {
		if g.Name == name {
			return true
		}
	}
	return false
}
