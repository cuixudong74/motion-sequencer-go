package motion

import "math"

// Playback returns a copy of the committed playback state.
func (e *Engine) Playback() PlaybackState {
	return e.currentSnapshot().playback
}

func (e *Engine) commitPlayback(next PlaybackState) error {
	if err := validatePlayback(next); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	snap := e.currentSnapshot()
	next.CommitSequence = snap.playback.CommitSequence + 1
	e.state.Store(&engineSnapshot{timeline: snap.timeline, playback: next})
	return nil
}

// Seek publishes an explicit jump or retreat to t.
func (e *Engine) Seek(t float64) error {
	if err := validateSampleTime(t); err != nil {
		return err
	}
	next := e.Playback()
	next.Position = t
	next.LastTickToken = ""
	return e.commitPlayback(next)
}

// SetRate changes a single scalar used by subsequent Tick calls. Existing
// positions are not reinterpreted.
func (e *Engine) SetRate(rate float64) error {
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate <= 0 {
		return &ValidationError{Operation: "set rate", Reason: "rate must be finite and positive"}
	}
	next := e.Playback()
	next.Rate = rate
	return e.commitPlayback(next)
}

// SetPlaying pauses or resumes. It does not alter position.
func (e *Engine) SetPlaying(playing bool) {
	next := e.Playback()
	next.Playing = playing
	_ = e.commitPlayback(next)
}

// Tick moves by dt seconds multiplied by the committed rate. Negative dt is
// allowed for an explicit local rewind; the position is clamped at zero.
func (e *Engine) Tick(dt float64) (PlaybackState, error) {
	if math.IsNaN(dt) || math.IsInf(dt, 0) {
		return PlaybackState{}, &ValidationError{Operation: "tick", Reason: "dt must be finite"}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.tickLocked(dt, "")
}

// TickIdempotent applies dt once for token. Replaying the same token after a
// retry returns the state produced by the first call without advancing again.
// An empty token behaves like Tick.
func (e *Engine) TickIdempotent(dt float64, token string) (PlaybackState, error) {
	if math.IsNaN(dt) || math.IsInf(dt, 0) {
		return PlaybackState{}, &ValidationError{Operation: "tick", Reason: "dt must be finite"}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if token != "" {
		if current := e.currentSnapshot().playback; current.LastTickToken == token {
			return current, nil
		}
	}
	return e.tickLocked(dt, token)
}

func (e *Engine) tickLocked(dt float64, token string) (PlaybackState, error) {
	snap := e.currentSnapshot()
	next := snap.playback
	next.Position = math.Max(0, next.Position+dt*next.Rate)
	next.LastTickToken = token
	next.CommitSequence++
	if err := validatePlayback(next); err != nil {
		return PlaybackState{}, err
	}
	e.state.Store(&engineSnapshot{timeline: snap.timeline, playback: next})
	return next, nil
}

// SamplePlayback samples the committed default playback position.
func (e *Engine) SamplePlayback(channel string) (SampleResult, error) {
	state := e.Playback()
	return e.SampleChannel(channel, state.Position)
}

// NewCursor creates a reader-owned cursor. Cursors do not modify the engine and
// therefore cannot slow or block timeline advancement or other readers.
func NewCursor(engine *Engine, start float64) (*Cursor, error) {
	if engine == nil {
		return nil, &ValidationError{Operation: "new cursor", Reason: "engine is nil"}
	}
	if err := validateSampleTime(start); err != nil {
		return nil, err
	}
	return &Cursor{engine: engine, position: start, rate: 1}, nil
}

// Position returns the cursor position.
func (c *Cursor) Position() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.position
}

// SetRate sets this cursor's independent positive rate.
func (c *Cursor) SetRate(rate float64) error {
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate <= 0 {
		return &ValidationError{Operation: "set cursor rate", Reason: "rate must be finite and positive"}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rate = rate
	return nil
}

// Seek moves this cursor without affecting the engine.
func (c *Cursor) Seek(t float64) error {
	if err := validateSampleTime(t); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.position = t
	return nil
}

// Tick advances the cursor by its own rate. Negative dt rewinds to at most zero.
func (c *Cursor) Tick(dt float64) (float64, error) {
	if math.IsNaN(dt) || math.IsInf(dt, 0) {
		return 0, &ValidationError{Operation: "tick cursor", Reason: "dt must be finite"}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.position = math.Max(0, c.position+dt*c.rate)
	return c.position, nil
}

// Sample reads the shared immutable timeline at this cursor's position.
func (c *Cursor) Sample(channel string) (SampleResult, error) {
	c.mu.RLock()
	t := c.position
	engine := c.engine
	c.mu.RUnlock()
	return engine.SampleChannel(channel, t)
}
