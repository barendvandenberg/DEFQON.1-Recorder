package youtube

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/revunix/defqon1-recorder/internal/logging"
	"github.com/revunix/defqon1-recorder/internal/tools"
	"github.com/revunix/defqon1-recorder/internal/util"
)

const (
	KindTrailer = "TRAILER"
	KindLive    = "LIVE"

	formatBestAvailable = "bestvideo+bestaudio/best"
	formatAudio         = "bestaudio/best[height<=480]/best"
	mp3Bitrate          = "320k"
	probeLimit          = 20 * time.Second
)

type State string

const (
	StateDisabled   State = "Disabled"
	StateChecking   State = "Checking"
	StateWaiting    State = "Waiting"
	StateNotLive    State = "Not live"
	StateTrailer    State = "Trailer"
	StateRecording  State = "Recording"
	StateRecovering State = "Recovering"
	StateEnded      State = "Ended"
	StateError      State = "Error"
)

type Feed struct {
	Name string
	URL  string
}

type RecordMode string

const (
	ModeVideoAudio RecordMode = "video_audio"
	ModeAudio      RecordMode = "audio"
	ModeNone       RecordMode = "none"
)

func ParseRecordMode(raw string) RecordMode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "video_audio", "video-audio", "video/audio", "video+audio", "video+mp3", "video_mp3", "both", "all", "av", "a/v":
		return ModeVideoAudio
	case "audio", "audio_only", "audio-only", "mp3", "mp3_only", "mp3-only":
		return ModeAudio
	case "none", "off", "disabled", "skip", "no", "false", "0":
		return ModeNone
	default:
		return ModeVideoAudio
	}
}

func (m RecordMode) Label() string {
	switch m {
	case ModeAudio:
		return "MP3"
	case ModeNone:
		return "Off"
	default:
		return "Video+MP3"
	}
}

func (m RecordMode) Detail() string {
	switch m {
	case ModeAudio:
		return "MP3 only"
	case ModeNone:
		return "disabled"
	default:
		return "video + MP3"
	}
}

func (m RecordMode) recordsVideo() bool {
	return m == ModeVideoAudio
}

func (m RecordMode) recordsAudio() bool {
	return m == ModeVideoAudio || m == ModeAudio
}

func (m RecordMode) next() RecordMode {
	switch m {
	case ModeVideoAudio:
		return ModeAudio
	case ModeAudio:
		return ModeNone
	default:
		return ModeVideoAudio
	}
}

type ModePersister interface {
	SaveYouTube(feed, mode string) error
}

type Config struct {
	Enabled       bool
	Feeds         []Feed
	Modes         map[string]RecordMode
	RecordingsDir string
	ToolsDir      string
	Group         string
	PollInterval  time.Duration
	StalledAfter  time.Duration
	Persister     ModePersister
}

type videoFormat struct {
	format      string
	container   string
	extension   string
	description string
}

type Snapshot struct {
	Name        string
	URL         string
	State       State
	Detail      string
	Path        string
	Mode        RecordMode
	Recording   bool
	Live        bool
	TrailerDone bool
	Previewing  bool
	Fullscreen  bool
	LastChecked time.Time
}

type Manager struct {
	enabled      bool
	preview      bool
	feeds        []Feed
	byName       map[string]Feed
	dir          string
	paths        tools.Paths
	group        string
	pollInterval time.Duration
	stalledAfter time.Duration
	video        videoFormat
	modes        map[string]RecordMode
	persister    ModePersister
	log          logging.Logger

	mu       sync.RWMutex
	status   map[string]*feedStatus
	active   map[string]*recording
	previewS *previewSession
	triggers map[string]chan struct{}

	wg      sync.WaitGroup
	stopped int32
}

type feedStatus struct {
	name        string
	url         string
	state       State
	detail      string
	path        string
	mode        RecordMode
	recording   bool
	live        bool
	trailerDone bool
	lastChecked time.Time
}

type recording struct {
	feed        string
	kind        string
	mode        RecordMode
	cmd         *exec.Cmd
	audioYtdlp  *exec.Cmd
	audioFFmpeg *exec.Cmd
	audioPipeR  *os.File
	audioPipeW  *os.File
	path        string
	audioPath   string
	startedAt   time.Time
	lastSize    int64
	lastCheck   time.Time
	done        chan struct{}

	mu       sync.Mutex
	stopping bool
}

