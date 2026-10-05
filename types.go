package motion

type Keyframe struct {
	Time  float64 `json:"time"`
	Value float64 `json:"value"`
}

type Clip struct {
	ID            string     `json:"id"`
	Start         float64    `json:"start"`
	Duration      float64    `json:"duration"`
	TransitionIn  float64    `json:"transition_in"`
	TransitionOut float64    `json:"transition_out"`
	Easing        EasingID   `json:"easing,omitempty"`
	Keyframes     []Keyframe `json:"keyframes"`
}

type Track struct {
	ID     string   `json:"id"`
	Weight float64  `json:"weight"`
	Easing EasingID `json:"easing,omitempty"`
	Clips  []Clip   `json:"clips"`
	Muted  bool     `json:"muted,omitempty"`
}

type Channel struct {
	ID        string  `json:"id"`
	BlendMode string  `json:"blend_mode,omitempty"`
	Tracks    []Track `json:"tracks"`
}

type Timeline struct {
	Channels []Channel `json:"channels"`
}

type Playback struct {
	Position          float64 `json:"position"`
	CommittedPosition float64 `json:"committed_position"`
	Rate              float64 `json:"rate"`
	Playing           bool    `json:"playing"`
}

type engineState struct {
	timeline Timeline
	playback Playback
	revision uint64
}
