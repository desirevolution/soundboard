package main

import (
	"fmt"
	"sync"
	"time"
)

const maxWaiting = 20

type QueueItem struct {
	ID       uint64 `json:"id"`
	Filename string `json:"filename"`
	By       string `json:"by"`
	ByID     string `json:"byId,omitempty"`
	ByColor  string `json:"byColor,omitempty"`
}
type Playing struct {
	QueueItem
	Name      string `json:"name"`
	StartedAt int64  `json:"startedAt"`
	EndsAt    int64  `json:"endsAt"`
	Version   string `json:"version"`
}
type QueueState struct {
	Playing     *Playing       `json:"playing"`
	Waiting     int            `json:"waiting"`
	Limit       int            `json:"limit"`
	WaitingByID map[string]int `json:"waitingById"`
}

type Queue struct {
	mu       sync.Mutex
	lib      *Library
	nextID   uint64
	playing  *Playing
	waiting  []QueueItem
	timer    *time.Timer
	onChange func(QueueState, *Playing)
}

func NewQueue(lib *Library, onChange func(QueueState, *Playing)) *Queue {
	return &Queue{lib: lib, onChange: onChange}
}
func (q *Queue) stateLocked() QueueState {
	var p *Playing
	if q.playing != nil {
		copy := *q.playing
		p = &copy
	}
	counts := map[string]int{}
	for _, item := range q.waiting {
		if item.ByID != "" {
			counts[item.ByID]++
		}
	}
	return QueueState{Playing: p, Waiting: len(q.waiting), Limit: maxWaiting, WaitingByID: counts}
}
func (q *Queue) State() QueueState { q.mu.Lock(); defer q.mu.Unlock(); return q.stateLocked() }
func (q *Queue) Enqueue(filename, by string, extra ...string) (int, error) {
	q.mu.Lock()
	if !q.lib.Has(filename) {
		q.mu.Unlock()
		return 0, fmt.Errorf("sound not found")
	}
	if len(q.waiting) >= maxWaiting {
		q.mu.Unlock()
		return 0, fmt.Errorf("queue full")
	}
	q.nextID++
	item := QueueItem{ID: q.nextID, Filename: filename, By: by}
	if len(extra) > 0 {
		item.ByID = extra[0]
	}
	if len(extra) > 1 {
		item.ByColor = extra[1]
	}
	q.waiting = append(q.waiting, item)
	position := len(q.waiting)
	if q.playing == nil {
		q.advanceLocked()
		position = 0
	}
	state := q.stateLocked()
	var started *Playing
	if position == 0 && q.playing != nil {
		p := *q.playing
		started = &p
	}
	q.mu.Unlock()
	q.onChange(state, started)
	return position, nil
}
func (q *Queue) advanceLocked() {
	if q.timer != nil {
		q.timer.Stop()
		q.timer = nil
	}
	q.playing = nil
	for len(q.waiting) > 0 {
		item := q.waiting[0]
		q.waiting = q.waiting[1:]
		sound, ok := q.lib.Get(item.Filename)
		if !ok {
			continue
		}
		now := time.Now()
		q.playing = &Playing{QueueItem: item, Name: sound.Name, Version: sound.Version, StartedAt: now.UnixMilli(), EndsAt: now.Add(time.Duration(sound.DurationMS) * time.Millisecond).UnixMilli()}
		id := item.ID
		q.timer = time.AfterFunc(time.Duration(sound.DurationMS)*time.Millisecond, func() { q.finish(id) })
		break
	}
}
func (q *Queue) finish(id uint64) {
	q.mu.Lock()
	if q.playing == nil || q.playing.ID != id {
		q.mu.Unlock()
		return
	}
	q.advanceLocked()
	state := q.stateLocked()
	var started *Playing
	if q.playing != nil {
		p := *q.playing
		started = &p
	}
	q.mu.Unlock()
	q.onChange(state, started)
}
func (q *Queue) Prune() {
	q.mu.Lock()
	remaining := q.waiting[:0]
	for _, item := range q.waiting {
		if q.lib.Has(item.Filename) {
			remaining = append(remaining, item)
		}
	}
	changed := len(remaining) != len(q.waiting)
	q.waiting = remaining
	state := q.stateLocked()
	q.mu.Unlock()
	if changed {
		q.onChange(state, nil)
	}
}
func (q *Queue) Close() {
	q.mu.Lock()
	if q.timer != nil {
		q.timer.Stop()
	}
	q.mu.Unlock()
}
