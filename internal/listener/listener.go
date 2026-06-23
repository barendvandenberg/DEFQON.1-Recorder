package listener

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/ebitengine/oto/v3"

	"github.com/revunix/defqon1-recorder/internal/logging"
	"github.com/revunix/defqon1-recorder/internal/tools"
)

const (
	sampleRate   = 48000
	channelCount = 2

	// probeGrace gives ffmpeg time to fail fast on a bad URL before Play reports
	// success. If the process exits within this window, the error is surfaced
	// instead of leaving the user with silent "playing" audio.
	probeGrace = 1500 * time.Millisecond
	// deviceTimeout bounds how long we wait for the audio backend to become
	// ready before giving up.
	deviceTimeout = 5 * time.Second
	// stopTimeout bounds how long Stop waits for ffmpeg to exit after being killed.
	stopTimeout = 2 * time.Second
)

type State string

const (
	StateStopped  State = "Stopped"
	StateStarting State = "Starting"
	StatePlaying  State = "Playing"
)

type Snapshot struct {
	State State
	Stage string
}

// Player streams a live URL through ffmpeg into an oto player so audio plays
// inside the TUI without opening a browser. At most one session runs at a time.
type Player struct {
	ffmpeg string
	log    logging.Logger

	// playMu serializes Play calls: two concurrent invocations would each stop
	// nothing and both install their own session, leaking ffmpeg + oto.
	playMu sync.Mutex
	// mu guards ctx, current and stopped.
	mu      sync.Mutex
	ctx     *oto.Context
	current *session
	stopped bool
}

type session struct {
	stage  string
	cmd    *exec.Cmd
	player *oto.Player
	done   chan struct{}
	state  State

	closeOnce sync.Once

	mu      sync.Mutex
	stopped bool
	exited  bool
	exitErr error
}

func New(toolsDir string, log logging.Logger) *Player {
	if log == nil {
		log = logging.Noop{}
	}
	return &Player{
		ffmpeg: tools.Resolve(toolsDir).FFmpeg,
		log:    log,
	}
}

// Play starts playback of streamURL, replacing any active session. It blocks
// briefly (probeGrace) to detect immediate failures, then returns. Outcome
// messages are logged through the player's logger.
func (p *Player) Play(stage, streamURL string) error {
	if err := validateURL(streamURL); err != nil {
		p.log.Error(fmt.Sprintf("[%s] Audio failed: %s", stage, err))
		return err
	}

	p.playMu.Lock()
	defer p.playMu.Unlock()

	// A new Play clears any prior stop request and tears down the old session.
	p.mu.Lock()
	p.stopped = false
	p.mu.Unlock()
	p.stopCurrent()

	ctx, err := p.context()
	if err != nil {
		p.log.Error(fmt.Sprintf("[%s] Audio failed: %s", stage, err))
		return err
	}

	args := []string{
		"-hide_banner", "-nostdin", "-loglevel", "error",
		"-i", streamURL,
		"-vn",
		"-f", "s16le",
		"-acodec", "pcm_s16le",
		"-ac", fmt.Sprint(channelCount),
		"-ar", fmt.Sprint(sampleRate),
		"pipe:1",
	}
	cmd := exec.Command(p.ffmpeg, args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		p.log.Error(fmt.Sprintf("[%s] Audio failed: %s", stage, err))
		return fmt.Errorf("ffmpeg stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		p.log.Error(fmt.Sprintf("[%s] Audio failed: %s", stage, err))
		return fmt.Errorf("ffmpeg stderr: %w", err)
	}

	if err := cmd.Start(); err != nil {
		p.log.Error(fmt.Sprintf("[%s] Audio failed: %s", stage, err))
		return fmt.Errorf("start ffmpeg: %w", err)
	}

	otoPlayer := ctx.NewPlayer(stdout)
	otoPlayer.SetBufferSize(sampleRate * channelCount * 2)

	s := &session{
		stage:  stage,
		cmd:    cmd,
		player: otoPlayer,
		done:   make(chan struct{}),
		state:  StateStarting,
	}

	p.mu.Lock()
	// Stop may have been requested while we were starting up; if so, abandon
	// the fresh session instead of leaving it running.
	if p.stopped {
		p.mu.Unlock()
		s.stop()
		return nil
	}
	p.current = s
	p.mu.Unlock()

	go p.drainStderr(stage, stderr)
	go p.wait(s)

	otoPlayer.Play()

	// Probe for an immediate failure before declaring success.
	select {
	case <-s.done:
		p.mu.Lock()
		if p.current == s {
			p.current = nil
		}
		p.mu.Unlock()
		if s.isStopped() {
			// User hit stop during startup; nothing to log (Stop handles it).
			return nil
		}
		err := s.exitError()
		if err == nil {
			err = errors.New("ffmpeg exited without producing audio")
		}
		p.log.Error(fmt.Sprintf("[%s] Audio failed: %s", stage, err))
		return fmt.Errorf("ffmpeg exited: %w", err)
	case <-time.After(probeGrace):
	}

	p.setState(s, StatePlaying)
	p.log.Info(fmt.Sprintf("[%s] TUI audio playing.", stage))
	return nil
}

// Stop ends the active session (if any) and marks the player stopped so a
// concurrent Play that is still starting up will abort its new session.
func (p *Player) Stop() {
	p.mu.Lock()
	p.stopped = true
	s := p.current
	p.current = nil
	p.mu.Unlock()
	if s != nil {
		s.stop()
	}
}

func (p *Player) Snapshot() Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.current == nil {
		return Snapshot{State: StateStopped}
	}
	return Snapshot{State: p.current.state, Stage: p.current.stage}
}

