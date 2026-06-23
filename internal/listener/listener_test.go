package listener

import (
	"testing"

	"github.com/revunix/defqon1-recorder/internal/logging"
)

func TestValidateURL(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"https://listen.mixlr.com/live", true},
		{"http://example.com/stream", true},
		{"file:///etc/passwd", false},
		{"ftp://example.com", false},
		{"https://", false},
		{"", false},
	}
	for _, c := range cases {
		err := validateURL(c.in)
		if (err == nil) != c.want {
			t.Fatalf("validateURL(%q) err=%v, want ok=%v", c.in, err, c.want)
		}
	}
}

func TestNewPlayerDefaultsToStopped(t *testing.T) {
	p := New(t.TempDir(), logging.Noop{})
	snap := p.Snapshot()
	if snap.State != StateStopped {
		t.Fatalf("fresh player should be Stopped, got %q", snap.State)
	}
	if snap.Stage != "" {
		t.Fatalf("fresh player should have empty stage, got %q", snap.Stage)
	}
}

func TestStopIsNoopWhenIdle(t *testing.T) {
	p := New(t.TempDir(), logging.Noop{})
	p.Stop()
	if p.Snapshot().State != StateStopped {
		t.Fatal("Stop on idle player must not change state")
	}
}