type previewSession struct {
	feed       string
	cmd        *exec.Cmd
	fullscreen bool
	done       chan struct{}
}

type metadata struct {
	Title      string `json:"title"`
	LiveStatus string `json:"live_status"`
	IsLive     bool   `json:"is_live"`
}

func New(cfg Config, log logging.Logger) *Manager {
	if log == nil {
		log = logging.Noop{}
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 30 * time.Second
	}
	if cfg.StalledAfter <= 0 {
		cfg.StalledAfter = 90 * time.Second
	}
	if cfg.Group == "" {
		cfg.Group = "anonymous"
	}
	video := bestVideoFormat()

	paths := tools.Resolve(cfg.ToolsDir)
	enabled := cfg.Enabled && SupportedOS()
	m := &Manager{
		enabled:      enabled,
		preview:      enabled,
		feeds:        append([]Feed(nil), cfg.Feeds...),
		byName:       make(map[string]Feed, len(cfg.Feeds)),
		dir:          cfg.RecordingsDir,
		paths:        paths,
		group:        cfg.Group,
		pollInterval: cfg.PollInterval,
		stalledAfter: cfg.StalledAfter,
		video:        video,
		modes:        make(map[string]RecordMode, len(cfg.Feeds)),
		persister:    cfg.Persister,
		log:          log,
		status:       make(map[string]*feedStatus, len(cfg.Feeds)),
		active:       make(map[string]*recording),
		triggers:     make(map[string]chan struct{}, len(cfg.Feeds)),
	}
	for _, feed := range m.feeds {
		m.byName[feed.Name] = feed
		m.triggers[feed.Name] = make(chan struct{}, 1)
		mode := ModeVideoAudio
		if cfg.Modes != nil {
			if configured, ok := cfg.Modes[feed.Name]; ok {
				mode = ParseRecordMode(string(configured))
			}
		}
		m.modes[feed.Name] = mode
		state := StateWaiting
		detail := "Waiting for live stream"
		if !enabled {
			state = StateDisabled
			detail = "YouTube recording is only enabled on Windows/macOS"
		} else if mode == ModeNone {
			state = StateDisabled
			detail = "YouTube recording disabled for this feed"
		}
		m.status[feed.Name] = &feedStatus{
			name:   feed.Name,
			url:    feed.URL,
			state:  state,
			detail: detail,
			mode:   mode,
		}
	}
	return m
}

func SupportedOS() bool {
	return runtime.GOOS == "windows" || runtime.GOOS == "darwin"
}

func bestVideoFormat() videoFormat {
	return videoFormat{
		format:      formatBestAvailable,
		container:   "mkv",
		extension:   "mkv",
		description: "best available source quality",
	}
}

func (m *Manager) Enabled() bool {
	return m != nil && m.enabled
}

func (m *Manager) PreviewSupported() bool {
	return m != nil && m.preview
}

func (m *Manager) modeFor(feed string) RecordMode {
	m.mu.RLock()
	defer m.mu.RUnlock()
	mode := m.modes[feed]
	if mode == "" {
		return ModeVideoAudio
	}
	return mode
}

