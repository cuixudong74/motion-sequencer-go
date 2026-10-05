package motion

import "errors"

var (
	ErrInvalidTimeline = errors.New("invalid timeline")
	ErrUnknownChannel  = errors.New("unknown channel")
	ErrUnknownTrack    = errors.New("unknown track")
	ErrUnknownClip     = errors.New("unknown clip")
	ErrUnknownVersion  = errors.New("unknown archive version")
	ErrInvalidArchive  = errors.New("invalid archive")
)
