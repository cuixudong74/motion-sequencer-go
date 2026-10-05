package motion

import (
	"fmt"
	"math"
)

// NewEngine validates tl and stores a normalized deep copy.
func NewEngine(tl *Timeline) (*Engine, error) {
	if err := validateTimeline(tl); err != nil {
		return nil, err
	}
	e := &Engine{}
	e.state.Store(&engineSnapshot{
		timeline: cloneTimeline(tl),
		playback: PlaybackState{Rate: 1, CommitSequence: 0, Epoch: 1},
	})
	return e, nil
}

func (e *Engine) currentSnapshot() *engineSnapshot {
	if snap := e.state.Load(); snap != nil {
		return snap
	}
	return &engineSnapshot{
		timeline: &Timeline{Channels: map[string]Channel{}},
		playback: PlaybackState{Rate: 1},
	}
}

// Timeline returns a deep copy of the currently published timeline.
func (e *Engine) Timeline() Timeline {
	return *cloneTimeline(e.currentSnapshot().timeline)
}

// ReplaceTimeline validates the complete new definition before publishing it.
// The current timeline remains visible if validation fails.
func (e *Engine) ReplaceTimeline(tl *Timeline) error {
	if err := validateTimeline(tl); err != nil {
		return err
	}
	copied := cloneTimeline(tl)

	e.mu.Lock()
	defer e.mu.Unlock()
	snap := e.currentSnapshot()
	e.state.Store(&engineSnapshot{timeline: copied, playback: snap.playback})
	return nil
}

// UpdateTimeline gives a mutator an isolated working copy. Its changes are
// validated atomically and then published as one snapshot.
func (e *Engine) UpdateTimeline(mutate func(*Timeline) error) error {
	if mutate == nil {
		return &ValidationError{Operation: "update timeline", Reason: "mutator is nil"}
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	snap := e.currentSnapshot()
	working := cloneTimeline(snap.timeline)
	if err := mutate(working); err != nil {
		return err
	}
	if err := validateTimeline(working); err != nil {
		return err
	}
	e.state.Store(&engineSnapshot{timeline: working, playback: snap.playback})
	return nil
}

// SampleChannel evaluates one channel at an absolute time. Sampling is pure:
// it mutates nothing and therefore the call order and frequency cannot affect
// the result.
func (e *Engine) SampleChannel(channel string, t float64) (SampleResult, error) {
	if err := validateSampleTime(t); err != nil {
		return SampleResult{}, err
	}
	snap := e.currentSnapshot()
	ch, ok := snap.timeline.Channels[channel]
	if !ok {
		return SampleResult{}, fmt.Errorf("%w: %s", ErrChannelNotFound, channel)
	}
	return sampleChannel(ch, t), nil
}

func validateSampleTime(t float64) error {
	if t < 0 || math.IsNaN(t) || math.IsInf(t, 0) {
		return &ValidationError{Operation: "sample", Reason: "time must be finite and non-negative"}
	}
	return nil
}

// SampleAll returns a copied result map for every channel at time t.
func (e *Engine) SampleAll(t float64) map[string]SampleResult {
	if err := validateSampleTime(t); err != nil {
		return map[string]SampleResult{}
	}
	snap := e.currentSnapshot()
	out := make(map[string]SampleResult, len(snap.timeline.Channels))
	for id, ch := range snap.timeline.Channels {
		out[id] = sampleChannel(ch, t)
	}
	return out
}