func (m *Manager) kick(feed string) {
	ch := m.triggers[feed]
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (m *Manager) ToggleMode(feedName string) (RecordMode, error) {
	if m == nil || !m.enabled {
		return ModeNone, errors.New("YouTube recording is not enabled")
	}
	if _, ok := m.byName[feedName]; !ok {
		return ModeNone, fmt.Errorf("unknown YouTube feed %q", feedName)
	}

	m.mu.Lock()
	cur := m.modes[feedName]
	if cur == "" {
		cur = ModeVideoAudio
	}
	next := cur.next()
	m.modes[feedName] = next
	if s := m.status[feedName]; s != nil {
		s.mode = next
		if next == ModeNone {
			s.state = StateDisabled
			s.detail = "YouTube recording disabled for this feed"
			s.path = ""
			s.recording = m.active[feedName] != nil
		} else if s.state == StateDisabled {
			s.state = StateWaiting
			s.detail = "Recording mode changed; checking stream"
		}
	}
	active := m.active[feedName] != nil
	m.mu.Unlock()

	if m.persister != nil {
		if err := m.persister.SaveYouTube(feedName, string(next)); err != nil {
			m.log.Error(fmt.Sprintf("cannot persist YouTube recording mode: %s", err))
		}
	}
	if active {
		m.stopRecording(feedName)
	}
	if next != ModeNone {
		m.kick(feedName)
	}
	return next, nil
}

func (m *Manager) Run(ctx context.Context) {
	if !m.enabled {
		return
	}
	m.log.Info(fmt.Sprintf("YouTube monitor enabled: %d feeds, poll %s, format %s.", len(m.feeds), m.pollInterval, m.video.description))

	var loops sync.WaitGroup
	for _, feed := range m.feeds {
		feed := feed
		loops.Add(1)
		go func() {
			defer loops.Done()
			m.runFeed(ctx, feed)
		}()
	}
	loops.Wait()
}

func (m *Manager) runFeed(ctx context.Context, feed Feed) {
	m.checkFeed(ctx, feed, true)

	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()
	trigger := m.triggers[feed.Name]

	for {
		select {
		case <-ctx.Done():
			return
		case <-trigger:
			m.monitorActive(feed.Name)
			m.checkFeed(ctx, feed, false)
		case <-ticker.C:
			m.monitorActive(feed.Name)
			m.checkFeed(ctx, feed, false)
		}
	}
}

func (m *Manager) checkFeed(ctx context.Context, feed Feed, startup bool) {
	mode := m.modeFor(feed.Name)
	if mode == ModeNone {
		if m.isRecording(feed.Name, "") {
			m.log.Info(fmt.Sprintf("[YouTube %s] Recording disabled. Stopping.", feed.Name))
			m.stopRecording(feed.Name)
		}
		m.setStatus(feed.Name, func(s *feedStatus) {
			s.state = StateDisabled
			s.detail = "YouTube recording disabled for this feed"
			s.path = ""
			s.mode = mode
			s.live = false
			s.lastChecked = time.Now()
		})
		return
	}

	m.setStatus(feed.Name, func(s *feedStatus) {
		s.state = StateChecking
		s.detail = "Checking YouTube live status"
		s.mode = mode
	})

	meta, err := m.probe(ctx, feed.URL)
	if err != nil {
		if m.isRecording(feed.Name, KindLive) {
			m.setStatus(feed.Name, func(s *feedStatus) {
				s.state = StateRecovering
				s.detail = fmt.Sprintf("Probe failed; keeping active recording: %s", err)
				s.live = true
			})
			return
		}
		if isNotLiveError(err) {
			m.setStatus(feed.Name, func(s *feedStatus) {
				s.state = StateNotLive
				s.detail = "Stream is not live yet"
				s.live = false
				s.lastChecked = time.Now()
			})
			return
		}
		m.setStatus(feed.Name, func(s *feedStatus) {
			s.state = StateError
			s.detail = fmt.Sprintf("Probe failed: %s", err)
			s.live = false
			s.lastChecked = time.Now()
		})
		m.log.Warn(fmt.Sprintf("[YouTube %s] Probe failed: %s", feed.Name, err))
		return
	}

	live := meta.IsLive || meta.LiveStatus == "is_live"
	detail := statusDetail(meta)
	if live {
		m.setStatus(feed.Name, func(s *feedStatus) {
			s.state = StateRecording
			s.detail = detail
			s.live = true
			s.lastChecked = time.Now()
		})
		if err := m.startRecording(feed, KindLive); err != nil {
			m.setStatus(feed.Name, func(s *feedStatus) {
				s.state = StateError
				s.detail = err.Error()
			})
			m.log.Error(fmt.Sprintf("[YouTube %s] %s", feed.Name, err))
		}
		return
	}

	if m.isRecording(feed.Name, KindLive) {
		m.log.Info(fmt.Sprintf("[YouTube %s] Live stream ended. Stopping recording.", feed.Name))
		m.stopRecording(feed.Name)
	}

	if startup && !m.trailerDone(feed.Name) && !m.isRecording(feed.Name, KindTrailer) {
		m.setStatus(feed.Name, func(s *feedStatus) {
			s.state = StateTrailer
			s.detail = "Recording startup trailer"
			s.live = false
			s.lastChecked = time.Now()
		})
		if err := m.startRecording(feed, KindTrailer); err != nil {
			m.setStatus(feed.Name, func(s *feedStatus) {
				s.state = StateWaiting
				s.detail = fmt.Sprintf("Trailer unavailable; waiting for live stream: %s", err)
				s.trailerDone = true
			})
			m.log.Warn(fmt.Sprintf("[YouTube %s] Trailer recording failed: %s", feed.Name, err))
		}
		return
	}

	m.setStatus(feed.Name, func(s *feedStatus) {
		s.state = StateNotLive
		s.detail = detail
		s.live = false
		s.lastChecked = time.Now()
	})
}

func isNotLiveError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	needles := []string{
		"not currently live",
		"not live",
		"not started",
		"has not started",
		"will begin",
		"premieres in",
		"premiere will begin",
		"live event will begin",
		"livestream will begin",
		"this live event",
		"upcoming",
	}
	for _, needle := range needles {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

func statusDetail(meta metadata) string {
	title := strings.TrimSpace(meta.Title)
	state := strings.TrimSpace(meta.LiveStatus)
	switch {
	case title != "" && state != "":
		return fmt.Sprintf("%s (%s)", title, state)
	case title != "":
		return title
	case state != "":
		return state
	default:
		return "Waiting for live stream"
	}
}

func (m *Manager) probe(ctx context.Context, rawURL string) (metadata, error) {
	probeCtx, cancel := context.WithTimeout(ctx, probeLimit)
	defer cancel()

	args := []string{"--dump-single-json", "--skip-download", "--no-warnings", rawURL}
	cmd := exec.CommandContext(probeCtx, m.paths.YtDLP, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
		return metadata{}, errors.New("yt-dlp probe timed out")
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return metadata{}, errors.New(msg)
	}
	var meta metadata
	if err := json.Unmarshal(out, &meta); err != nil {
		return metadata{}, fmt.Errorf("parse yt-dlp metadata: %w", err)
	}
	return meta, nil
}

func (m *Manager) startRecording(feed Feed, kind string) error {
	if m.dir == "" {
		return errors.New("recordings directory is empty")
	}
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return fmt.Errorf("create recordings dir: %w", err)
	}

	mode := m.modeFor(feed.Name)
	if mode == ModeNone {
		return nil
	}

	m.mu.Lock()
	if cur := m.active[feed.Name]; cur != nil {
		if cur.kind == kind && cur.mode == mode {
			m.mu.Unlock()
			return nil
		}
		cur.markStopping()
		_ = cur.interruptAll()
		done := cur.done
		waitForFinalization := cur.kind == kind
		m.mu.Unlock()
		if waitForFinalization {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				return errors.New("previous recording is still finalizing")
			}
		}
	} else {
		m.mu.Unlock()
	}

	started := time.Now().UTC()
	path := ""
	if mode.recordsVideo() {
		path = filepath.Join(m.dir, videoFileName(feed.Name, kind, started, m.group, m.video.extension))
	}
	audioPath := ""
	if mode.recordsAudio() {
		audioPath = filepath.Join(m.dir, audioFileName(feed.Name, kind, started, m.group))
	}

	rec := &recording{
		feed:      feed.Name,
		kind:      kind,
		mode:      mode,
		path:      path,
		audioPath: audioPath,
		startedAt: started,
		lastCheck: time.Now(),
		done:      make(chan struct{}),
	}

	var stdout, stderr io.Reader
	if mode.recordsVideo() {
		args := []string{
			"--no-part",
			"--newline",
			"-f", m.video.format,
			"--merge-output-format", m.video.container,
		}
		if kind == KindLive {
			args = append(args, "--hls-use-mpegts")
		}
		if m.paths.FFmpegDir != "" {
			args = append(args, "--ffmpeg-location", m.paths.FFmpegDir)
		}
		args = append(args, "-o", path, feed.URL)

		cmd := exec.Command(m.paths.YtDLP, args...)
		prepareCommand(cmd)
		var err error
		stdout, err = cmd.StdoutPipe()
		if err != nil {
			return fmt.Errorf("yt-dlp stdout: %w", err)
		}
		stderr, err = cmd.StderrPipe()
		if err != nil {
			return fmt.Errorf("yt-dlp stderr: %w", err)
		}
		rec.cmd = cmd
	}

	m.mu.Lock()
	m.active[feed.Name] = rec
	m.mu.Unlock()

	if rec.cmd != nil {
		if err := rec.cmd.Start(); err != nil {
			m.removeActive(feed.Name, rec)
			return fmt.Errorf("start yt-dlp: %w", err)
		}
	}
	if mode.recordsAudio() {
		if err := m.startAudioRecording(feed, rec); err != nil {
			if !mode.recordsVideo() {
				m.removeActive(feed.Name, rec)
				rec.kill()
				return fmt.Errorf("start MP3 recording: %w", err)
			}
			m.log.Warn(fmt.Sprintf("[YouTube %s] MP3 recording unavailable: %s", feed.Name, err))
		}
	}

	m.setStatus(feed.Name, func(s *feedStatus) {
		s.state = StateRecording
		if kind == KindTrailer {
			s.state = StateTrailer
		}
		s.detail = m.recordingDetail(kind, mode)
		s.path = rec.outputPath()
		s.mode = mode
		s.recording = true
	})
	m.log.Info(fmt.Sprintf("[YouTube %s] %s.", feed.Name, m.recordingDetail(kind, mode)))
	if rec.audioFFmpeg != nil {
		m.log.Info(fmt.Sprintf("[YouTube %s] Recording separate MP3 at %s.", feed.Name, mp3Bitrate))
	}

	if stdout != nil {
		go m.drain(rec, stdout)
	}
	if stderr != nil {
		go m.drain(rec, stderr)
	}

	m.wg.Add(1)
	go m.waitRecording(rec)
	return nil
}

