package motion

import (
	"math"
	"sync"
	"testing"
)

func constantClip(id string, start, duration, transition, value float64) Clip {
	return Clip{
		ID:           id,
		Start:        start,
		Duration:     duration,
		TransitionIn: transition,
		Keyframes:    []Keyframe{{Offset: 0, Value: value}, {Offset: duration, Value: value}},
	}
}

func linearClip(id string, start, duration, transition, from, to float64) Clip {
	return Clip{
		ID:           id,
		Start:        start,
		Duration:     duration,
		TransitionIn: transition,
		Keyframes:    []Keyframe{{Offset: 0, Value: from}, {Offset: duration, Value: to}},
	}
}

func TestWeightedTrackMixingIsDeterministic(t *testing.T) {
	tl := &Timeline{
		Channels: map[string]Channel{
			"x": {
				ID: "x",
				Tracks: []Track{
					{ID: "a", Weight: 1, Clips: []Clip{constantClip("a", 0, 10, 0, 10)}},
					{ID: "b", Weight: 3, Clips: []Clip{constantClip("b", 0, 10, 0, 20)}},
				},
			},
		},
	}
	engine, err := NewEngine(tl)
	if err != nil {
		t.Fatal(err)
	}
	want := SampleResult{Time: 2, Value: 17.5, Active: true, Weight: 4}
	first, err := engine.SampleChannel("x", 2)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		got, err := engine.SampleChannel("x", 2)
		if err != nil {
			t.Fatal(err)
		}
		if got != first || got != want {
			t.Fatalf("sample %d = %+v, want %+v", i, got, want)
		}
	}
}

func TestKeyframeEasing(t *testing.T) {
	tl := &Timeline{Channels: map[string]Channel{"x": {ID: "x", Tracks: []Track{{
		ID: "t", Weight: 1,
		Clips: []Clip{{
			ID: "c", Start: 0, Duration: 1,
			Keyframes: []Keyframe{{Offset: 0, Value: 0, Easing: EaseInQuad}, {Offset: 1, Value: 1}},
		}},
	}}}}}
	engine, err := NewEngine(tl)
	if err != nil {
		t.Fatal(err)
	}
	got, err := engine.SampleChannel("x", 0.5)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got.Value-0.25) > 1e-15 {
		t.Fatalf("eased value = %v, want 0.25", got.Value)
	}
}

func TestInterruptedTransitionConvergesContinuously(t *testing.T) {
	clips := []Clip{
		linearClip("a", 0, 10, 0, 0, 10),
		constantClip("b", 5, 10, 5, 100),
		constantClip("c", 7.5, 10, 2.5, 200),
	}
	tl := &Timeline{Channels: map[string]Channel{"x": {ID: "x", Tracks: []Track{{ID: "t", Weight: 1, Clips: clips}}}}}
	engine, err := NewEngine(tl)
	if err != nil {
		t.Fatal(err)
	}

	before, err := engine.SampleChannel("x", 7.5-1e-12)
	if err != nil {
		t.Fatal(err)
	}
	at, err := engine.SampleChannel("x", 7.5)
	if err != nil {
		t.Fatal(err)
	}
	after, err := engine.SampleChannel("x", 7.5+1e-12)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(before.Value-at.Value) > 1e-9 || math.Abs(after.Value-at.Value) > 1e-9 {
		t.Fatalf("interrupt introduced a jump: before=%v at=%v after=%v", before.Value, at.Value, after.Value)
	}

	mid, err := engine.SampleChannel("x", 8.75)
	if err != nil {
		t.Fatal(err)
	}
	// Existing A->B transition is at 77.1875; interruption alpha is .5.
	if math.Abs(mid.Value-138.59375) > 1e-12 {
		t.Fatalf("mid interrupted transition = %v", mid.Value)
	}
	end, err := engine.SampleChannel("x", 10)
	if err != nil {
		t.Fatal(err)
	}
	if end.Value != 200 || !end.Active {
		t.Fatalf("transition did not converge: %+v", end)
	}
}

func TestTickPathEqualsDirectSeekForExactIntervals(t *testing.T) {
	tl := &Timeline{Channels: map[string]Channel{"x": {ID: "x", Tracks: []Track{{ID: "t", Weight: 1, Clips: []Clip{linearClip("a", 0, 4, 0, 0, 40)}}}}}}
	engine, err := NewEngine(tl)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.SetRate(2); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if _, err := engine.Tick(0.25); err != nil {
			t.Fatal(err)
		}
	}
	stepped, err := engine.SamplePlayback("x")
	if err != nil {
		t.Fatal(err)
	}
	direct, err := NewEngine(tl)
	if err != nil {
		t.Fatal(err)
	}
	if err := direct.Seek(4); err != nil {
		t.Fatal(err)
	}
	wanted, err := direct.SamplePlayback("x")
	if err != nil {
		t.Fatal(err)
	}
	if stepped != wanted {
		t.Fatalf("stepped=%+v direct=%+v", stepped, wanted)
	}
}