func (p *Player) stopCurrent() {
	p.mu.Lock()
	s := p.current
	p.current = nil
	p.mu.Unlock()
	if s != nil {
		s.stop()
	}
}

func (p *Player) setState(s *session, state State) {
	p.mu.Lock()
	if p.current == s {
		s.state = state
	}
	p.mu.Unlock()
}

// wait reaps the ffmpeg process and cleans up the audio player once it exits.
func (p *Player) wait(s *session) {
	err := s.cmd.Wait()

	s.mu.Lock()
	s.exited = true
	s.exitErr = err
	wasStopped := s.stopped
	s.mu.Unlock()

	s.closePlayer()
	close(s.done)

	p.mu.Lock()
	wasCurrent := p.current == s
	if wasCurrent {
		p.current = nil
	}
	p.mu.Unlock()

	// Surface unexpected deaths that happen after a session was confirmed
	// playing; user-initiated stops are silent (Stop already logged).
	if wasCurrent && !wasStopped {
		p.log.Warn(fmt.Sprintf("[%s] TUI audio ended unexpectedly.", s.stage))
	}
}

// context lazily builds the oto context. The potentially slow device-ready wait
// happens without holding mu so Stop/Snapshot are not blocked for seconds.
func (p *Player) context() (*oto.Context, error) {
	p.mu.Lock()
	if p.ctx != nil {
		ctx := p.ctx
		p.mu.Unlock()
		return ctx, nil
	}
	p.mu.Unlock()

	ctx, ready, err := oto.NewContext(&oto.NewContextOptions{
		SampleRate:   sampleRate,
		ChannelCount: channelCount,
		Format:       oto.FormatSignedInt16LE,
		BufferSize:   500 * time.Millisecond,
	})
	if err != nil {
		return nil, fmt.Errorf("audio device: %w", err)
	}

	select {
	case <-ready:
	case <-time.After(deviceTimeout):
		return nil, errors.New("audio device did not become ready")
	}

	p.mu.Lock()
	if p.ctx == nil {
		p.ctx = ctx
	}
	result := p.ctx
	p.mu.Unlock()
	return result, nil
}

func (p *Player) drainStderr(stage string, r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.Contains(strings.ToLower(line), "error") {
			p.log.Error(fmt.Sprintf("[%s] ffmpeg: %s", stage, line))
		}
	}
}

func (s *session) stop() {
	s.mu.Lock()
	s.stopped = true
	s.mu.Unlock()

	s.closePlayer()
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	select {
	case <-s.done:
	case <-time.After(stopTimeout):
	}
}

func (s *session) closePlayer() {
	s.closeOnce.Do(func() {
		_ = s.player.Close()
	})
}

func (s *session) isStopped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopped
}

func (s *session) exitError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exitErr
}

func validateURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("unsupported URL scheme %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return errors.New("stream URL is missing a host")
	}
	return nil
}