func (m *Manager) recordingDetail(kind string, mode RecordMode) string {
	switch mode {
	case ModeAudio:
		return fmt.Sprintf("Recording %s MP3 only at %s", strings.ToLower(kind), mp3Bitrate)
	default:
		return fmt.Sprintf("Recording %s video + MP3 at %s", strings.ToLower(kind), m.video.description)
	}
}

func (m *Manager) startAudioRecording(feed Feed, rec *recording) error {
	ytdlpArgs := []string{"--no-part", "-f", formatAudio}
	if rec.kind == KindLive {
		ytdlpArgs = append(ytdlpArgs, "--hls-use-mpegts")
	}
	if m.paths.FFmpegDir != "" {
		ytdlpArgs = append(ytdlpArgs, "--ffmpeg-location", m.paths.FFmpegDir)
	}
	ytdlpArgs = append(ytdlpArgs, "-o", "-", feed.URL)

	ffmpegArgs := []string{
		"-hide_banner", "-nostdin", "-loglevel", "error",
		"-i", "pipe:0",
		"-vn",
		"-c:a", "libmp3lame", "-b:a", mp3Bitrate,
		"-f", "mp3", rec.audioPath,
	}

	ytdlp := exec.Command(m.paths.YtDLP, ytdlpArgs...)
	ffmpeg := exec.Command(m.paths.FFmpeg, ffmpegArgs...)
	prepareCommand(ytdlp)
	prepareCommand(ffmpeg)

	pipeR, pipeW, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("audio pipe: %w", err)
	}
	ytdlp.Stdout = pipeW
	ffmpeg.Stdin = pipeR

	ytdlpStderr, err := ytdlp.StderrPipe()
	if err != nil {
		pipeR.Close()
		pipeW.Close()
		return fmt.Errorf("audio yt-dlp stderr: %w", err)
	}
	ffmpegStderr, err := ffmpeg.StderrPipe()
	if err != nil {
		pipeR.Close()
		pipeW.Close()
		ytdlpStderr.Close()
		return fmt.Errorf("audio ffmpeg stderr: %w", err)
	}

	if err := ytdlp.Start(); err != nil {
		pipeR.Close()
		pipeW.Close()
		ytdlpStderr.Close()
		ffmpegStderr.Close()
		return fmt.Errorf("start audio yt-dlp: %w", err)
	}
	if err := ffmpeg.Start(); err != nil {
		_ = ytdlp.Process.Kill()
		pipeR.Close()
		pipeW.Close()
		ytdlpStderr.Close()
		ffmpegStderr.Close()
		return fmt.Errorf("start audio ffmpeg: %w", err)
	}

	rec.mu.Lock()
	rec.audioYtdlp = ytdlp
	rec.audioFFmpeg = ffmpeg
	rec.audioPipeR = pipeR
	rec.audioPipeW = pipeW
	rec.mu.Unlock()

	go m.drain(rec, ytdlpStderr)
	go m.drain(rec, ffmpegStderr)
	return nil
}

