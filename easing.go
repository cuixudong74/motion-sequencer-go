package motion

import "math"

func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 || math.IsNaN(x) {
		return 1
	}
	return x
}

// applyEasing maps a normalized time in [0,1] to an easing output.
//
// All inputs are clamped rather than extrapolated: a transition or keyframe
// never reads a value that depends on how often the timeline was sampled.
func applyEasing(kind Easing, x float64) float64 {
	x = clamp01(x)
	switch kind {
	case EaseLinear, "":
		return x
	case EaseInQuad:
		return x * x
	case EaseOutQuad:
		return 1 - (1-x)*(1-x)
	case EaseInOutCubic:
		if x < 0.5 {
			return 4 * x * x * x
		}
		y := -2*x + 2
		return 1 - y*y*y/2
	default:
		// Validation rejects unknown easings before sampling. This branch keeps
		// sampling total even if a Timeline is constructed through unsafe means.
		return x
	}
}
