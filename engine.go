package motion

import (
	"errors"
	"sync/atomic"
)

var (
	ErrInvalidPlayback = errors.New("invalid playback")
)

// Engine owns one atomically replaceable, immutable state snapshot. Readers
// load one pointer for the duration of a sample and never hold a mutex.
type Engine struct {
	state atomic.Pointer[engineState]
}

func NewEngine(t Timeline) (*Engine, error) {
	if err := ValidateTimeline(t); err != nil {
		return nil, err
	}
	e := &Engine{}
	e.state.Store(&engineState{
		timeline: cloneTimeline(t),
		playback: Playback{Rate: 1},
	})
	return e, nil
}

// Sample evaluates an arbitrary logical time; it does not change playback.
func (e *Engine) Sample(at float64) map[string]float64 {
	s := e.snapshot()
	return sampleTimeline(s.timeline, at)
}

// SampleNow evaluates the current playback position.
func (e *Engine) SampleNow() map[string]float64 {
	s := e.snapshot()
	return sampleTimeline(s.timeline, s.playback.Position)
}

func (e *Engine) Position() float64 {
	return e.snapshot().playback.Position
}

func (e *Engine) PlaybackState() Playback {
	return e.snapshot().playback
}

func (e *Engine) Timeline() Timeline {
	return cloneTimeline(e.snapshot().timeline)
}

func (e *Engine) Revision() uint64 {
	return e.snapshot().revision
}

func (e *Engine) Play() {
	_ = e.update(func(s *engineState) error {
		s.playback.Playing = true
		return nil
	})
}

func (e *Engine) Pause() {
	_ = e.update(func(s *engineState) error {
		s.playback.Playing = false
		return nil
	})
}

func (e *Engine) SetRate(rate float64) error {
	return e.update(func(s *engineState) error {
		if !finiteNumber(rate) {
			return ErrInvalidPlayback
		}
		s.playback.Rate = rate
		return nil
	})
}

// Advance moves the current position by deltaTime*rate. A negative rate moves
// backwards, while an explicit positive delta with a paused engine is ignored.
// Accepting the resulting state is an atomic event, so concurrent advancers
// cannot lose an increment.
func (e *Engine) Advance(deltaTime float64) error {
	return e.update(func(s *engineState) error {
		if !finiteNumber(deltaTime) {
			return ErrInvalidPlayback
		}
		if !s.playback.Playing {
			return nil
		}
		next := s.playback.Position + deltaTime*s.playback.Rate
		if !finiteNumber(next) || next < 0 {
			return ErrInvalidPlayback
		}
		s.playback.Position = next
		return nil
	})
}

// Seek jumps to an explicit non-negative position. Equality between a sequence
// of positive advances and Seek is guaranteed because Sample is a pure
// function of logical time.
func (e *Engine) Seek(position float64) error {
	return e.update(func(s *engineState) error {
		if !finiteNumber(position) || position < 0 {
			return ErrInvalidPlayback
		}
		s.playback.Position = position
		return nil
	})
}

// CommitPosition advances the durable recovery watermark to the current
// position. An explicit rewind does not lower an existing checkpoint.
func (e *Engine) CommitPosition() (Playback, uint64) {
	var result Playback
	var revision uint64
	_ = e.update(func(s *engineState) error {
		if s.playback.Position > s.playback.CommittedPosition {
			s.playback.CommittedPosition = s.playback.Position
		}
		result = s.playback
		revision = s.revision
		return nil
	})
	return result, revision
}

// ReplaceTimeline validates the complete replacement before it becomes
// visible. On error the existing snapshot remains the only active state.
func (e *Engine) ReplaceTimeline(t Timeline) error {
	next := cloneTimeline(t)
	return e.update(func(s *engineState) error {
		if err := ValidateTimeline(next); err != nil {
			return err
		}
		s.timeline = next
		return nil
	})
}

func (e *Engine) UpsertChannel(channel Channel) error {
	channel = cloneTimeline(Timeline{Channels: []Channel{channel}}).Channels[0]
	return e.update(func(s *engineState) error {
		for i := range s.timeline.Channels {
			if s.timeline.Channels[i].ID == channel.ID {
				s.timeline.Channels[i] = channel
				return ValidateTimeline(s.timeline)
			}
		}
		s.timeline.Channels = append(s.timeline.Channels, channel)
		return ValidateTimeline(s.timeline)
	})
}

func (e *Engine) UpsertTrack(channelID string, track Track) error {
	return e.update(func(s *engineState) error {
		ch, ok := findChannel(&s.timeline, channelID)
		if !ok {
			return ErrUnknownChannel
		}
		track = cloneTimeline(Timeline{Channels: []Channel{{Tracks: []Track{track}}}}).Channels[0].Tracks[0]
		for i := range ch.Tracks {
			if ch.Tracks[i].ID == track.ID {
				ch.Tracks[i] = track
				return ValidateTimeline(s.timeline)
			}
		}
		ch.Tracks = append(ch.Tracks, track)
		return ValidateTimeline(s.timeline)
	})
}