func videoFileName(feedName, kind string, t time.Time, group, ext string) string {
	feed := util.Alnum(feedName)
	if feed == "" {
		feed = "YOUTUBE"
	}
	grp := util.Alnum(group)
	if grp == "" {
		grp = "anonymous"
	}
	if ext == "" {
		ext = "mkv"
	}
	return fmt.Sprintf("DEFQON.1.%d.YOUTUBE.%s.%s.%s.%s.BEST-%s.%s",
		t.Year(), strings.ToUpper(feed), t.Format("20060102"), t.Format("1504"), kind, grp, ext)
}

func audioFileName(feedName, kind string, t time.Time, group string) string {
	feed := util.Alnum(feedName)
	if feed == "" {
		feed = "YOUTUBE"
	}
	grp := util.Alnum(group)
	if grp == "" {
		grp = "anonymous"
	}
	return fmt.Sprintf("DEFQON.1.%d.YOUTUBE.%s.%s.%s.%s.MP3-%s.mp3",
		t.Year(), strings.ToUpper(feed), t.Format("20060102"), t.Format("1504"), kind, grp)
}

func (m *Manager) drain(rec *recording, r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || rec.isStopping() {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "error") || strings.Contains(lower, "warning") {
			m.log.Warn(fmt.Sprintf("[YouTube %s] %s", rec.feed, line))
		}
	}
}

