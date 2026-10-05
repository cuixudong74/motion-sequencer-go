package motion

import "math"

// EasingID identifies a deterministic scalar easing function.
// An empty ID is equivalent to EasingLinear.
type EasingID string

const (
	EasingLinear    EasingID = "linear"
	EasingEaseIn    EasingID = "ease_in"
	EasingEaseOut   EasingID = "ease_out"
	EasingEaseInOut EasingID = "ease_in_out"
)

// easingProgress maps a transition progress to an eased progress.
// Values outside [0,1] are clamped. NaN is rejected by validation before
// sampling, but is normalized here as an extra defensive boundary.
func easingProgress(id EasingID, t float64) float64 {
	if math.IsNaN(t) {
		return 0
	}
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return 1
	}
	switch normalizeEasing(id) {
	case EasingEaseIn:
		return t * t * t
	case EasingEaseOut:
		u := 1 - t
		return 1 - u*u*u
	case EasingEaseInOut:
		if t < 0.5 {
			return 4 * t * t * t
		}
		u := -2*t + 2
		return 1 - u*u*u/2
	default:
		return t
	}
}

func normalizeEasing(id EasingID) EasingID {
	if id == "" {
		return EasingLinear
	}
	return id
}

func isValidEasing(id EasingID) bool {
	switch normalizeEasing(id) {
	case EasingLinear, EasingEaseIn, EasingEaseOut, EasingEaseInOut:
		return true
	default:
		return false
	}
}
