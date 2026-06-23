package split

import (
	"testing"
	"time"

	"github.com/revunix/defqon1-recorder/internal/timetable"
)

func berlin(y, mo, d, h, mi int) time.Time {
	loc, _ := time.LoadLocation("Europe/Berlin")
	return time.Date(y, time.Month(mo), d, h, mi, 0, 0, loc)
}

func TestPlanSetsClipsToRecordingWindow(t *testing.T) {
	// UV stage: 13:00-14:00 and 14:00-15:00 sets.
	tt := timetable.NewWithSets([]timetable.Set{
		{Stage: "UV", DJ: "Angerfist", Start: berlin(2026, 6, 26, 13, 0), End: berlin(2026, 6, 26, 14, 0)},
		{Stage: "UV", DJ: "D-Block & S-te-Fan", Start: berlin(2026, 6, 26, 14, 0), End: berlin(2026, 6, 26, 15, 0)},
	})

	// Recording ran 12:55 -> 14:30, i.e. it started before set 1 and ended mid set 2.
	recStart := berlin(2026, 6, 26, 12, 55)
	recEnd := berlin(2026, 6, 26, 14, 30)

	segs := planSets("UV", recStart, recEnd, tt)
	if len(segs) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(segs))
	}

	// Set 1: fully inside recording -> offset 5m, duration 60m.
	if secs(segs[0].offset) != 5*60 {
		t.Fatalf("set1 offset = %v, want 5m", segs[0].offset)
	}
	if secs(segs[0].duration) != 60*60 {
		t.Fatalf("set1 duration = %v, want 60m", segs[0].duration)
	}
	// Set 2: started 65m into recording, clipped to end at 30m (95m - 65m).
	if secs(segs[1].offset) != 65*60 {
		t.Fatalf("set2 offset = %v, want 65m", segs[1].offset)
	}
	if secs(segs[1].duration) != 30*60 {
		t.Fatalf("set2 duration = %v, want 30m", segs[1].duration)
	}
}

func TestPlanSetsSkipsOutOfRange(t *testing.T) {
	tt := timetable.NewWithSets([]timetable.Set{
		{Stage: "BLUE", DJ: "A", Start: berlin(2026, 6, 26, 13, 0), End: berlin(2026, 6, 26, 14, 0)},
		{Stage: "BLUE", DJ: "B", Start: berlin(2026, 6, 26, 14, 0), End: berlin(2026, 6, 26, 15, 0)},
	})

	// Recording window entirely before any set -> no segments.
	segs := planSets("BLUE", berlin(2026, 6, 26, 11, 0), berlin(2026, 6, 26, 11, 30), tt)
	if len(segs) != 0 {
		t.Fatalf("expected 0 segments, got %d", len(segs))
	}

	// Other stage -> no segments.
	segs = planSets("RED", berlin(2026, 6, 26, 13, 0), berlin(2026, 6, 26, 14, 0), tt)
	if len(segs) != 0 {
		t.Fatalf("expected 0 segments for other stage, got %d", len(segs))
	}

	// Nil timetable -> no segments, no panic.
	segs = planSets("BLUE", berlin(2026, 6, 26, 13, 0), berlin(2026, 6, 26, 14, 0), nil)
	if segs != nil {
		t.Fatalf("nil timetable must yield no segments, got %v", segs)
	}
}

func TestSetReleaseNameSanitizes(t *testing.T) {
	got := setReleaseName("UV", "D-Block & S-te-Fan", berlin(2026, 6, 26, 13, 0), "revunix")
	want := "DEFQON.1.2026.UV.DBlockSteFan.20260626.1300.LIVE.MP3-revunix"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func secs(d time.Duration) float64 { return d.Seconds() }
