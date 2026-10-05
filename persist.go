package motion

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// ArchiveVersion is the newest archive format produced by Save.
const ArchiveVersion = 2

type archiveEnvelope struct {
	Version *int `json:"version"`
}

type archiveV1 struct {
	Version  int         `json:"version"`
	Timeline *timelineV1 `json:"timeline,omitempty"`
}

type archiveV2 struct {
	Version  int      `json:"version"`
	Timeline Timeline `json:"timeline"`
	Playback Playback `json:"playback"`
	Revision uint64   `json:"revision"`
}

type timelineV1 struct {
	Channels []channelV1 `json:"channels"`
}

type channelV1 struct {
	ID     string    `json:"id"`
	Tracks []trackV1 `json:"tracks"`
}

type trackV1 struct {
	ID     string   `json:"id"`
	Weight float64  `json:"weight"`
	Clips  []clipV1 `json:"clips"`
}

type clipV1 struct {
	ID            string     `json:"id"`
	Start         float64    `json:"start"`
	Duration      float64    `json:"duration"`
	TransitionIn  float64    `json:"transition_in"`
	TransitionOut float64    `json:"transition_out"`
	Keyframes     []Keyframe `json:"keyframes"`
}

// Save writes the current timeline together with the latest committed
// playback checkpoint. Output is canonical JSON from fixed struct fields.
func (e *Engine) Save(w io.Writer) error {
	if w == nil {
		return ErrInvalidArchive
	}
	s := e.snapshot()
	data, err := json.MarshalIndent(archiveV2{
		Version:  ArchiveVersion,
		Timeline: s.timeline,
		Playback: s.playback,
		Revision: s.revision,
	}, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

// Load decodes and validates the complete archive before touching engine
// state. A malformed payload, unsupported version, or invalid timeline causes
// Load to return an error and leave the existing timeline/playback untouched.
func (e *Engine) Load(r io.Reader) error {
	if r == nil {
		return ErrInvalidArchive
	}
	payload, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	var head archiveEnvelope
	if err := json.Unmarshal(payload, &head); err != nil {
		return ErrInvalidArchive
	}

	var (
		timeline Timeline
		playback Playback
		revision uint64
	)
	switch {
	case head.Version == nil:
		var old timelineV1
		if err := strictDecode(payload, &old); err != nil {
			return err
		}
		timeline = old.toCurrent()
		playback = Playback{Rate: 1}
	case *head.Version == 1:
		var old archiveV1
		if err := strictDecode(payload, &old); err != nil {
			return err
		}
		if old.Timeline == nil {
			var direct timelineV1
			if err := strictDecode(payload, &direct); err != nil {
				return err
			}
			old.Timeline = &direct
		}
		timeline = old.Timeline.toCurrent()
		playback = Playback{Rate: 1}
	case *head.Version == ArchiveVersion:
		var current archiveV2
		if err := strictDecode(payload, &current); err != nil {
			return err
		}
		timeline = current.Timeline
		playback = current.Playback
		revision = current.Revision
	default:
		return ErrUnknownVersion
	}

	if err := ValidateTimeline(timeline); err != nil {
		return ErrInvalidArchive
	}
	if err := validateLoadedPlayback(playback); err != nil {
		return err
	}

	// Recovery starts at the latest committed position, discarding positions
	// that had not reached a durable commit before interruption.
	playback.Position = playback.CommittedPosition
	return e.update(func(s *engineState) error {
		s.timeline = timeline
		s.playback = playback
		s.revision = revision
		return nil
	})
}

func validateLoadedPlayback(p Playback) error {
	if !finiteNumber(p.Position) || !finiteNumber(p.CommittedPosition) || !finiteNumber(p.Rate) {
		return ErrInvalidArchive
	}
	if p.Position < 0 || p.CommittedPosition < 0 || p.Position < p.CommittedPosition {
		return ErrInvalidArchive
	}
	return nil
}

func strictDecode(payload []byte, v any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidArchive, err)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		return ErrInvalidArchive
	}
	return nil
}

func (v timelineV1) toCurrent() Timeline {
	out := Timeline{Channels: make([]Channel, len(v.Channels))}
	for i, ch := range v.Channels {
		currentChannel := Channel{ID: ch.ID, BlendMode: BlendNormalized, Tracks: make([]Track, len(ch.Tracks))}
		for j, tr := range ch.Tracks {
			currentTrack := Track{ID: tr.ID, Weight: tr.Weight, Easing: EasingLinear, Clips: make([]Clip, len(tr.Clips))}
			for k, clip := range tr.Clips {
				currentTrack.Clips[k] = Clip{
					ID:            clip.ID,
					Start:         clip.Start,
					Duration:      clip.Duration,
					TransitionIn:  clip.TransitionIn,
					TransitionOut: clip.TransitionOut,
					Easing:        EasingLinear,
					Keyframes:     append([]Keyframe(nil), clip.Keyframes...),
				}
			}
			currentChannel.Tracks[j] = currentTrack
		}
		out.Channels[i] = currentChannel
	}
	return out
}
