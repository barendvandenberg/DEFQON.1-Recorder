package timetable

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLoadTimetable(t *testing.T) {
	tt, err := Load(filepath.Join("..", "..", "dq-timetable.json"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if tt.Size() == 0 {
		t.Fatal("expected non-empty timetable")
	}

	// The timetable must be sorted by start time.
	for i := 1; i < tt.Size(); i++ {
		if tt.sets[i-1].Start.After(tt.sets[i].Start) {
			t.Fatalf("sets not sorted at index %d", i)
		}
	}

	// Find the known RED opening ceremony on 2026-06-26 13:00 Europe/Berlin.
	loc := tt.sets[0].Start.Location()
	want := time.Date(2026, time.June, 26, 13, 0, 0, 0, loc)
	var opening *Set
	for i := range tt.sets {
		if tt.sets[i].Stage == "RED" && tt.sets[i].Start.Equal(want) {
			opening = &tt.sets[i]
			break
		}
	}
	if opening == nil {
		t.Fatalf("RED opening ceremony 2026-06-26 13:00 not found")
	}
	const wantDJ = "The Opening Ceremony with Outsiders"
	if opening.DJ != wantDJ {
		t.Fatalf("DJ = %q, want %q", opening.DJ, wantDJ)
	}
	if opening.End.Equal(opening.Start) {
		t.Fatal("end time not derived from next set")
	}
}

func TestCurrentAndUpcoming(t *testing.T) {
	// Synthetic timetable around "now" so the time-dependent logic can be
	// exercised regardless of the real event dates.
	now := time.Now()
	loc := now.Location()

	tt := &Timetable{sets: []Set{
		{Stage: "RED", DJ: "Past DJ", Start: now.Add(-2 * time.Hour), End: now.Add(-1 * time.Hour)},
		{Stage: "BLUE", DJ: "Live Now", Start: now.Add(-30 * time.Minute), End: now.Add(30 * time.Minute)},
		{Stage: "RED", DJ: "Next RED", Start: now.Add(1 * time.Hour), End: now.Add(2 * time.Hour)},
		{Stage: "BLUE", DJ: "Next BLUE", Start: now.Add(2 * time.Hour), End: now.Add(3 * time.Hour)},
	}}
	_ = loc

	if set := tt.CurrentSet("DEFINITELY_NOT_A_STAGE"); set != nil {
		t.Fatalf("expected nil for unknown stage, got %+v", set)
	}
	if set := tt.CurrentSet("RED"); set != nil {
		t.Fatalf("RED has no current set, got %+v", set)
	}
	if set := tt.CurrentSet("BLUE"); set == nil || set.DJ != "Live Now" {
		t.Fatalf("expected current BLUE = Live Now, got %+v", set)
	}

	upcoming := tt.Upcoming()
	if len(upcoming) != 2 {
		t.Fatalf("expected 2 upcoming sets (one per stage), got %d", len(upcoming))
	}
	seen := map[string]bool{}
	for _, s := range upcoming {
		if seen[s.Stage] {
			t.Fatalf("stage %s appears more than once in upcoming", s.Stage)
		}
		seen[s.Stage] = true
		if !s.Start.After(time.Now()) {
			t.Fatalf("upcoming set in the past: %+v", s)
		}
	}
}
