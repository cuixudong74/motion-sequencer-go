package motion

import (
	"math"
	"sort"
)

const (
	BlendNormalized = "normalized"
	BlendAdditive   = "additive"
)

type trackSample struct {
	value float64
	alpha float64
}

// ValidateTimeline checks a complete, immutable timeline description.
// Timelines are copied before every committed change, so a validated
// timeline never contains NaN, Inf, duplicate IDs or unsupported options.
func ValidateTimeline(t Timeline) error {
	seenChannels := make(map[string]struct{}, len(t.Channels))
	for _, ch := range t.Channels {
		if ch.ID == "" {
			return ErrInvalidTimeline
		}
		if _, exists := seenChannels[ch.ID]; exists {
			return ErrInvalidTimeline
		}
		seenChannels[ch.ID] = struct{}{}
		if ch.BlendMode != "" && ch.BlendMode != BlendNormalized && ch.BlendMode != BlendAdditive {
			return ErrInvalidTimeline
		}

		seenTracks := make(map[string]struct{}, len(ch.Tracks))
		for _, tr := range ch.Tracks {
			if tr.ID == "" {
				return ErrInvalidTimeline
			}
			if _, exists := seenTracks[tr.ID]; exists {
				return ErrInvalidTimeline
			}
			seenTracks[tr.ID] = struct{}{}
			if !finiteNumber(tr.Weight) || tr.Weight < 0 {
				return ErrInvalidTimeline
			}
			if !isValidEasing(tr.Easing) {
				return ErrInvalidTimeline
			}
			if err := validateClips(tr.Clips); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateClips(clips []Clip) error {
	seenClips := make(map[string]struct{}, len(clips))
	starts := make(map[float64]struct{}, len(clips))
	for _, clip := range clips {
		if clip.ID == "" {
			return ErrInvalidTimeline
		}
		if _, exists := seenClips[clip.ID]; exists {
			return ErrInvalidTimeline
		}
		seenClips[clip.ID] = struct{}{}
		if !finiteNumber(clip.Start) || !finiteNumber(clip.Duration) || clip.Duration <= 0 {
			return ErrInvalidTimeline
		}
		if _, exists := starts[clip.Start]; exists {
			return ErrInvalidTimeline
		}
		starts[clip.Start] = struct{}{}
		if !finiteNumber(clip.TransitionIn) || !finiteNumber(clip.TransitionOut) {
			return ErrInvalidTimeline
		}
		if clip.TransitionIn < 0 || clip.TransitionOut < 0 ||
			clip.TransitionIn > clip.Duration || clip.TransitionOut > clip.Duration ||
			clip.TransitionIn+clip.TransitionOut > clip.Duration {
			return ErrInvalidTimeline
		}
		if !isValidEasing(clip.Easing) {
			return ErrInvalidTimeline
		}
		if len(clip.Keyframes) == 0 {
			return ErrInvalidTimeline
		}

		last := math.Inf(-1)
		for _, key := range clip.Keyframes {
			if !finiteNumber(key.Time) || !finiteNumber(key.Value) || key.Time < 0 || key.Time > clip.Duration || key.Time <= last {
				return ErrInvalidTimeline
			}
			last = key.Time
		}
	}

	ordered := append([]Clip(nil), clips...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Start < ordered[j].Start })
	for i, clip := range ordered {
		if clip.TransitionOut == 0 {
			continue
		}
		outStart := clip.Start + clip.Duration - clip.TransitionOut
		end := clip.Start + clip.Duration
		covered := false
		overlap := false
		for j := 0; j < i; j++ {
			older := ordered[j]
			olderEnd := older.Start + older.Duration
			if olderEnd <= outStart || older.Start >= end {
				continue
			}
			overlap = true
			if older.Start <= outStart && olderEnd >= end {
				covered = true
			}
		}
		if overlap && !covered {
			return ErrInvalidTimeline
		}
	}
	return nil
}

func finiteNumber(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

// sampleTimeline evaluates a validated timeline at an explicit logical time.
// The function is pure: it never mutates its input and does not read engine
// playback state. Therefore repeated calls with the same time and data return
// identical values regardless of previous sample times or call frequency.
func sampleTimeline(t Timeline, at float64) map[string]float64 {
	out := make(map[string]float64, len(t.Channels))
	if !finiteNumber(at) {
		for _, ch := range t.Channels {
			out[ch.ID] = 0
		}
		return out
	}

	for _, ch := range t.Channels {
		samples := make([]trackSample, 0, len(ch.Tracks))
		weights := make([]float64, 0, len(ch.Tracks))
		for _, tr := range ch.Tracks {
			if tr.Muted || tr.Weight == 0 {
				continue
			}
			if sample, ok := sampleTrack(tr, at); ok {
				samples = append(samples, sample)
				weights = append(weights, tr.Weight)
			}
		}
		out[ch.ID] = blendChannelSamples(ch.BlendMode, samples, weights)
	}
	return out
}

func blendChannelSamples(mode string, samples []trackSample, weights []float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	if mode == BlendAdditive {
		sum := 0.0
		for i, sample := range samples {
			sum += sample.value * weights[i]
		}
		return sum
	}

	weightedSum := 0.0
	weightTotal := 0.0
	for i, sample := range samples {
		w := weights[i]
		weightedSum += sample.value * w
		weightTotal += w
	}
	if weightTotal == 0 {
		return 0
	}
	return weightedSum / weightTotal
}

func sampleTrack(tr Track, at float64) (trackSample, bool) {
	clips := append([]Clip(nil), tr.Clips...)
	sort.Slice(clips, func(i, j int) bool { return clips[i].Start < clips[j].Start })

	// Newer clips have priority. Find the newest clip whose content interval
	// contains the requested time. Intervals are half-open [start,end), so a
	// boundary has exactly one current owner.
	for i := len(clips) - 1; i >= 0; i-- {
		clip := clips[i]
		if at >= clip.Start && at <= clip.Start+clip.Duration {
			return evaluateClip(clips, i, at, tr.Easing, false), true
		}
	}
	return trackSample{}, false
}

// evaluateClip evaluates clips[:index+1]. Older clips are only used as
// transition sources/targets. Entry sources are frozen at the entering clip's
// start, which is the property that makes interruption independent of the
// order or frequency in which the transition was sampled.
func evaluateClip(clips []Clip, index int, at float64, trackEasing EasingID, inclusiveEnd bool) trackSample {
	clip := clips[index]
	local := clipValue(clip, at-clip.Start, trackEasing)
	easing := effectiveEasing(clip.Easing, trackEasing)

	inEnd := clip.Start + clip.TransitionIn
	outStart := clip.Start + clip.Duration - clip.TransitionOut
	if at >= clip.Start && at < inEnd && clip.TransitionIn > 0 {
		source := evaluateOlderAt(clips, index, clip.Start, trackEasing)
		p := easingProgress(easing, (at-clip.Start)/clip.TransitionIn)
		return blendSamples(source, trackSample{value: local, alpha: 1}, p)
	}
	if at >= outStart && (inclusiveEnd || at <= clip.Start+clip.Duration) && clip.TransitionOut > 0 {
		underlying := evaluateOlderAt(clips, index, at, trackEasing)
		p := easingProgress(easing, (at-outStart)/clip.TransitionOut)
		return blendSamples(trackSample{value: local, alpha: 1}, underlying, p)
	}
	return trackSample{value: local, alpha: 1}
}

func evaluateOlderAt(clips []Clip, newerIndex int, at float64, trackEasing EasingID) trackSample {
	// A clip ending exactly at the boundary is included through its limiting
	// value, so an entering clip starts from the prior value without a jump.
	for i := newerIndex - 1; i >= 0; i-- {
		clip := clips[i]
		end := clip.Start + clip.Duration
		if at >= clip.Start && (at < end || at == end) {
			return evaluateClip(clips, i, at, trackEasing, true)
		}
	}
	return trackSample{}
}

func blendSamples(from, to trackSample, progress float64) trackSample {
	return trackSample{
		value: (1-progress)*from.value + progress*to.value,
		alpha: (1-progress)*from.alpha + progress*to.alpha,
	}
}

func effectiveEasing(clip, track EasingID) EasingID {
	if clip != "" {
		return clip
	}
	if track != "" {
		return track
	}
	return EasingLinear
}

func clipValue(clip Clip, local float64, trackEasing EasingID) float64 {
	if local <= clip.Keyframes[0].Time {
		return clip.Keyframes[0].Value
	}
	last := clip.Keyframes[len(clip.Keyframes)-1]
	if local >= last.Time {
		return last.Value
	}
	for i := 1; i < len(clip.Keyframes); i++ {
		right := clip.Keyframes[i]
		if local <= right.Time {
			left := clip.Keyframes[i-1]
			span := right.Time - left.Time
			p := 0.0
			if span > 0 {
				p = (local - left.Time) / span
			}
			p = easingProgress(effectiveEasing(clip.Easing, trackEasing), p)
			return left.Value + (right.Value-left.Value)*p
		}
	}
	return last.Value
}
