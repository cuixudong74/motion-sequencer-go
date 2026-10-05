package motion

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func testTimeline() *Timeline {
	return &Timeline{
		Version: "project-v1",
		Channels: map[string]Channel{"x": {ID: "x", Tracks: []Track{{
			ID:     "t",
			Weight: 2,
			Clips:  []Clip{linearClip("c", 0, 10, 1, 0, 100)},
		}}}},
	}
}

func TestArchiveRoundTripAndRecover(t *testing.T) {
	engine, err := NewEngine(testTimeline())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.TickIdempotent(3, "committed-tick"); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := engine.SaveArchive(&buf); err != nil {
		t.Fatal(err)
	}

	restored, err := NewEngine(&Timeline{})
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.LoadArchive(&buf); err != nil {
		t.Fatal(err)
	}
	state := restored.Playback()
	if state.Position != 3 || state.LastTickToken != "committed-tick" {
		t.Fatalf("restored playback = %+v", state)
	}
	if got := restored.Timeline().Channels["x"].Tracks[0].Weight; got != 2 {
		t.Fatalf("restored weight = %v", got)
	}
	sample, err := restored.SamplePlayback("x")
	if err != nil {
		t.Fatal(err)
	}
	if !sample.Active || sample.Value == 0 {
		t.Fatalf("sample after restore = %+v", sample)
	}
}

func TestLoadOldVersionMigratesDefaults(t *testing.T) {
	old, err := json.Marshal(Archive{
		Format:   ArchiveFormat,
		Version:  OldArchiveVersion,
		Timeline: *testTimeline(),
		Playback: PlaybackState{Position: 2, Rate: 0, CommitSequence: 7},
	})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(&Timeline{})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.LoadArchive(bytes.NewReader(old)); err != nil {
		t.Fatal(err)
	}
	state := engine.Playback()
	if state.Rate != 1 || state.Position != 2 || state.CommitSequence != 7 {
		t.Fatalf("migrated playback = %+v", state)
	}
}

func TestUnknownFutureArchiveIsRejectedWithoutChangingState(t *testing.T) {
	engine, err := NewEngine(testTimeline())
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Seek(1.5); err != nil {
		t.Fatal(err)
	}
	future, err := json.Marshal(Archive{
		Format:   ArchiveFormat,
		Version:  CurrentArchiveVersion + 1,
		Timeline: Timeline{Channels: map[string]Channel{}},
		Playback: PlaybackState{Position: 9, Rate: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = engine.LoadArchive(bytes.NewReader(future))
	if err == nil || !strings.Contains(err.Error(), "unsupported archive version") {
		t.Fatalf("expected unsupported version, got %v", err)
	}
	if got := engine.Playback().Position; got != 1.5 {
		t.Fatalf("position changed after rejected archive: %v", got)
	}
	if _, err := engine.SampleChannel("x", 1.5); err != nil {
		t.Fatalf("old timeline changed after rejected archive: %v", err)
	}
}

func TestInvalidArchiveDoesNotPartiallyApply(t *testing.T) {
	engine, err := NewEngine(testTimeline())
	if err != nil {
		t.Fatal(err)
	}
	invalid := []byte(`{"format":"` + ArchiveFormat + `","version":1,"timeline":{"channels":{"x":{"id":"x","tracks":[{"id":"t","weight":1,"clips":[{"id":"bad","start":1,"duration":-1,"keyframes":[]}]}]}}},"playback":{"position":0,"rate":1}}`)
	if err := engine.LoadArchive(bytes.NewReader(invalid)); err == nil {
		t.Fatal("expected invalid archive error")
	}
	tl := engine.Timeline()
	if len(tl.Channels["x"].Tracks[0].Clips) != 1 || tl.Channels["x"].Tracks[0].Clips[0].ID != "c" {
		t.Fatalf("current timeline was partially changed: %+v", tl.Channels["x"])
	}
}

func TestSaveLoadArchiveFile(t *testing.T) {
	path := t.TempDir() + "/motion.json"
	engine, err := NewEngine(testTimeline())
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Seek(4); err != nil {
		t.Fatal(err)
	}
	if err := engine.SaveArchiveFile(path); err != nil {
		t.Fatal(err)
	}
	restored, err := NewEngine(&Timeline{})
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.LoadArchiveFile(path); err != nil {
		t.Fatal(err)
	}
	if got := restored.Playback().Position; got != 4 {
		t.Fatalf("file restored position = %v", got)
	}
}

func TestArchiveRejectsTrailingJSON(t *testing.T) {
	engine, err := NewEngine(testTimeline())
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := engine.SaveArchive(&buf); err != nil {
		t.Fatal(err)
	}
	trailing := append(buf.Bytes(), []byte(` {"unexpected":true}`)...)
	if err := engine.LoadArchive(bytes.NewReader(trailing)); err == nil {
		t.Fatal("expected trailing JSON to be rejected")
	}
	if got := engine.Playback().Position; got != 0 {
		t.Fatalf("state changed after malformed archive: %v", got)
	}
}
