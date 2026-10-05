package motion

import (
	"errors"
	"math"
	"sync"
	"testing"
	"time"
)

func constantClip(id string, start, duration, value float64) Clip {
	return Clip{
		ID:        id,
		Start:     start,
		Duration:  duration,
		Keyframes: []Keyframe{{Time: 0, Value: value}, {Time: duration, Value: value}},
	}
}

func linearClip(id string, start, duration, from, to float64) Clip {
	return Clip{
		ID:        id,
		Start:     start,
		Duration:  duration,
		Keyframes: []Keyframe{{Time: 0, Value: from}, {Time: duration, Value: to}},
	}
}

func twoTrackTimeline(mode string) Timeline {
	return Timeline{Channels: []Channel{{
		ID:        "x",
		BlendMode: mode,
		Tracks: []Track{
			{ID: "a", Weight: 1, Clips: []Clip{constantClip("a1", 0, 10, 10)}},
			{ID: "b", Weight: 3, Clips: []Clip{constantClip("b1", 0, 10, 20)}},
		},
	}}}
}

func TestWeightedOverlayDeterministic(t *testing.T) {
	engine, err := NewEngine(twoTrackTimeline(BlendNormalized))
	if err != nil {
		t.Fatal(err)
	}
	got := engine.Sample(2)["x"]
	want := (10.0*1 + 20.0*3) / 4
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("weighted sample = %v, want %v", got, want)
	}
	for i := 0; i < 10; i++ {
		if again := engine.Sample(2)["x"]; again != got {
			t.Fatalf("sample changed from %v to %v", got, again)
		}
	}
}

func TestAdditiveOverlay(t *testing.T) {
	engine, err := NewEngine(twoTrackTimeline(BlendAdditive))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := engine.Sample(1)["x"], 70.0; got != want {
		t.Fatalf("additive sample = %v, want %v", got, want)
	}
}

func TestAdvanceMatchesSeekAndSupportsReverseRate(t *testing.T) {
	engine, _ := NewEngine(Timeline{Channels: []Channel{{ID: "x", Tracks: []Track{{ID: "t", Weight: 1, Clips: []Clip{linearClip("c", 0, 10, 0, 10)}}}}}})
	engine.Play()
	for i := 0; i < 5; i++ {
		if err := engine.Advance(0.5); err != nil {
			t.Fatal(err)
		}
	}
	stepped := engine.SampleNow()["x"]

	jumped, _ := NewEngine(engine.Timeline())
	if err := jumped.Seek(2.5); err != nil {
		t.Fatal(err)
	}
	if got := jumped.SampleNow()["x"]; got != stepped {
		t.Fatalf("stepped %v != seek %v", stepped, got)
	}

	if err := engine.SetRate(-1); err != nil {
		t.Fatal(err)
	}
	if err := engine.Advance(1); err != nil {
		t.Fatal(err)
	}
	if got, want := engine.Position(), 1.5; got != want {
		t.Fatalf("position after reverse = %v, want %v", got, want)
	}
}

func TestInterruptedTransitionConverges(t *testing.T) {
	timeline := Timeline{Channels: []Channel{{ID: "x", Tracks: []Track{{
		ID:     "t",
		Weight: 1,
		Clips: []Clip{
			constantClip("old", 0, 8, 10),
			func() Clip {
				c := constantClip("middle", 2, 6, 20)
				c.TransitionIn = 4
				return c
			}(),
			func() Clip {
				c := constantClip("new", 3, 5, 30)
				c.TransitionIn = 2
				return c
			}(),
		},
	}}}}}
	engine, err := NewEngine(timeline)
	if err != nil {
		t.Fatal(err)
	}

	// The new clip starts while middle is halfway through its own entry.
	// Its frozen source is therefore 12.5, not old=10 or middle=20.
	if got, want := engine.Sample(3)["x"], 12.5; got != want {
		t.Fatalf("interruption boundary = %v, want %v", got, want)
	}
	if got, want := engine.Sample(4)["x"], (12.5+30)/2; got != want {
		t.Fatalf("interrupted transition midpoint = %v, want %v", got, want)
	}
	if got, want := engine.Sample(5)["x"], 30.0; got != want {
		t.Fatalf("interrupted transition endpoint = %v, want %v", got, want)
	}

	// Direct sampling is independent of having visited the interruption
	// boundary or transition midpoint first.
	fresh, _ := NewEngine(timeline)
	for _, at := range []float64{3, 3.25, 4, 4.75, 5, 7, 8} {
		if got, want := fresh.Sample(at)["x"], engine.Sample(at)["x"]; got != want {
			t.Fatalf("at %v direct sample %v != stepped sample %v", at, got, want)
		}
	}
}