func (m *Manager) waitRecording(rec *recording) {
	defer m.wg.Done()
	defer close(rec.done)

	var err error
	if rec.cmd != nil {
		err = rec.cmd.Wait()
	}
	audioErr := rec.waitAudio()
	stopped := rec.isStopping() || atomic.LoadInt32(&m.stopped) != 0
	m.removeActive(rec.feed, rec)

	finished := false
	if rec.path != "" {
		if info, statErr := os.Stat(rec.path); statErr == nil {
			if info.Size() == 0 {
				_ = os.Remove(rec.path)
			} else {
				finished = true
			}
		}
	}
	audioFinished := false
	if rec.audioPath != "" {
		if info, statErr := os.Stat(rec.audioPath); statErr == nil {
			if info.Size() == 0 {
				_ = os.Remove(rec.audioPath)
			} else {
				audioFinished = true
			}
		}
	}

	if err != nil && !stopped {
		m.log.Warn(fmt.Sprintf("[YouTube %s] %s recording exited: %s", rec.feed, strings.ToLower(rec.kind), err))
		m.setStatus(rec.feed, func(s *feedStatus) {
			s.state = StateRecovering
			s.detail = "Recording stopped unexpectedly; will retry on next live check"
			s.recording = false
		})
		return
	}
	if audioErr != nil && !stopped {
		m.log.Warn(fmt.Sprintf("[YouTube %s] MP3 recording exited: %s", rec.feed, audioErr))
		if !rec.mode.recordsVideo() {
			m.setStatus(rec.feed, func(s *feedStatus) {
				s.state = StateRecovering
				s.detail = "MP3 recording stopped unexpectedly; will retry on next live check"
				s.recording = false
			})
			return
		}
	}
	anyFinished := finished || audioFinished
	currentMode := m.modeFor(rec.feed)

	if rec.kind == KindTrailer {
		m.setStatus(rec.feed, func(s *feedStatus) {
			s.trailerDone = true
			s.recording = false
			s.mode = currentMode
			if currentMode == ModeNone {
				s.state = StateDisabled
				s.detail = "YouTube recording disabled for this feed"
				s.path = ""
			} else if anyFinished {
				s.state = StateWaiting
				s.detail = "Startup trailer recorded; waiting for live stream"
			} else {
				s.state = StateWaiting
				s.detail = "Trailer unavailable; waiting for live stream"
			}
		})
		if finished {
			m.log.Info(fmt.Sprintf("[YouTube %s] Trailer recording finished.", rec.feed))
		}
		if audioFinished {
			m.log.Info(fmt.Sprintf("[YouTube %s] Trailer MP3 recording finished.", rec.feed))
		}
		return
	}

	m.setStatus(rec.feed, func(s *feedStatus) {
		s.recording = false
		s.mode = currentMode
		if currentMode == ModeNone {
			s.state = StateDisabled
			s.detail = "YouTube recording disabled for this feed"
			s.path = ""
		} else if anyFinished {
			s.state = StateEnded
			s.detail = "Live recording finished"
		} else {
			s.state = StateWaiting
			s.detail = "Live recording ended without output"
		}
	})
	if finished {
		m.log.Info(fmt.Sprintf("[YouTube %s] Live recording finished.", rec.feed))
	}
	if audioFinished {
		m.log.Info(fmt.Sprintf("[YouTube %s] Live MP3 recording finished.", rec.feed))
	}
}

