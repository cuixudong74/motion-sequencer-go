package motion

import (
	"errors"
	"testing"
)

func TestExitTransitionsHaveDefinedTargets(t *testing.T) {
	oldClip := constantClip("old", 0, 10, 10)
	coveredClip := constantClip("covered", 2, 4, 20)
	coveredClip.TransitionOut = 2
	timeline := Timeline{Channels: []Channel{{ID: "x", Tracks: []Track{{
		ID:     "t",
		Weight: 1,
		Clips:  []Clip{oldClip, coveredClip},
	}}}}}
	engine, err := NewEngine(timeline)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := engine.Sample(5)["x"], 15.0; got != want {
		t.Fatalf("covered exit midpoint = %v, want %v", got, want)
	}
	if got, want := engine.Sample(6)["x"], 10.0; got != want {
		t.Fatalf("covered exit end = %v, want %v", got, want)
	}

	fadeClip := constantClip("fade", 0, 4, 20)
	fadeClip.TransitionOut = 2
	fade, err := NewEngine(Timeline{Channels: []Channel{{ID: "x", Tracks: []Track{{ID: "t", Weight: 1, Clips: []Clip{fadeClip}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := fade.Sample(3)["x"], 10.0; got != want {
		t.Fatalf("exit to zero midpoint = %v, want %v", got, want)
	}

	partialOld := constantClip("old", 0, 5, 10)
	partialClip := constantClip("partial", 2, 4, 20)
	partialClip.TransitionOut = 2
	_, err = NewEngine(Timeline{Channels: []Channel{{ID: "x", Tracks: []Track{{ID: "t", Weight: 1, Clips: []Clip{partialOld, partialClip}}}}}})
	if !errors.Is(err, ErrInvalidTimeline) {
		t.Fatalf("partial coverage error = %v, want invalid timeline", err)
	}
}

func TestRejectsInvalidConfiguration(t *testing.T) {
	valid := twoTrackTimeline(BlendNormalized)
	cases := map[string]Timeline{}

	duplicateChannel := valid
	duplicateChannel.Channels = append(duplicateChannel.Channels, duplicateChannel.Channels[0])
	cases["duplicate channel"] = duplicateChannel

	negativeWeight := twoTrackTimeline(BlendNormalized)
	negativeWeight.Channels[0].Tracks[0].Weight = -1
	cases["negative weight"] = negativeWeight

	unknownEasing := twoTrackTimeline(BlendNormalized)
	unknownEasing.Channels[0].Tracks[0].Easing = "bounce"
	cases["unknown easing"] = unknownEasing

	for name, timeline := range cases {
		if _, err := NewEngine(timeline); !errors.Is(err, ErrInvalidTimeline) {
			t.Fatalf("%s: error = %v, want invalid timeline", name, err)
		}
	}
}

func TestClipBoundaryHandsOffWithoutGap(t *testing.T) {
	first := constantClip("first", 0, 2, 10)
	second := constantClip("second", 2, 2, 30)
	second.TransitionIn = 2
	engine, err := NewEngine(Timeline{Channels: []Channel{{ID: "x", Tracks: []Track{{ID: "t", Weight: 1, Clips: []Clip{first, second}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := engine.Sample(2)["x"]; got != 10 {
		t.Fatalf("boundary start = %v, want old value 10", got)
	}
	if got, want := engine.Sample(3)["x"], 20.0; got != want {
		t.Fatalf("boundary handoff midpoint = %v, want %v", got, want)
	}
	if got, want := engine.Sample(4)["x"], 30.0; got != want {
		t.Fatalf("handoff end = %v, want %v", got, want)
	}
}
