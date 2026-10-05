package motion

import (
	"sync"
	"sync/atomic"
)

// Easing is the identifier of a built-in, deterministic easing function.
type Easing string

const (
	EaseLinear     Easing = "linear"
	EaseInQuad     Easing = "in-quad"
	EaseOutQuad    Easing = "out-quad"
	EaseInOutCubic Easing = "in-out-cubic"
)

// Keyframe is a scalar keyframe local to the beginning of its containing clip.
type Keyframe struct {
	Offset float64 `json:"offset"`
	Value  float64 `json:"value"`
	Easing Easing  `json:"easing,omitempty"`
}

// Clip occupies a time interval and contains clip-local keyframes.
type Clip struct {
	ID           string     `json:"id"`
	Start        float64    `json:"start"`
	Duration     float64    `json:"duration"`
	TransitionIn float64    `json:"transitionIn"`
	Easing       Easing     `json:"easing,omitempty"`
	Keyframes    []Keyframe `json:"keyframes"`
}

// Track is a weighted source on a channel.
type Track struct {
	ID     string  `json:"id"`
	Weight float64 `json:"weight"`
	Easing Easing  `json:"easing,omitempty"`
	Clips  []Clip  `json:"clips"`
}

// Channel combines one or more weighted tracks.
type Channel struct {
	ID     string  `json:"id"`
	Tracks []Track `json:"tracks"`
}

// Timeline is an immutable-at-sample-time motion definition.
type Timeline struct {
	Version  string             `json:"version"`
	Channels map[string]Channel `json:"channels"`
}

// SampleResult is a scalar sample at a time.
type SampleResult struct {
	Time   float64 `json:"time"`
	Value  float64 `json:"value"`
	Active bool    `json:"active"`
	Weight float64 `json:"weight"`
}

// PlaybackState is the recoverable default playback cursor state.
type PlaybackState struct {
	Position       float64 `json:"position"`
	Rate           float64 `json:"rate"`
	Playing        bool    `json:"playing"`
	CommitSequence int64   `json:"commitSequence"`
	Epoch          int64   `json:"epoch"`
	LastTickToken  string  `json:"lastTickToken,omitempty"`
}

type engineSnapshot struct {
	timeline *Timeline
	playback PlaybackState
}

// Engine coordinates an immutable timeline snapshot and playback state.
type Engine struct {
	state atomic.Pointer[engineSnapshot]
	mu    sync.Mutex
}

// Cursor is an independent reader-owned sampling position.
type Cursor struct {
	engine   *Engine
	mu       sync.RWMutex
	position float64
	rate     float64
}