func (m *Manager) monitorActive(feed string) {
	m.mu.RLock()
	rec := m.active[feed]
	m.mu.RUnlock()
	if rec == nil {
		return
	}

	path := rec.outputPath()
	if path == "" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		return
	}

	rec.mu.Lock()
	stalled := false
	if info.Size() > rec.lastSize {
		rec.lastSize = info.Size()
		rec.lastCheck = time.Now()
	} else if time.Since(rec.lastCheck) > m.stalledAfter {
		stalled = true
	}
	rec.mu.Unlock()

	if stalled {
		m.log.Warn(fmt.Sprintf("[YouTube %s] Recording stalled. Restarting on next check.", feed))
		m.stopRecording(feed)
		m.setStatus(feed, func(s *feedStatus) {
			s.state = StateRecovering
			s.detail = "Recording stalled; retrying"
			s.recording = false
		})
	}
}

func (m *Manager) Snapshot() []Snapshot {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]Snapshot, 0, len(m.feeds))
	previewFeed := ""
	previewFullscreen := false
	if m.previewS != nil {
		previewFeed = m.previewS.feed
		previewFullscreen = m.previewS.fullscreen
	}
	for _, feed := range m.feeds {
		s := m.status[feed.Name]
		if s == nil {
			continue
		}
		_, active := m.active[feed.Name]
		out = append(out, Snapshot{
			Name:        s.name,
			URL:         s.url,
			State:       s.state,
			Detail:      s.detail,
			Path:        s.path,
			Mode:        s.mode,
			Recording:   s.recording || active,
			Live:        s.live,
			TrailerDone: s.trailerDone,
			Previewing:  previewFeed == feed.Name,
			Fullscreen:  previewFeed == feed.Name && previewFullscreen,
			LastChecked: s.lastChecked,
		})
	}
	return out
}

func (m *Manager) OpenPreview(feedName string, fullscreen bool) error {
	if !m.preview {
		return errors.New("YouTube preview is only enabled on Windows/macOS")
	}
	feed, ok := m.byName[feedName]
	if !ok {
		return fmt.Errorf("unknown YouTube feed %q", feedName)
	}

	args := []string{
		"--force-window=immediate",
		"--no-terminal",
		"--ytdl-format=" + m.video.format,
		"--geometry=1280x720",
	}
	if fullscreen {
		args = append(args, "--fs")
	} else {
		args = append(args, "--autofit=1280x720")
	}
	args = append(args, feed.URL)

	cmd := exec.Command(m.paths.MPV, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start mpv: %w", err)
	}

	s := &previewSession{
		feed:       feed.Name,
		cmd:        cmd,
		fullscreen: fullscreen,
		done:       make(chan struct{}),
	}

	m.mu.Lock()
	old := m.previewS
	m.previewS = s
	m.mu.Unlock()
	if old != nil {
		old.stop()
	}

	go m.waitPreview(s)
	if fullscreen {
		m.log.Info(fmt.Sprintf("[YouTube %s] mpv preview opened fullscreen at %s.", feed.Name, m.video.description))
	} else {
		m.log.Info(fmt.Sprintf("[YouTube %s] mpv preview opened at %s.", feed.Name, m.video.description))
	}
	return nil
}

func (m *Manager) StopPreview() {
	if m == nil {
		return
	}
	m.mu.Lock()
	s := m.previewS
	m.previewS = nil
	m.mu.Unlock()
	if s != nil {
		s.stop()
		m.log.Info("YouTube mpv preview stopped.")
	}
}

func (m *Manager) waitPreview(s *previewSession) {
	_ = s.cmd.Wait()
	close(s.done)
	m.mu.Lock()
	if m.previewS == s {
		m.previewS = nil
	}
	m.mu.Unlock()
}