func (e *Engine) RemoveTrack(channelID, trackID string) error {
	return e.update(func(s *engineState) error {
		ch, ok := findChannel(&s.timeline, channelID)
		if !ok {
			return ErrUnknownChannel
		}
		for i, tr := range ch.Tracks {
			if tr.ID == trackID {
				ch.Tracks = append(ch.Tracks[:i], ch.Tracks[i+1:]...)
				return nil
			}
		}
		return ErrUnknownTrack
	})
}

func (e *Engine) SetTrackWeight(channelID, trackID string, weight float64) error {
	return e.update(func(s *engineState) error {
		_, tr, ok := findTrack(&s.timeline, channelID, trackID)
		if !ok {
			return ErrUnknownTrack
		}
		tr.Weight = weight
		return ValidateTimeline(s.timeline)
	})
}

func (e *Engine) UpsertClip(channelID, trackID string, clip Clip) error {
	return e.update(func(s *engineState) error {
		_, tr, ok := findTrack(&s.timeline, channelID, trackID)
		if !ok {
			return ErrUnknownTrack
		}
		clip.Keyframes = append([]Keyframe(nil), clip.Keyframes...)
		for i := range tr.Clips {
			if tr.Clips[i].ID == clip.ID {
				tr.Clips[i] = clip
				return ValidateTimeline(s.timeline)
			}
		}
		tr.Clips = append(tr.Clips, clip)
		return ValidateTimeline(s.timeline)
	})
}

func (e *Engine) RemoveClip(channelID, trackID, clipID string) error {
	return e.update(func(s *engineState) error {
		_, tr, ok := findTrack(&s.timeline, channelID, trackID)
		if !ok {
			return ErrUnknownTrack
		}
		for i, clip := range tr.Clips {
			if clip.ID == clipID {
				tr.Clips = append(tr.Clips[:i], tr.Clips[i+1:]...)
				return nil
			}
		}
		return ErrUnknownClip
	})
}

func (e *Engine) SetClipTransitions(channelID, trackID, clipID string, in, out float64) error {
	return e.update(func(s *engineState) error {
		_, tr, ok := findTrack(&s.timeline, channelID, trackID)
		if !ok {
			return ErrUnknownTrack
		}
		for i := range tr.Clips {
			if tr.Clips[i].ID == clipID {
				tr.Clips[i].TransitionIn = in
				tr.Clips[i].TransitionOut = out
				return ValidateTimeline(s.timeline)
			}
		}
		return ErrUnknownClip
	})
}

func (e *Engine) SetClipKeyframes(channelID, trackID, clipID string, keyframes []Keyframe) error {
	return e.update(func(s *engineState) error {
		_, tr, ok := findTrack(&s.timeline, channelID, trackID)
		if !ok {
			return ErrUnknownTrack
		}
		for i := range tr.Clips {
			if tr.Clips[i].ID == clipID {
				tr.Clips[i].Keyframes = append([]Keyframe(nil), keyframes...)
				return ValidateTimeline(s.timeline)
			}
		}
		return ErrUnknownClip
	})
}

func findChannel(t *Timeline, id string) (*Channel, bool) {
	for i := range t.Channels {
		if t.Channels[i].ID == id {
			return &t.Channels[i], true
		}
	}
	return nil, false
}

func findTrack(t *Timeline, channelID, trackID string) (*Channel, *Track, bool) {
	ch, ok := findChannel(t, channelID)
	if !ok {
		return nil, nil, false
	}
	for i := range ch.Tracks {
		if ch.Tracks[i].ID == trackID {
			return ch, &ch.Tracks[i], true
		}
	}
	return nil, nil, false
}

func (e *Engine) snapshot() *engineState {
	s := e.state.Load()
	if s == nil {
		return &engineState{playback: Playback{Rate: 1}}
	}
	return s
}

func (e *Engine) update(mutate func(*engineState) error) error {
	for {
		current := e.state.Load()
		if current == nil {
			initial := &engineState{playback: Playback{Rate: 1}}
			if !e.state.CompareAndSwap(nil, initial) {
				continue
			}
			current = initial
		}
		next := &engineState{
			timeline: cloneTimeline(current.timeline),
			playback: current.playback,
			revision: current.revision + 1,
		}
		if err := mutate(next); err != nil {
			return err
		}
		if e.state.CompareAndSwap(current, next) {
			return nil
		}
	}
}

func cloneTimeline(t Timeline) Timeline {
	out := Timeline{Channels: make([]Channel, len(t.Channels))}
	for i, ch := range t.Channels {
		copyChannel := Channel{ID: ch.ID, BlendMode: ch.BlendMode, Tracks: make([]Track, len(ch.Tracks))}
		for j, tr := range ch.Tracks {
			copyTrack := Track{
				ID:     tr.ID,
				Weight: tr.Weight,
				Easing: tr.Easing,
				Muted:  tr.Muted,
				Clips:  make([]Clip, len(tr.Clips)),
			}
			for k, clip := range tr.Clips {
				copyTrack.Clips[k] = clip
				copyTrack.Clips[k].Keyframes = append([]Keyframe(nil), clip.Keyframes...)
			}
			copyChannel.Tracks[j] = copyTrack
		}
		out.Channels[i] = copyChannel
	}
	return out
}
