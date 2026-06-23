package controller

import (
	"testing"
	"time"

	"github.com/revunix/defqon1-recorder/internal/logging"
	"github.com/revunix/defqon1-recorder/internal/mixlr"
	"github.com/revunix/defqon1-recorder/internal/recorder"
	"github.com/revunix/defqon1-recorder/internal/status"
	"github.com/revunix/defqon1-recorder/internal/timetable"
)

func newTestController(t *testing.T, channels, blacklist []string) *Controller {
	t.Helper()
	enabled := make(map[string]bool, len(channels))
	for _, ch := range channels {
		enabled[ch] = true
	}
	for _, ch := range blacklist {
		enabled[ch] = false
	}
	return New(
		mixlr.New("https://example.invalid/"),
		recorder.New(t.TempDir(), time.Minute, t.TempDir(), "tester", nil, logging.Noop{}),
		&timetable.Timetable{},
		status.New(channels),
		logging.Noop{},
		channels,
		enabled,
		nil,
	)
}

func TestIsEnabledDefaultsAndBlacklist(t *testing.T) {
	channels := []string{"defqon1blue", "defqon1red"}
	c := newTestController(t, channels, []string{"defqon1red"})

	if !c.IsEnabled("defqon1blue") {
		t.Fatal("blue should be enabled by default")
	}
	if c.IsEnabled("defqon1red") {
		t.Fatal("red should be disabled via blacklist")
	}
	if !c.IsEnabled("unknown-channel") {
		t.Fatal("unknown channels should default to enabled")
	}
}

func TestToggleFlipsState(t *testing.T) {
	channels := []string{"defqon1blue"}
	c := newTestController(t, channels, nil)

	if c.Toggle("defqon1blue") != false {
		t.Fatal("toggling an enabled channel should disable it (return false)")
	}
	if c.IsEnabled("defqon1blue") {
		t.Fatal("blue should be disabled after toggle")
	}

	if c.Toggle("defqon1blue") != true {
		t.Fatal("toggling a disabled channel should enable it (return true)")
	}
	if !c.IsEnabled("defqon1blue") {
		t.Fatal("blue should be enabled after second toggle")
	}
}

type recordingPersister struct {
	calls []struct {
		channel string
		enabled bool
	}
}

func (r *recordingPersister) Save(channel string, enabled bool) error {
	r.calls = append(r.calls, struct {
		channel string
		enabled bool
	}{channel, enabled})
	return nil
}

func TestTogglePersistsEachChange(t *testing.T) {
	channels := []string{"defqon1blue", "defqon1red"}
	enabled := map[string]bool{"defqon1blue": true, "defqon1red": true}
	p := &recordingPersister{}
	c := New(
		mixlr.New("https://example.invalid/"),
		recorder.New(t.TempDir(), time.Minute, t.TempDir(), "tester", nil, logging.Noop{}),
		&timetable.Timetable{},
		status.New(channels),
		logging.Noop{},
		channels,
		enabled,
		p,
	)

	c.Toggle("defqon1blue") // -> off
	c.Toggle("defqon1blue") // -> on
	c.Toggle("defqon1red")  // -> off

	if len(p.calls) != 3 {
		t.Fatalf("expected 3 persist calls, got %d", len(p.calls))
	}
	if p.calls[0].enabled || p.calls[1].channel != "defqon1blue" || !p.calls[1].enabled || p.calls[2].enabled {
		t.Fatalf("unexpected persist calls: %+v", p.calls)
	}
}
