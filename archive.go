package motion

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	ArchiveFormat         = "motion-sequencer-archive"
	CurrentArchiveVersion = 1
	// OldArchiveVersion is accepted and migrated to version 1.
	OldArchiveVersion = 0
)

// Archive is the versioned persistence envelope.
type Archive struct {
	Format   string        `json:"format,omitempty"`
	Version  int           `json:"version"`
	Timeline Timeline      `json:"timeline"`
	Playback PlaybackState `json:"playback"`
}

// SaveArchive writes the complete timeline and the committed playback state.
func (e *Engine) SaveArchive(w io.Writer) error {
	if w == nil {
		return &ValidationError{Operation: "save archive", Reason: "writer is nil"}
	}
	snap := e.currentSnapshot()
	data, err := encodeArchive(Archive{
		Format:   ArchiveFormat,
		Version:  CurrentArchiveVersion,
		Timeline: *cloneTimeline(snap.timeline),
		Playback: snap.playback,
	})
	if err != nil {
		return err
	}
	if err := writeAll(w, data); err != nil {
		return err
	}
	return nil
}

// LoadArchive decodes and validates an archive before publishing anything.
// Unknown future versions and invalid archives leave the current engine state
// untouched.
func (e *Engine) LoadArchive(r io.Reader) error {
	if r == nil {
		return &ValidationError{Operation: "load archive", Reason: "reader is nil"}
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnsupportedArchiveFormat, err)
	}
	var envelope struct {
		Format  string `json:"format"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("%w: %v", ErrUnsupportedArchiveFormat, err)
	}
	if envelope.Version > CurrentArchiveVersion {
		return fmt.Errorf("%w: %d", ErrUnsupportedArchiveVersion, envelope.Version)
	}

	var archive Archive
	decoder := json.NewDecoder(bytes.NewReader(raw))
	// Current files must be exact. Version 0 files may carry fields from an
	// older implementation; known data is migrated after decoding.
	if envelope.Version != OldArchiveVersion {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(&archive); err != nil {
		return fmt.Errorf("%w: %v", ErrUnsupportedArchiveFormat, err)
	}
	if err := rejectTrailingJSON(decoder); err != nil {
		return err
	}
	if err := migrateAndValidateArchive(&archive); err != nil {
		return err
	}
	copied := cloneTimeline(&archive.Timeline)

	e.mu.Lock()
	defer e.mu.Unlock()
	e.state.Store(&engineSnapshot{timeline: copied, playback: archive.Playback})
	return nil
}

func rejectTrailingJSON(decoder *json.Decoder) error {
	if err := decoder.Decode(&struct{}{}); err == nil {
		return fmt.Errorf("%w: trailing data after archive", ErrUnsupportedArchiveFormat)
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: %v", ErrUnsupportedArchiveFormat, err)
	}
	return nil
}

func migrateAndValidateArchive(archive *Archive) error {
	if archive.Version < OldArchiveVersion {
		return fmt.Errorf("%w: %d", ErrUnsupportedArchiveVersion, archive.Version)
	}
	if archive.Version > CurrentArchiveVersion {
		return fmt.Errorf("%w: %d", ErrUnsupportedArchiveVersion, archive.Version)
	}
	if archive.Format != "" && archive.Format != ArchiveFormat {
		return fmt.Errorf("%w: %q", ErrUnsupportedArchiveFormat, archive.Format)
	}
	if err := validateTimeline(&archive.Timeline); err != nil {
		return err
	}

	// Version 0 was written before the envelope carried an explicit format and
	// before default playback rate was required.
	if archive.Version == OldArchiveVersion {
		archive.Format = ArchiveFormat
		if archive.Playback.Rate == 0 {
			archive.Playback.Rate = 1
		}
		if archive.Playback.Epoch == 0 {
			archive.Playback.Epoch = 1
		}
	}
	if err := validatePlayback(archive.Playback); err != nil {
		return err
	}
	archive.Version = CurrentArchiveVersion
	archive.Format = ArchiveFormat
	return nil
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		if n > len(data) {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func encodeArchive(a Archive) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(a); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// SaveArchiveFile atomically writes an archive: temporary file, fsync, then
// rename in the same directory.
func (e *Engine) SaveArchiveFile(path string) error {
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, ".motion-archive-*")
	if err != nil {
		return err
	}
	tmpName := file.Name()
	removeOnError := true
	defer func() {
		if removeOnError {
			_ = os.Remove(tmpName)
		}
	}()
	if err := e.SaveArchive(file); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	removeOnError = false
	return nil
}

// LoadArchiveFile loads an atomically named archive.
func (e *Engine) LoadArchiveFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := e.LoadArchive(file); err != nil {
		if errors.Is(err, ErrUnsupportedArchiveVersion) {
			return err
		}
		return err
	}
	return nil
}