func TestRuntimeMutationIsAtomicAndValidated(t *testing.T) {
	engine, _ := NewEngine(twoTrackTimeline(BlendNormalized))
	beforeRevision := engine.Revision()
	before := engine.Sample(2)["x"]

	// A bad transition duration must not leave the track half-updated.
	if err := engine.SetClipTransitions("x", "a", "a1", 99, 0); !errors.Is(err, ErrInvalidTimeline) {
		t.Fatalf("bad transitions error = %v", err)
	}
	if engine.Revision() == beforeRevision+1 {
		t.Fatal("failed mutation unexpectedly advanced revision")
	}
	if got := engine.Sample(2)["x"]; got != before {
		t.Fatalf("sample changed after failed mutation: %v vs %v", got, before)
	}

	if err := engine.SetTrackWeight("x", "b", 1); err != nil {
		t.Fatal(err)
	}
	if got, want := engine.Sample(2)["x"], 15.0; got != want {
		t.Fatalf("weighted sample after change = %v, want %v", got, want)
	}
	if err := engine.SetClipKeyframes("x", "a", "a1", []Keyframe{{Time: 0, Value: 40}, {Time: 10, Value: 40}}); err != nil {
		t.Fatal(err)
	}
	if got, want := engine.Sample(2)["x"], 30.0; got != want {
		t.Fatalf("sample after keyframe change = %v, want %v", got, want)
	}
}

func TestConcurrentSamplersAndMutations(t *testing.T) {
	engine, _ := NewEngine(twoTrackTimeline(BlendNormalized))
	var writerWG, samplerWG sync.WaitGroup
	stop := make(chan struct{})

	writerWG.Add(1)
	go func() {
		defer writerWG.Done()
		weight := 1.0
		for {
			select {
			case <-stop:
				return
			default:
				weight++
				if weight > 4 {
					weight = 1
				}
				if err := engine.SetTrackWeight("x", "b", weight); err != nil {
					t.Errorf("set weight: %v", err)
					return
				}
			}
		}
	}()

	sampler := func(iterations int, slow bool) {
		defer samplerWG.Done()
		for at := 0; at < iterations; at++ {
			sample := engine.Sample(float64(at%10) / 10)
			value := sample["x"]
			if math.IsNaN(value) || math.IsInf(value, 0) {
				t.Errorf("invalid sample %v", sample)
				return
			}
			if slow && at%128 == 0 {
				time.Sleep(time.Millisecond)
			}
		}
	}
	samplerWG.Add(2)
	go sampler(5000, true)
	go sampler(10000, false)

	samplerWG.Wait()
	close(stop)
	writerWG.Wait()
}

func TestEntryFromInactiveFadesFromZero(t *testing.T) {
	clip := constantClip("c", 2, 4, 20)
	clip.TransitionIn = 2
	engine, _ := NewEngine(Timeline{Channels: []Channel{{ID: "x", Tracks: []Track{{ID: "t", Weight: 1, Clips: []Clip{clip}}}}}})
	if got := engine.Sample(1)["x"]; got != 0 {
		t.Fatalf("inactive value = %v", got)
	}
	if got := engine.Sample(2)["x"]; got != 0 {
		t.Fatalf("entry start = %v, want 0", got)
	}
	if got, want := engine.Sample(3)["x"], 10.0; got != want {
		t.Fatalf("entry midpoint = %v, want %v", got, want)
	}
	if got, want := engine.Sample(4)["x"], 20.0; got != want {
		t.Fatalf("entry end = %v, want %v", got, want)
	}
}