func (m *Manager) StopAll(timeout time.Duration) {
	if m == nil {
		return
	}
	atomic.StoreInt32(&m.stopped, 1)
	m.StopPreview()

	m.mu.RLock()
	feeds := make([]string, 0, len(m.active))
	for feed := range m.active {
		feeds = append(feeds, feed)
	}
	m.mu.RUnlock()
	for _, feed := range feeds {
		m.stopRecording(feed)
	}
	if len(feeds) > 0 {
		msg := fmt.Sprintf("Finalizing %d YouTube recording(s). This can take a few seconds...", len(feeds))
		m.log.Info(msg)
		fmt.Fprintln(os.Stderr, msg)
	}

	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		m.log.Warn("YouTube recording finalization timed out; forcing recorder processes to close.")
		fmt.Fprintln(os.Stderr, "YouTube recording finalization timed out; forcing recorder processes to close.")
		m.forceKillAll()
		<-done
	}
}

func (m *Manager) stopRecording(feed string) {
	m.mu.RLock()
	rec := m.active[feed]
	m.mu.RUnlock()
	if rec == nil {
		return
	}
	rec.markStopping()
	msg := fmt.Sprintf("[YouTube %s] Stopping recording; finalizing %s...", feed, rec.mediaDescription())
	m.log.Info(msg)
	m.setStatus(feed, func(s *feedStatus) {
		s.detail = fmt.Sprintf("Stopping recording; finalizing %s", rec.mediaDescription())
		s.recording = true
	})
	if err := rec.interruptAll(); err != nil {
		rec.kill()
	}
}

func (m *Manager) forceKillAll() {
	m.mu.RLock()
	recs := make([]*recording, 0, len(m.active))
	for _, rec := range m.active {
		recs = append(recs, rec)
	}
	m.mu.RUnlock()
	for _, rec := range recs {
		rec.kill()
	}
}

func (m *Manager) isRecording(feed, kind string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rec := m.active[feed]
	return rec != nil && (kind == "" || rec.kind == kind)
}

func (m *Manager) trailerDone(feed string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := m.status[feed]
	return s != nil && s.trailerDone
}

func (m *Manager) removeActive(feed string, rec *recording) {
	m.mu.Lock()
	if m.active[feed] == rec {
		delete(m.active, feed)
	}
	m.mu.Unlock()
}

func (m *Manager) setStatus(feed string, fn func(*feedStatus)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.status[feed]
	if s == nil {
		return
	}
	fn(s)
}

func (r *recording) markStopping() {
	r.mu.Lock()
	r.stopping = true
	r.mu.Unlock()
}

func (r *recording) isStopping() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stopping
}

func (r *recording) outputPath() string {
	if r.path != "" {
		return r.path
	}
	return r.audioPath
}

func (r *recording) mediaDescription() string {
	if r.path != "" && r.audioPath != "" {
		return "MKV and MP3"
	}
	if r.path != "" {
		return "MKV"
	}
	if r.audioPath != "" {
		return "MP3"
	}
	return "recording"
}

func (r *recording) interruptAll() error {
	var first error
	r.mu.Lock()
	cmds := []*exec.Cmd{r.cmd, r.audioYtdlp, r.audioFFmpeg}
	r.mu.Unlock()
	for _, cmd := range cmds {
		if err := interruptCommand(cmd); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (r *recording) waitAudio() error {
	r.mu.Lock()
	ytdlp := r.audioYtdlp
	ffmpeg := r.audioFFmpeg
	pipeR := r.audioPipeR
	pipeW := r.audioPipeW
	r.mu.Unlock()

	if ytdlp == nil && ffmpeg == nil {
		return nil
	}
	if r.cmd != nil && !r.isStopping() && r.kind == KindLive {
		_ = interruptCommand(ytdlp)
		_ = interruptCommand(ffmpeg)
	}

	var first error
	if ytdlp != nil {
		if err := ytdlp.Wait(); err != nil {
			first = err
		}
	}
	if pipeW != nil {
		_ = pipeW.Close()
	}
	if ffmpeg != nil {
		if err := ffmpeg.Wait(); err != nil && first == nil {
			first = err
		}
	}
	if pipeR != nil {
		_ = pipeR.Close()
	}
	return first
}

func (r *recording) kill() {
	r.markStopping()
	if r.cmd != nil && r.cmd.Process != nil {
		_ = r.cmd.Process.Kill()
	}
	if r.audioYtdlp != nil && r.audioYtdlp.Process != nil {
		_ = r.audioYtdlp.Process.Kill()
	}
	if r.audioFFmpeg != nil && r.audioFFmpeg.Process != nil {
		_ = r.audioFFmpeg.Process.Kill()
	}
}

func (s *previewSession) stop() {
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	select {
	case <-s.done:
	case <-time.After(2 * time.Second):
	}
}