func TestRewindClampsAtZeroAndIdempotentTick(t *testing.T) {
	engine, err := NewEngine(&Timeline{Channels: map[string]Channel{}})
	if err != nil {
		t.Fatal(err)
	}
	first, err := engine.TickIdempotent(1, "token-1")
	if err != nil {
		t.Fatal(err)
	}
	again, err := engine.TickIdempotent(1, "token-1")
	if err != nil {
		t.Fatal(err)
	}
	if first.Position != 1 || again.Position != 1 || again.CommitSequence != first.CommitSequence {
		t.Fatalf("duplicate token advanced: first=%+v again=%+v", first, again)
	}
	if _, err := engine.Tick(-5); err != nil {
		t.Fatal(err)
	}
	if got := engine.Playback().Position; got != 0 {
		t.Fatalf("position after rewind = %v, want 0", got)
	}
}

func TestRuntimeUpdatePublishesCoherentSnapshot(t *testing.T) {
	engine, err := NewEngine(&Timeline{Channels: map[string]Channel{"x": {ID: "x", Tracks: []Track{{ID: "t", Weight: 1, Clips: []Clip{constantClip("a", 0, 10, 0, 1)}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	err = engine.UpdateTimeline(func(tl *Timeline) error {
		ch := tl.Channels["x"]
		ch.Tracks[0].Weight = 4
		ch.Tracks[0].Clips[0].TransitionIn = 2
		ch.Tracks[0].Clips[0].Keyframes = []Keyframe{{Offset: 0, Value: 1}, {Offset: 5, Value: 2}, {Offset: 10, Value: 2}}
		ch.Tracks = append(ch.Tracks, Track{ID: "new", Weight: 2})
		tl.Channels["x"] = ch
		tl.Channels["y"] = Channel{ID: "y", Tracks: []Track{{ID: "yt", Weight: 1}}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := engine.Timeline().Channels["x"].Tracks[0].Weight; got != 4 {
		t.Fatalf("weight = %v", got)
	}
	if _, ok := engine.Timeline().Channels["y"]; !ok {
		t.Fatal("new channel not published")
	}
}

func TestConcurrentSamplersAndWritersAreSafe(t *testing.T) {
	engine, err := NewEngine(&Timeline{Channels: map[string]Channel{"x": {ID: "x", Tracks: []Track{{ID: "t", Weight: 1, Clips: []Clip{constantClip("a", 0, 10, 0, 1)}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cursor, _ := NewCursor(engine, float64(i%3))
			for {
				select {
				case <-stop:
					return
				default:
					_, _ = cursor.Sample("x")
					_, _ = cursor.Tick(0.001)
					_ = engine.SampleAll(0)
				}
			}
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = engine.UpdateTimeline(func(tl *Timeline) error {
				ch := tl.Channels["x"]
				ch.Tracks[0].Weight = 1 + float64(i%3)
				tl.Channels["x"] = ch
				return nil
			})
		}
		close(stop)
	}()
	wg.Wait()
}

func TestRejectDuplicateClipStart(t *testing.T) {
	_, err := NewEngine(&Timeline{Channels: map[string]Channel{"x": {ID: "x", Tracks: []Track{{
		ID: "t", Weight: 1,
		Clips: []Clip{
			constantClip("a", 1, 2, 0, 1),
			constantClip("b", 1, 2, 0, 2),
		},
	}}}}})
	if err == nil {
		t.Fatal("expected duplicate start validation error")
	}
}

func TestTouchingClipTransitionCrossfadesAtBoundary(t *testing.T) {
	tl := &Timeline{Channels: map[string]Channel{"x": {
		ID: "x",
		Tracks: []Track{{
			ID:     "t",
			Weight: 1,
			Clips: []Clip{
				constantClip("a", 0, 2, 0, 10),
				constantClip("b", 2, 2, 2, 20),
			},
		}},
	}}}
	engine, err := NewEngine(tl)
	if err != nil {
		t.Fatal(err)
	}
	before, err := engine.SampleChannel("x", 2-1e-12)
	if err != nil {
		t.Fatal(err)
	}
	at, err := engine.SampleChannel("x", 2)
	if err != nil {
		t.Fatal(err)
	}
	after, err := engine.SampleChannel("x", 2+1e-12)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(before.Value-10) > 1e-12 || at.Value != 10 || math.Abs(after.Value-10) > 1e-9 {
		t.Fatalf("touching crossfade not continuous: before=%v at=%v after=%v", before, at, after)
	}
}
