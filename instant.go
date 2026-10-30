package main

import (
	"fmt"
	"sync"
	"time"
)

// InstantPlayer is a single, server-owned global sound channel, separate from
// the FIFO queue. A new trigger replaces only the current instant sound.
// Sequence numbers let browsers discard out-of-order start/stop broadcasts.
type InstantState struct {
	Sequence uint64   `json:"sequence"`
	Playing  *Playing `json:"playing"`
}

type InstantPlayer struct {
	mu       sync.Mutex
	lib      *Library
	sequence uint64
	playing  *Playing
	timer    *time.Timer
	onChange func(InstantState, bool)
}

func NewInstantPlayer(lib *Library, onChange func(InstantState, bool)) *InstantPlayer {
	return &InstantPlayer{lib: lib, onChange: onChange}
}
func (p *InstantPlayer) stateLocked() InstantState {
	var playing *Playing
	if p.playing != nil {
		c := *p.playing
		playing = &c
	}
	return InstantState{Sequence: p.sequence, Playing: playing}
}
func (p *InstantPlayer) State() InstantState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stateLocked()
}
func (p *InstantPlayer) Trigger(filename, by, byID, byColor string) (Playing, error) {
	p.mu.Lock()
	sound, ok := p.lib.Get(filename)
	if !ok {
		p.mu.Unlock()
		return Playing{}, fmt.Errorf("sound not found")
	}
	if p.timer != nil {
		p.timer.Stop()
		p.timer = nil
	}
	p.sequence++
	now := time.Now()
	play := Playing{QueueItem: QueueItem{ID: p.sequence, Filename: filename, By: by, ByID: byID, ByColor: byColor}, Name: sound.Name, Version: sound.Version, StartedAt: now.UnixMilli(), EndsAt: now.Add(time.Duration(sound.DurationMS) * time.Millisecond).UnixMilli()}
	p.playing = &play
	seq := p.sequence
	p.timer = time.AfterFunc(time.Duration(sound.DurationMS)*time.Millisecond, func() { p.finish(seq) })
	state := p.stateLocked()
	p.mu.Unlock()
	p.onChange(state, true)
	return play, nil
}
func (p *InstantPlayer) finish(seq uint64) {
	p.mu.Lock()
	if p.playing == nil || p.sequence != seq {
		p.mu.Unlock()
		return
	}
	p.sequence++
	p.playing = nil
	p.timer = nil
	state := p.stateLocked()
	p.mu.Unlock()
	p.onChange(state, false)
}
func (p *InstantPlayer) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.timer != nil {
		p.timer.Stop()
		p.timer = nil
	}
}
