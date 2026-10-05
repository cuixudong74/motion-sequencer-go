package motion

import "sort"

// sourceValue is the value and opacity produced by a time-addressable source.
// The source is safe to evaluate at any time; held is false after its last
// natural active interval so gaps reset a track to inactive.
type sourceValue struct {
	value float64
	// opacity is in [0,1]. It allows a transition to fade a track in instead
	// of treating every clip boundary as an instantaneous 0->1 cut.
	opacity float64
	held    bool
}

type motionSource interface {
	at(t float64) sourceValue
	naturalEnd() float64
}

type emptySource struct{}

func (emptySource) at(float64) sourceValue { return sourceValue{} }
func (emptySource) naturalEnd() float64    { return -1 }

type clipSource struct {
	clip        Clip
	trackEasing Easing
}

func (s clipSource) naturalEnd() float64 { return s.clip.End() }

func (s clipSource) at(t float64) sourceValue {
	c := s.clip
	if t < c.Start {
		return sourceValue{}
	}
	local := t - c.Start
	if local >= c.Duration {
		// Evaluate the terminal keyframe value. The held flag below still
		// represents the half-open natural active interval.
		local = c.Duration
	}
	return sourceValue{value: sampleClip(c, local, s.trackEasing), opacity: 1, held: t < c.End()}
}

// End returns the exclusive end time of a clip.
func (c Clip) End() float64 { return c.Start + c.Duration }

func (s transitionSource) naturalEnd() float64 { return s.in.clip.End() }

type transitionSource struct {
	in          clipSource
	old         motionSource
	start       float64
	duration    float64
	easing      Easing
	trackEasing Easing
}

func (s transitionSource) at(t float64) sourceValue {
	in := s.in.clip
	end := in.End()
	if t < s.start {
		return s.old.at(t)
	}
	if t == s.start {
		result := s.old.at(t)
		// A half-open source reports inactive at its exclusive end. When a new
		// clip starts exactly at that end, the transition nevertheless begins
		// from the well-defined endpoint value.
		if t == s.old.naturalEnd() {
			result.held = true
		}
		return result
	}
	if t >= end {
		// Once this transition is retained only as the predecessor of a later
		// transition, freeze at the incoming clip's well-defined end value.
		return sourceValue{value: sampleClip(in, in.Duration, s.trackEasing), opacity: 1}
	}

	fresh := s.in.at(t)
	if t >= s.start+s.duration || s.duration == 0 {
		return fresh
	}
	alpha := applyEasing(s.easing, (t-s.start)/s.duration)
	old := s.old.at(t)
	return sourceValue{
		value:   (1-alpha)*old.value + alpha*fresh.value,
		opacity: (1-alpha)*old.opacity + alpha*fresh.opacity,
		held:    true,
	}
}

func sampleChannel(ch Channel, t float64) SampleResult {
	var totalValue, totalWeight float64
	for _, tr := range ch.Tracks {
		trackResult := sampleTrack(tr, t)
		if !trackResult.Active {
			continue
		}
		w := tr.Weight * trackResult.Weight
		totalValue += w * trackResult.Value
		totalWeight += w
	}
	if totalWeight == 0 {
		return SampleResult{Time: t, Active: false}
	}
	return SampleResult{
		Time:   t,
		Value:  totalValue / totalWeight,
		Active: true,
		Weight: totalWeight,
	}
}

func sampleTrack(tr Track, t float64) SampleResult {
	clips := append([]Clip(nil), tr.Clips...)
	sort.SliceStable(clips, func(i, j int) bool {
		if clips[i].Start == clips[j].Start {
			return clips[i].ID < clips[j].ID
		}
		return clips[i].Start < clips[j].Start
	})

	var current motionSource
	for _, clip := range clips {
		if clip.Start > t {
			break
		}
		old := motionSource(emptySource{})
		// A predecessor participates through an overlap or at a touching end
		// boundary. It does not leak across a positive inactive gap.
		if current != nil && clip.Start <= current.naturalEnd() {
			old = current
		}
		transitionEase := clip.Easing
		if transitionEase == "" {
			transitionEase = tr.Easing
		}
		current = transitionSource{
			in:          clipSource{clip: clip, trackEasing: tr.Easing},
			old:         old,
			start:       clip.Start,
			duration:    clip.TransitionIn,
			easing:      transitionEase,
			trackEasing: tr.Easing,
		}
	}
	if current == nil {
		return SampleResult{Time: t, Active: false}
	}
	result := current.at(t)
	if !result.held || result.opacity == 0 {
		return SampleResult{Time: t, Active: false}
	}
	return SampleResult{
		Time:   t,
		Value:  result.value,
		Active: true,
		Weight: result.opacity,
	}
}

// sampleClip evaluates keyframes using clip-local time. The easing fallback is
// keyframe easing, then clip easing, then track easing, then linear.
func sampleClip(clip Clip, local float64, trackEasing Easing) float64 {
	keys := clip.Keyframes
	if len(keys) == 0 {
		return 0
	}
	if local <= keys[0].Offset {
		return keys[0].Value
	}
	last := keys[len(keys)-1]
	if local >= last.Offset {
		return last.Value
	}
	i := sort.Search(len(keys)-1, func(i int) bool { return keys[i+1].Offset >= local })
	a, b := keys[i], keys[i+1]
	span := b.Offset - a.Offset
	if span == 0 {
		return b.Value
	}
	easing := a.Easing
	if easing == "" {
		easing = clip.Easing
	}
	if easing == "" {
		easing = trackEasing
	}
	u := applyEasing(easing, (local-a.Offset)/span)
	return a.Value + (b.Value-a.Value)*u
}
