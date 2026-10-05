package motion

import "testing"

func TestEasingEndpointsAndClamping(t *testing.T) {
	ids := []EasingID{EasingLinear, EasingEaseIn, EasingEaseOut, EasingEaseInOut}
	for _, id := range ids {
		if got := easingProgress(id, -1); got != 0 {
			t.Fatalf("%s before range = %v", id, got)
		}
		if got := easingProgress(id, 0); got != 0 {
			t.Fatalf("%s at zero = %v", id, got)
		}
		if got := easingProgress(id, 1); got != 1 {
			t.Fatalf("%s at one = %v", id, got)
		}
		if got := easingProgress(id, 2); got != 1 {
			t.Fatalf("%s after range = %v", id, got)
		}
		previous := 0.0
		for i := 1; i <= 10; i++ {
			p := float64(i) / 10
			got := easingProgress(id, p)
			if got < previous {
				t.Fatalf("%s decreased from %v to %v", id, previous, got)
			}
			previous = got
		}
	}
	if !isValidEasing("") || !isValidEasing(EasingLinear) || isValidEasing("bounce") {
		t.Fatal("unexpected easing validity")
	}
}

func TestClipEasingOverridesTrackEasing(t *testing.T) {
	linear := linearClip("linear", 0, 1, 0, 1)
	eased := linearClip("eased", 0, 1, 0, 1)
	eased.Easing = EasingEaseIn
	timeline := Timeline{Channels: []Channel{{ID: "x", Tracks: []Track{
		{ID: "linear", Weight: 1, Clips: []Clip{linear}},
		{ID: "eased", Weight: 1, Easing: EasingEaseOut, Clips: []Clip{eased}},
	}}}}
	engine, err := NewEngine(timeline)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := engine.Sample(0.5)["x"], (0.5+0.125)/2; got != want {
		t.Fatalf("mixed easing sample = %v, want %v", got, want)
	}
}
