package controller

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/revunix/defqon1-recorder/internal/logging"
	"github.com/revunix/defqon1-recorder/internal/mixlr"
	"github.com/revunix/defqon1-recorder/internal/recorder"
	"github.com/revunix/defqon1-recorder/internal/status"
	"github.com/revunix/defqon1-recorder/internal/timetable"
	"github.com/revunix/defqon1-recorder/internal/util"
)

// TogglePersister persists recording toggles so they survive a restart. The
// controller depends on the small interface rather than a concrete store.
type TogglePersister interface {
	Save(channel string, enabled bool) error
}

type Controller struct {
	client    *mixlr.Client
	recorder  *recorder.Manager
	timetable *timetable.Timetable
	status    *status.Registry
	log       logging.Logger
	channels  []string

	enabledMu sync.RWMutex
	enabled   map[string]bool
	persister TogglePersister

	// trigger requests an immediate re-check of a single channel (e.g. right
	// after it is re-enabled) so recording resumes without waiting for the next
	// periodic CheckOnce.
	trigger chan string
}

func New(
	client *mixlr.Client,
	rec *recorder.Manager,
	tt *timetable.Timetable,
	reg *status.Registry,
	log logging.Logger,
	channels []string,
	enabled map[string]bool,
	persister TogglePersister,
) *Controller {
	return &Controller{
		client:    client,
		recorder:  rec,
		timetable: tt,
		status:    reg,
		log:       log,
		channels:  channels,
		enabled:   enabled,
		persister: persister,
		trigger:   make(chan string, 16),
	}
}

func (c *Controller) CheckOnce(ctx context.Context) {
	c.log.Info(fmt.Sprintf("--- Checking channels at %s ---",
		time.Now().In(util.Berlin()).Format("15:04:05")))

	var wg sync.WaitGroup
	for _, channel := range c.channels {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.checkChannel(ctx, channel)
		}()
	}
	wg.Wait()
}

func (c *Controller) checkChannel(ctx context.Context, channel string) {
	ch, err := c.client.Fetch(ctx, channel)
	if err != nil {
		c.log.Error(fmt.Sprintf("[%s] Fetch Error: %s", channel, err))
		// Keep the last known status on transient errors.
		return
	}

	stage := ch.Username
	if stage == "" {
		stage = channel
	}
	c.status.Set(channel, stage, ch.Live, ch.ListenerCount, ch.StreamURL)

	if !c.IsEnabled(channel) {
		// Recording is disabled for this channel: never start one, and stop a
		// recording that may have started before the channel was disabled.
		if c.recorder.IsRecording(stage) {
			c.log.Info(fmt.Sprintf("[%s] Recording disabled. Stopping.", stage))
			c.recorder.Stop(stage)
		}
		return
	}

	if ch.Live && ch.StreamURL != "" {
		if c.recorder.IsRecording(stage) {
			c.recorder.UpdateListeners(stage, ch.ListenerCount)
		} else {
			c.recorder.Start(stage, ch.StreamURL, ch.ListenerCount)
		}
		return
	}

	if c.recorder.IsRecording(stage) {
		c.log.Info(fmt.Sprintf("[%s] Offline. Stopping recording.", stage))
		c.recorder.Stop(stage)
	}
}

// IsEnabled reports whether a channel is allowed to be recorded. Unknown
// channels default to enabled.
func (c *Controller) IsEnabled(channel string) bool {
	c.enabledMu.RLock()
	defer c.enabledMu.RUnlock()
	enabled, ok := c.enabled[channel]
	if !ok {
		return true
	}
	return enabled
}

// Toggle flips the recording state of a channel and returns the new value.
// Disabling a channel immediately stops any active recording for it; enabling
// a channel schedules a prompt re-check so recording resumes without waiting
// for the next periodic check. The new state is persisted if a persister is
// configured.
func (c *Controller) Toggle(channel string) bool {
	c.enabledMu.Lock()
	cur, ok := c.enabled[channel]
	if !ok {
		cur = true
	}
	next := !cur
	c.enabled[channel] = next
	c.enabledMu.Unlock()

	c.persist(channel, next)

	if !next {
		stage := channel
		if st, found := c.status.Stream(channel); found && st.Stage != "" {
			stage = st.Stage
		}
		if c.recorder.IsRecording(stage) {
			c.log.Info(fmt.Sprintf("[%s] Recording disabled. Stopping.", stage))
			c.recorder.Stop(stage)
		}
		return false
	}

	c.kick(channel)
	return true
}

// persist writes the toggle to disk. A failure is logged but does not revert
// the in-memory state, so the session still behaves correctly even if the file
// cannot be written.
func (c *Controller) persist(channel string, enabled bool) {
	if c.persister == nil {
		return
	}
	if err := c.persister.Save(channel, enabled); err != nil {
		c.log.Error(fmt.Sprintf("cannot persist recording toggle: %s", err))
	}
}

// kick requests a non-blocking immediate re-check of a channel. If the trigger
// buffer is full (e.g. many rapid toggles), the signal is dropped: the next
// periodic CheckOnce will still reconcile the state.
func (c *Controller) kick(channel string) {
	select {
	case c.trigger <- channel:
	default:
	}
}

func (c *Controller) Run(ctx context.Context, checkInterval, stalledInterval time.Duration) {
	c.CheckOnce(ctx)

	check := time.NewTicker(checkInterval)
	stalled := time.NewTicker(stalledInterval)
	defer check.Stop()
	defer stalled.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-check.C:
			c.CheckOnce(ctx)
		case <-stalled.C:
			c.recorder.MonitorStalled()
		case channel := <-c.trigger:
			c.checkChannel(ctx, channel)
		}
	}
}
