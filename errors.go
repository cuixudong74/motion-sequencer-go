package motion

import "errors"

var (
	ErrInvalidTimeline           = errors.New("motion: invalid timeline")
	ErrInvalidPlayback           = errors.New("motion: invalid playback state")
	ErrUnsupportedArchiveVersion = errors.New("motion: unsupported archive version")
	ErrUnsupportedArchiveFormat  = errors.New("motion: unsupported archive format")
	ErrChannelNotFound           = errors.New("motion: channel not found")
	ErrInactiveChannel           = errors.New("motion: channel is inactive")
)

// ValidationError describes a rejected timeline or playback change.
type ValidationError struct {
	Operation string
	Reason    string
}

func (e *ValidationError) Error() string { return e.Operation + ": " + e.Reason }
