package recorder

import (
	"strings"
	"testing"
	"time"
)

func TestSceneReleaseName(t *testing.T) {
	ts := time.Date(2026, 6, 26, 18, 0, 7, 0, time.UTC)
	got := sceneReleaseName("BLUE", ts, "revunix")
	want := "DEFQON.1.S2026.BLUE.20260626.1800.LIVE.MP3-revunix"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSceneReleaseNameSanitizesAndFallsBack(t *testing.T) {
	ts := time.Date(2026, 6, 26, 18, 0, 0, 0, time.UTC)

	// Stage with whitespace/specials is reduced to alphanumerics.
	got := sceneReleaseName("Orocash Music!", ts, "revunix")
	if !strings.Contains(got, ".OrocashMusic.") {
		t.Fatalf("stage should be sanitized to alphanumerics, got %q", got)
	}

	// Empty stage falls back to UNKNOWN, empty group to anonymous.
	got = sceneReleaseName("", ts, "")
	if !strings.Contains(got, ".UNKNOWN.") || !strings.HasSuffix(got, "-anonymous") {
		t.Fatalf("empty stage/group should fall back, got %q", got)
	}

	// Windows-style group "DOMAIN\user" is cleaned by the caller (util), but the
	// name builder still strips anything non-alphanumeric.
	got = sceneReleaseName("RED", ts, `DOMAIN\revunix`)
	if !strings.HasSuffix(got, "-DOMAINrevunix") {
		t.Fatalf("group should be reduced to alphanumerics, got %q", got)
	}
}

func TestSceneReleaseNameContainsDEFQON(t *testing.T) {
	got := sceneReleaseName("BLUE", time.Now().UTC(), "revunix")
	if !strings.HasPrefix(got, "DEFQON.1.") {
		t.Fatalf("name must start with DEFQON.1, got %q", got)
	}
}
