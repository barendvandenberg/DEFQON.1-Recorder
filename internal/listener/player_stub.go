//go:build linux

package listener

import (
	"fmt"

	"github.com/revunix/defqon1-recorder/internal/logging"
)

// errUnsupported signals that TUI audio playback is unavailable. On Linux the
// recorder typically runs headless (e.g. inside Docker), so the oto audio
// backend — which would require CGO + ALSA — is intentionally not compiled in.
var errUnsupported = fmt.Errorf("TUI audio playback is not supported on this platform")

// Player is a no-op implementation used on platforms without the oto backend.
type Player struct {
	log logging.Logger
}

func New(_ string, log logging.Logger) *Player {
	if log == nil {
		log = logging.Noop{}
	}
	return &Player{log: log}
}

func (p *Player) Play(stage, _ string) error {
	p.log.Error(fmt.Sprintf("[%s] Audio failed: %s", stage, errUnsupported))
	return errUnsupported
}

func (p *Player) Stop() {}

func (p *Player) Snapshot() Snapshot {
	return Snapshot{State: StateStopped}
}
