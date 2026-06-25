package prefs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMissingFileIsEmpty(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "nope.ini"))
	got, err := s.Load()
	if err != nil {
		t.Fatalf("missing file should not error, got %s", err)
	}
	if len(got) != 0 {
		t.Fatalf("missing file should load empty map, got %v", got)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recorder.ini")
	s := New(path)

	want := map[string]bool{
		"defqon1blue":  true,
		"defqon1red":   false,
		"orocash-test": true,
	}
	if err := s.SaveAll(want); err != nil {
		t.Fatalf("SaveAll: %s", err)
	}

	loaded, err := New(path).Load()
	if err != nil {
		t.Fatalf("Load: %s", err)
	}
	if len(loaded) != len(want) {
		t.Fatalf("expected %d entries, got %d (%v)", len(want), len(loaded), loaded)
	}
	for k, v := range want {
		if loaded[k] != v {
			t.Errorf("channel %s: got %v, want %v", k, loaded[k], v)
		}
	}
}

func TestSaveSingleChannelUpdatesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recorder.ini")
	s := New(path)
	if err := s.SaveAll(map[string]bool{"defqon1blue": true, "defqon1red": true}); err != nil {
		t.Fatal(err)
	}
	if err := s.Save("defqon1red", false); err != nil {
		t.Fatal(err)
	}

	loaded, err := New(path).Load()
	if err != nil {
		t.Fatal(err)
	}
	if !loaded["defqon1blue"] {
		t.Error("blue should still be on")
	}
	if loaded["defqon1red"] {
		t.Error("red should now be off")
	}
}

func TestSaveYouTubePreservesRecording(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recorder.ini")
	s := New(path)
	if err := s.SaveAll(map[string]bool{"defqon1blue": true, "defqon1red": false}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveYouTube("Friday", "audio"); err != nil {
		t.Fatal(err)
	}

	loaded, err := New(path).LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Recording["defqon1blue"] || loaded.Recording["defqon1red"] {
		t.Fatalf("recording preferences were not preserved: %+v", loaded.Recording)
	}
	if loaded.YouTube["Friday"] != "audio" {
		t.Fatalf("Friday mode = %q, want audio", loaded.YouTube["Friday"])
	}
}

func TestParseIgnoresCommentsAndOtherSections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recorder.ini")
	content := `# header comment
; another comment
[other]
ignored=on

[recording]
defqon1blue = true
defqon1red=off
bogus=
=on

[youtube]
Friday=video_audio
Saturday=audio
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	all, err := New(path).LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	got := all.Recording
	if !got["defqon1blue"] {
		t.Error("blue should be on")
	}
	if got["defqon1red"] {
		t.Error("red should be off")
	}
	if _, ok := got["ignored"]; ok {
		t.Error("entries outside [recording] must be ignored")
	}
	if all.YouTube["Friday"] != "video_audio" || all.YouTube["Saturday"] != "audio" {
		t.Fatalf("youtube preferences not loaded: %+v", all.YouTube)
	}
}

func TestSaveProducesReadableINI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recorder.ini")
	if err := New(path).SaveAll(map[string]bool{"defqon1blue": true, "defqon1red": false}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "[recording]") {
		t.Errorf("written file should contain the recording section, got:\n%s", text)
	}
	if !strings.Contains(text, "[youtube]") {
		t.Errorf("written file should contain the youtube section, got:\n%s", text)
	}
	if !strings.Contains(text, "defqon1blue=on") {
		t.Errorf("expected defqon1blue=on, got:\n%s", text)
	}
}

func TestBoolVariants(t *testing.T) {
	for _, in := range []string{"1", "true", "TRUE", "yes", "on", "enabled"} {
		if !parseBool(in) {
			t.Errorf("parseBool(%q) should be true", in)
		}
	}
	for _, in := range []string{"0", "false", "off", "no", "", "anything"} {
		if parseBool(in) {
			t.Errorf("parseBool(%q) should be false", in)
		}
	}
}