func TestSamplingFrequencyAndOrderIndependent(t *testing.T) {
	timeline := Timeline{Channels: []Channel{{ID: "x", Tracks: []Track{
		{ID: "a", Weight: 2, Clips: []Clip{constantClip("a", 0, 10, 10)}},
		{ID: "b", Weight: 1, Clips: []Clip{func() Clip {
			c := linearClip("b", 1, 8, 0, 40)
			c.TransitionIn = 2
			c.TransitionOut = 2
			return c
		}()}},
	}}}}
	reference, _ := NewEngine(timeline)
	times := []float64{0, 0.25, 1, 1.5, 2, 3, 7, 8.5, 9, 10}
	want := make(map[float64]float64, len(times))
	for _, at := range times {
		want[at] = reference.Sample(at)["x"]
	}

	coarse, _ := NewEngine(timeline)
	for at := 0.0; at <= 10; at += 2 {
		if got := coarse.Sample(at)["x"]; got != reference.Sample(at)["x"] {
			t.Fatalf("coarse at %v: %v want %v", at, got, reference.Sample(at)["x"])
		}
	}
	fine, _ := NewEngine(timeline)
	for at := 10.0; at >= 0; at -= 0.25 {
		if expected, exists := want[at]; exists && fine.Sample(at)["x"] != expected {
			t.Fatalf("reverse at %v: %v want %v", at, fine.Sample(at)["x"], expected)
		}
	}
	for _, at := range []float64{8.5, 0.25, 7, 1.5, 10} {
		if got := reference.Sample(at)["x"]; got != want[at] {
			t.Fatalf("repeat at %v: %v want %v", at, got, want[at])
		}
	}
}

func TestSlowSamplerDoesNotBlockMutation(t *testing.T) {
	engine, _ := NewEngine(twoTrackTimeline(BlendNormalized))
	held := engine.state.Load()
	done := make(chan error, 1)
	go func() {
		time.Sleep(30 * time.Millisecond)
		done <- engine.SetTrackWeight("x", "a", 7)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Millisecond):
		// A slow reader still references the old immutable snapshot, while the
		// mutator publishes a new one. Completion is checked again below.
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if engine.Revision() == held.revision {
		t.Fatal("mutation did not publish while a slow sampler held an old snapshot")
	}
}

func TestRuntimeTrackAndClipLifecycle(t *testing.T) {
	engine, _ := NewEngine(Timeline{Channels: []Channel{{ID: "x", Tracks: []Track{{ID: "a", Weight: 1, Clips: []Clip{constantClip("a1", 0, 10, 5)}}}}}})
	newTrack := Track{ID: "b", Weight: 2, Clips: []Clip{constantClip("b1", 0, 10, 8)}}
	if err := engine.UpsertTrack("x", newTrack); err != nil {
		t.Fatal(err)
	}
	if got, want := engine.Sample(1)["x"], (5+16)/3.0; got != want {
		t.Fatalf("sample after add track = %v, want %v", got, want)
	}
	newTrack.Weight = 1
	if err := engine.UpsertTrack("x", newTrack); err != nil {
		t.Fatal(err)
	}
	if got, want := engine.Sample(1)["x"], 6.5; got != want {
		t.Fatalf("sample after replace track = %v, want %v", got, want)
	}
	if err := engine.UpsertClip("x", "b", constantClip("b2", 10, 2, 20)); err != nil {
		t.Fatal(err)
	}
	if got, want := engine.Sample(11)["x"], 20.0; got != want {
		t.Fatalf("new clip sample = %v, want %v", got, want)
	}
	if err := engine.RemoveClip("x", "b", "b2"); err != nil {
		t.Fatal(err)
	}
	if err := engine.RemoveTrack("x", "b"); err != nil {
		t.Fatal(err)
	}
	if got, want := engine.Sample(1)["x"], 5.0; got != want {
		t.Fatalf("sample after removal = %v, want %v", got, want)
	}

	for name, run := range map[string]func() error{
		"upsert missing channel":   func() error { return engine.UpsertTrack("missing", newTrack) },
		"weight missing track":     func() error { return engine.SetTrackWeight("x", "missing", 1) },
		"clip missing track":       func() error { return engine.UpsertClip("x", "missing", constantClip("c", 0, 1, 0)) },
		"remove missing clip":      func() error { return engine.RemoveClip("x", "a", "missing") },
		"transitions missing clip": func() error { return engine.SetClipTransitions("x", "a", "missing", 0, 0) },
		"keyframes missing clip":   func() error { return engine.SetClipKeyframes("x", "a", "missing", nil) },
	} {
		if err := run(); err == nil {
			t.Fatalf("%s unexpectedly succeeded", name)
		}
	}
}

