package motion

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestV2ArchiveRoundTripAndRecovery(t *testing.T) {
	engine, _ := NewEngine(twoTrackTimeline(BlendNormalized))
	engine.Play()
	if err := engine.Seek(9); err != nil {
		t.Fatal(err)
	}
	committed, _ := engine.CommitPosition()
	if committed.CommittedPosition != 9 {
		t.Fatalf("committed position = %v", committed.CommittedPosition)
	}
	if err := engine.Advance(1); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := engine.Save(&buf); err != nil {
		t.Fatal(err)
	}
	restored := &Engine{}
	if err := restored.Load(&buf); err != nil {
		t.Fatal(err)
	}
	if got := restored.Position(); got != 9 {
		t.Fatalf("restored position = %v, want committed 9", got)
	}
	if got := restored.PlaybackState().CommittedPosition; got != 9 {
		t.Fatalf("restored committed position = %v, want 9", got)
	}
	if got := restored.SampleNow()["x"]; got != engine.Sample(9)["x"] {
		t.Fatalf("restored sample %v != original sample at checkpoint %v", got, engine.Sample(9)["x"])
	}
}

func TestLoadsV1Archive(t *testing.T) {
	v1 := `{
	  "version": 1,
	  "timeline": {"channels":[{"id":"x","tracks":[{"id":"a","weight":2,"clips":[{"id":"c","start":0,"duration":2,"transition_in":0,"transition_out":0,"keyframes":[{"time":0,"value":4},{"time":2,"value":8}]}]}]}]}
	}`
	engine := &Engine{}
	if err := engine.Load(strings.NewReader(v1)); err != nil {
		t.Fatal(err)
	}
	if got, want := engine.Sample(1)["x"], 6.0; got != want {
		t.Fatalf("v1 sample = %v, want %v", got, want)
	}
	if got := engine.PlaybackState().Rate; got != 1 {
		t.Fatalf("default rate = %v", got)
	}
}

func TestLoadVersionlessArchive(t *testing.T) {
	v0 := `{"channels":[{"id":"x","tracks":[{"id":"a","weight":1,"clips":[{"id":"c","start":0,"duration":1,"transition_in":0,"transition_out":0,"keyframes":[{"time":0,"value":1},{"time":1,"value":1}]}]}]}]}`
	engine := &Engine{}
	if err := engine.Load(strings.NewReader(v0)); err != nil {
		t.Fatal(err)
	}
	if got, want := engine.Sample(0)["x"], 1.0; got != want {
		t.Fatalf("versionless sample = %v, want %v", got, want)
	}
}

func TestRejectsNewVersionAndDoesNotMutate(t *testing.T) {
	engine, _ := NewEngine(twoTrackTimeline(BlendNormalized))
	originalRevision := engine.Revision()
	originalSample := engine.Sample(1)["x"]

	err := engine.Load(strings.NewReader(`{"version":3,"timeline":{},"playback":{}}`))
	if !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("error = %v, want ErrUnknownVersion", err)
	}
	if engine.Revision() != originalRevision {
		t.Fatal("revision changed after rejected archive")
	}
	if got := engine.Sample(1)["x"]; got != originalSample {
		t.Fatalf("state changed after rejected archive: %v vs %v", got, originalSample)
	}
}

func TestInvalidArchiveDoesNotPartiallyLoad(t *testing.T) {
	engine, _ := NewEngine(twoTrackTimeline(BlendNormalized))
	originalRevision := engine.Revision()
	originalSample := engine.Sample(1)["x"]
	invalid := `{
	  "version": 2,
	  "timeline": {"channels":[{"id":"x","tracks":[{"id":"a","weight":1,"clips":[{"id":"bad","start":0,"duration":1,"keyframes":[{"time":2,"value":1}]}]}]}]},
	  "playback": {"position": 0, "committed_position": 0, "rate": 1, "playing": false},
	  "revision": 42
	}`
	if err := engine.Load(strings.NewReader(invalid)); !errors.Is(err, ErrInvalidArchive) {
		t.Fatalf("error = %v, want invalid archive", err)
	}
	if engine.Revision() != originalRevision {
		t.Fatal("revision changed after invalid load")
	}
	if got := engine.Sample(1)["x"]; got != originalSample {
		t.Fatalf("state partially changed: %v vs %v", got, originalSample)
	}
}

func TestRejectsUnknownArchiveFields(t *testing.T) {
	engine := &Engine{}
	err := engine.Load(strings.NewReader(`{"version":1,"timeline":{"channels":[]},"unexpected":true}`))
	if !errors.Is(err, ErrInvalidArchive) {
		t.Fatalf("error = %v, want invalid archive", err)
	}
}

func TestReplaceTimelineRejectsInvalidWithoutSideEffects(t *testing.T) {
	engine, _ := NewEngine(twoTrackTimeline(BlendNormalized))
	beforeRevision := engine.Revision()
	before := engine.Sample(1)["x"]
	invalid := twoTrackTimeline(BlendNormalized)
	invalid.Channels[0].Tracks[0].Clips[0].Duration = 0
	if err := engine.ReplaceTimeline(invalid); !errors.Is(err, ErrInvalidTimeline) {
		t.Fatalf("replace error = %v", err)
	}
	if engine.Revision() != beforeRevision {
		t.Fatal("invalid replacement changed revision")
	}
	if got := engine.Sample(1)["x"]; got != before {
		t.Fatalf("invalid replacement changed state: %v vs %v", got, before)
	}
}

func TestLoadRejectsInvalidPlayback(t *testing.T) {
	payload := `{
	  "version": 2,
	  "timeline": {"channels":[]},
	  "playback": {"position":-1,"committed_position":-1,"rate":1,"playing":false},
	  "revision":1
	}`
	engine, _ := NewEngine(twoTrackTimeline(BlendNormalized))
	before := engine.Revision()
	if err := engine.Load(strings.NewReader(payload)); !errors.Is(err, ErrInvalidArchive) {
		t.Fatalf("error = %v", err)
	}
	if engine.Revision() != before {
		t.Fatal("invalid playback archive changed revision")
	}
}

func TestExplicitRewindDoesNotMoveCommittedCheckpoint(t *testing.T) {
	engine, _ := NewEngine(twoTrackTimeline(BlendNormalized))
	engine.Play()
	if err := engine.Seek(5); err != nil {
		t.Fatal(err)
	}
	if _, revision := engine.CommitPosition(); revision == 0 {
		t.Fatal("commit did not advance revision")
	}
	if err := engine.Seek(1); err != nil {
		t.Fatal(err)
	}
	committed, _ := engine.CommitPosition()
	if committed.CommittedPosition != 5 {
		t.Fatalf("committed checkpoint = %v, want 5", committed.CommittedPosition)
	}
}

func TestLoadRejectsCheckpointAheadOfPosition(t *testing.T) {
	payload := `{
	  "version": 2,
	  "timeline": {"channels":[]},
	  "playback": {"position":3,"committed_position":7,"rate":1,"playing":false},
	  "revision":1
	}`
	engine, _ := NewEngine(twoTrackTimeline(BlendNormalized))
	if err := engine.Load(strings.NewReader(payload)); !errors.Is(err, ErrInvalidArchive) {
		t.Fatalf("error = %v, want invalid archive", err)
	}
}
