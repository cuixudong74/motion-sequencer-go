package motion

import (
	"math"
	"sort"
)

func finiteNonNegative(x float64) bool {
	return !math.IsNaN(x) && !math.IsInf(x, 0) && x >= 0
}

func knownEasing(kind Easing) bool {
	switch kind {
	case "", EaseLinear, EaseInQuad, EaseOutQuad, EaseInOutCubic:
		return true
	default:
		return false
	}
}

// validateTimeline rejects values that would make a sample ill-defined.
func validateTimeline(tl *Timeline) error {
	if tl == nil {
		return &ValidationError{Operation: "validate timeline", Reason: "timeline is nil"}
	}
	for channelID, ch := range tl.Channels {
		if channelID == "" {
			return &ValidationError{Operation: "validate timeline", Reason: "empty channel id"}
		}
		if ch.ID != "" && ch.ID != channelID {
			return &ValidationError{Operation: "validate channel " + channelID, Reason: "channel map key and id disagree"}
		}
		trackIDs := make(map[string]struct{}, len(ch.Tracks))
		for _, tr := range ch.Tracks {
			if tr.ID == "" {
				return &ValidationError{Operation: "validate channel " + channelID, Reason: "empty track id"}
			}
			if _, exists := trackIDs[tr.ID]; exists {
				return &ValidationError{Operation: "validate track " + tr.ID, Reason: "duplicate track id"}
			}
			trackIDs[tr.ID] = struct{}{}
			if !knownEasing(tr.Easing) {
				return &ValidationError{Operation: "validate track " + tr.ID, Reason: "unknown easing"}
			}
			if math.IsNaN(tr.Weight) || math.IsInf(tr.Weight, 0) || tr.Weight < 0 {
				return &ValidationError{Operation: "validate track " + tr.ID, Reason: "weight must be a finite non-negative number"}
			}
			clipIDs := make(map[string]struct{}, len(tr.Clips))
			starts := make(map[float64]string, len(tr.Clips))
			for _, clip := range tr.Clips {
				if clip.ID == "" {
					return &ValidationError{Operation: "validate track " + tr.ID, Reason: "empty clip id"}
				}
				if _, exists := clipIDs[clip.ID]; exists {
					return &ValidationError{Operation: "validate clip " + clip.ID, Reason: "duplicate clip id"}
				}
				clipIDs[clip.ID] = struct{}{}
				if previousID, exists := starts[clip.Start]; exists {
					return &ValidationError{Operation: "validate track " + tr.ID, Reason: "clips " + previousID + " and " + clip.ID + " have the same start time"}
				}
				starts[clip.Start] = clip.ID
				if !finiteNonNegative(clip.Start) || !isPositiveFinite(clip.Duration) {
					return &ValidationError{Operation: "validate clip " + clip.ID, Reason: "start must be non-negative and duration positive"}
				}
				if clip.TransitionIn < 0 || clip.TransitionIn > clip.Duration || math.IsNaN(clip.TransitionIn) || math.IsInf(clip.TransitionIn, 0) {
					return &ValidationError{Operation: "validate clip " + clip.ID, Reason: "transition must be finite and in [0,duration]"}
				}
				if !knownEasing(clip.Easing) {
					return &ValidationError{Operation: "validate clip " + clip.ID, Reason: "unknown easing"}
				}
				var lastOffset float64
				for i, key := range clip.Keyframes {
					if key.Offset < 0 || key.Offset > clip.Duration || math.IsNaN(key.Offset) || math.IsInf(key.Offset, 0) {
						return &ValidationError{Operation: "validate keyframe", Reason: "offset must be finite and within clip duration"}
					}
					if math.IsNaN(key.Value) || math.IsInf(key.Value, 0) {
						return &ValidationError{Operation: "validate keyframe", Reason: "value must be finite"}
					}
					if !knownEasing(key.Easing) {
						return &ValidationError{Operation: "validate keyframe", Reason: "unknown easing"}
					}
					if i > 0 && key.Offset <= lastOffset {
						return &ValidationError{Operation: "validate clip " + clip.ID, Reason: "keyframes must be strictly increasing"}
					}
					lastOffset = key.Offset
				}
			}
		}
	}
	return nil
}

func isPositiveFinite(x float64) bool {
	return !math.IsNaN(x) && !math.IsInf(x, 0) && x > 0
}

func validatePlayback(state PlaybackState) error {
	if !finiteNonNegative(state.Position) {
		return &ValidationError{Operation: "validate playback", Reason: "position must be finite and non-negative"}
	}
	if math.IsNaN(state.Rate) || math.IsInf(state.Rate, 0) || state.Rate <= 0 {
		return &ValidationError{Operation: "validate playback", Reason: "rate must be finite and positive"}
	}
	return nil
}

// cloneTimeline returns a normalized deep copy. The public snapshot returned by
// Timeline is also copied, so callers cannot mutate data held by the engine.
func cloneTimeline(in *Timeline) *Timeline {
	if in == nil {
		return &Timeline{Channels: map[string]Channel{}}
	}
	out := &Timeline{
		Version:  in.Version,
		Channels: make(map[string]Channel, len(in.Channels)),
	}
	for id, ch := range in.Channels {
		copiedChannel := Channel{ID: id, Tracks: make([]Track, 0, len(ch.Tracks))}
		for _, tr := range ch.Tracks {
			copiedTrack := Track{
				ID:     tr.ID,
				Weight: tr.Weight,
				Easing: tr.Easing,
				Clips:  make([]Clip, 0, len(tr.Clips)),
			}
			for _, clip := range tr.Clips {
				copiedClip := Clip{
					ID:           clip.ID,
					Start:        clip.Start,
					Duration:     clip.Duration,
					TransitionIn: clip.TransitionIn,
					Easing:       clip.Easing,
					Keyframes:    make([]Keyframe, len(clip.Keyframes)),
				}
				copy(copiedClip.Keyframes, clip.Keyframes)
				copiedTrack.Clips = append(copiedTrack.Clips, copiedClip)
			}
			sort.SliceStable(copiedTrack.Clips, func(i, j int) bool {
				if copiedTrack.Clips[i].Start == copiedTrack.Clips[j].Start {
					return copiedTrack.Clips[i].ID < copiedTrack.Clips[j].ID
				}
				return copiedTrack.Clips[i].Start < copiedTrack.Clips[j].Start
			})
			copiedChannel.Tracks = append(copiedChannel.Tracks, copiedTrack)
		}
		out.Channels[id] = copiedChannel
	}
	return out
}
