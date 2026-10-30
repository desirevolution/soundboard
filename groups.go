package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Group struct {
	Name  string `json:"name"`
	Color string `json:"color"`
	Count int    `json:"count"`
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
var initialGroupColors = map[string]string{"default": "#8492a9", "reaktionen": "#db5359", "effekte": "#517dd0", "signale": "#d4a044"}

// Curated medium-bright colors that remain visible on light and dark backgrounds.
// Names do not determine the colors of newly created groups: their chosen color
// is saved in groups.json and survives restarts and renames.
var proposedColors = []string{
	"#db5359", "#517dd0", "#d4a044", "#35a079", "#9569c8", "#d8794a",
	"#3b9da6", "#c65e9b", "#7e95b4", "#bf715c", "#489ba0", "#b35dac",
	"#79a342", "#c45d7f", "#6b78cf", "#b58135", "#4d99c4", "#9c73b9",
	"#b45c36", "#688c45", "#b56589", "#448c7f", "#a76c43", "#7d7ac1",
}

func fallbackGroupColor(name string) string {
	if color, ok := initialGroupColors[name]; ok {
		return color
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return proposedColors[int(h.Sum32())%len(proposedColors)]
}
func validGroupName(name string) bool {
	if name == "" || len([]rune(name)) > 64 || strings.TrimSpace(name) != name || strings.ContainsAny(name, "/\\") {
		return false
	}
	for _, r := range name {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}
func loadGroups(dir string) (map[string]string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "groups.json"))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var stored map[string]string
	if err := json.Unmarshal(raw, &stored); err != nil || stored == nil {
		return nil, fmt.Errorf("invalid groups.json: %v", err)
	}
	for name, color := range stored {
		if !validGroupName(name) || !hexColor.MatchString(color) {
			return nil, fmt.Errorf("invalid group/color in groups.json: %q", name)
		}
	}
	return stored, nil
}
func (l *Library) Groups() []Group {
	l.mu.RLock()
	defer l.mu.RUnlock()
	counts := map[string]int{}
	for _, v := range l.entries {
		counts[v.Group]++
	}
	names := map[string]bool{"default": true}
	for k := range l.groups {
		names[k] = true
	}
	for k := range counts {
		names[k] = true
	}
	result := make([]Group, 0, len(names))
	for name := range names {
		color := l.groups[name]
		if color == "" {
			color = fallbackGroupColor(name)
		}
		result = append(result, Group{Name: name, Color: color, Count: counts[name]})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name == "default" {
			return true
		}
		if result[j].Name == "default" {
			return false
		}
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result
}

// WriteGroups uses an atomic rename. adminMu serializes all mutations.
func (l *Library) WriteGroups(groups map[string]string) error {
	for name, color := range groups {
		if !validGroupName(name) || !hexColor.MatchString(color) {
			return fmt.Errorf("invalid group/color: %s", name)
		}
	}
	return writeAtomicJSON(l.dir, "groups.json", groups)
}
func writeAtomicJSON(dir, name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	f, err := os.CreateTemp(dir, ".json-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0644); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, name))
}

// chooseGroupColor chooses a visually distinct, random color and persists it
// through the normal group creation flow. Prefer unused colors well separated
// from existing groups, but still return a candidate when the palette is full.
func chooseGroupColor(existing []Group) string {
	used := map[string]bool{}
	for _, g := range existing {
		used[strings.ToLower(g.Color)] = true
	}
	candidates := make([]string, 0, len(proposedColors))
	candidates = append(candidates, proposedColors...)
	// Extra colors for very large libraries, still in a readable saturation range.
	for h := 0; h < 360; h += 15 {
		candidates = append(candidates, hslHex(float64(h), 0.58, 0.51))
	}
	best := []string{}
	bestScore := -1.0
	for _, candidate := range candidates {
		if used[strings.ToLower(candidate)] {
			continue
		}
		score := 1000.0
		for color := range used {
			d := rgbDistance(candidate, color)
			if d < score {
				score = d
			}
		}
		// Randomly choose from distinctly separated candidates; when all are
		// too similar, choose among the most distant available colors.
		if score >= 95 {
			best = append(best, candidate)
		}
		if score > bestScore {
			bestScore = score
		}
	}
	if len(best) == 0 {
		for _, candidate := range candidates {
			if used[strings.ToLower(candidate)] {
				continue
			}
			score := 1000.0
			for color := range used {
				if d := rgbDistance(candidate, color); d < score {
					score = d
				}
			}
			if score >= bestScore-0.01 {
				best = append(best, candidate)
			}
		}
	}
	if len(best) == 0 {
		return "#517dd0"
	}
	if index, err := rand.Int(rand.Reader, big.NewInt(int64(len(best)))); err == nil {
		return best[index.Int64()]
	}
	return best[0]
}

func rgbDistance(a, b string) float64 {
	parse := func(s string, i int) float64 {
		v, err := strconv.ParseUint(s[i:i+2], 16, 8)
		if err != nil {
			return 0
		}
		return float64(v)
	}
	if !hexColor.MatchString(a) || !hexColor.MatchString(b) {
		return 0
	}
	dr := parse(a, 1) - parse(b, 1)
	dg := parse(a, 3) - parse(b, 3)
	db := parse(a, 5) - parse(b, 5)
	// Weighted RGB difference: green carries greater perceptual weight.
	return math.Sqrt(0.30*dr*dr + 0.59*dg*dg + 0.11*db*db)
}

func hslHex(h, s, l float64) string {
	c := (1 - math.Abs(2*l-1)) * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := l - c/2
	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return fmt.Sprintf("#%02x%02x%02x", int(math.Round((r+m)*255)), int(math.Round((g+m)*255)), int(math.Round((b+m)*255)))
}