func TestInvalidPlaybackCommands(t *testing.T) {
	engine, _ := NewEngine(twoTrackTimeline(BlendNormalized))
	for name, run := range map[string]func() error{
		"nan seek":      func() error { return engine.Seek(math.NaN()) },
		"negative seek": func() error { return engine.Seek(-1) },
		"nan rate":      func() error { return engine.SetRate(math.NaN()) },
		"nan advance":   func() error { return engine.Advance(math.NaN()) },
	} {
		if err := run(); !errors.Is(err, ErrInvalidPlayback) {
			t.Fatalf("%s error = %v, want invalid playback", name, err)
		}
	}
	engine.Play()
	if err := engine.SetRate(1); err != nil {
		t.Fatal(err)
	}
	if err := engine.Advance(-10); !errors.Is(err, ErrInvalidPlayback) {
		t.Fatalf("advance before zero error = %v", err)
	}
	engine.Pause()
	before := engine.Position()
	if err := engine.Advance(1); err != nil {
		t.Fatal(err)
	}
	if engine.Position() != before {
		t.Fatal("paused advance changed position")
	}
}

func TestUpsertChannelCopiesInput(t *testing.T) {
	engine, _ := NewEngine(twoTrackTimeline(BlendNormalized))
	channel := Channel{ID: "y", BlendMode: BlendAdditive, Tracks: []Track{{ID: "t", Weight: 1, Clips: []Clip{constantClip("c", 0, 2, 3)}}}}
	if err := engine.UpsertChannel(channel); err != nil {
		t.Fatal(err)
	}
	channel.Tracks[0].Weight = 100
	channel.Tracks[0].Clips[0].Keyframes[0].Value = 999
	if got, want := engine.Sample(0)["y"], 3.0; got != want {
		t.Fatalf("mutated caller input leaked into snapshot: got %v want %v", got, want)
	}
}

func TestVariableRateAdvanceMatchesSeek(t *testing.T) {
	makeEngine := func() *Engine {
		e, _ := NewEngine(Timeline{Channels: []Channel{{ID: "x", Tracks: []Track{{ID: "t", Weight: 1, Clips: []Clip{linearClip("c", 0, 10, 0, 100)}}}}}})
		e.Play()
		return e
	}
	stepped := makeEngine()
	if err := stepped.SetRate(2); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if err := stepped.Advance(0.25); err != nil {
			t.Fatal(err)
		}
	}
	jumped := makeEngine()
	if err := jumped.Seek(2); err != nil {
		t.Fatal(err)
	}
	if got, want := stepped.SampleNow()["x"], jumped.SampleNow()["x"]; got != want {
		t.Fatalf("2x stepped sample %v != seek sample %v", got, want)
	}
	if got, want := stepped.Position(), 2.0; got != want {
		t.Fatalf("2x position = %v, want %v", got, want)
	}
}

func TestConcurrentAdvancesDoNotLoseIncrements(t *testing.T) {
	engine, _ := NewEngine(twoTrackTimeline(BlendNormalized))
	engine.Play()
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if err := engine.Advance(1); err != nil {
					t.Errorf("advance: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if got, want := engine.Position(), 400.0; got != want {
		t.Fatalf("position after concurrent advances = %v, want %v", got, want)
	}
}
