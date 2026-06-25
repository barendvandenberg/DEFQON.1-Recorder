package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/revunix/defqon1-recorder/internal/config"
	"github.com/revunix/defqon1-recorder/internal/controller"
	"github.com/revunix/defqon1-recorder/internal/mixlr"
	"github.com/revunix/defqon1-recorder/internal/prefs"
	"github.com/revunix/defqon1-recorder/internal/recorder"
	"github.com/revunix/defqon1-recorder/internal/split"
	"github.com/revunix/defqon1-recorder/internal/status"
	"github.com/revunix/defqon1-recorder/internal/timetable"
	"github.com/revunix/defqon1-recorder/internal/tools"
	"github.com/revunix/defqon1-recorder/internal/tui"
	"github.com/revunix/defqon1-recorder/internal/util"
	"github.com/revunix/defqon1-recorder/internal/youtube"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	cfg := config.Default()

	logCh := make(chan tui.LogMessage, 2048)
	logger := tui.NewLogger(logCh)

	if err := os.MkdirAll(cfg.RecordingsDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "cannot create recordings dir: %s\n", err)
		os.Exit(1)
	}
	if abs, err := filepath.Abs(cfg.RecordingsDir); err == nil {
		logger.Info(fmt.Sprintf("Recordings saved in: %s", abs))
	}

	tt, err := timetable.Load(cfg.TimetablePath)
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to load timetable: %s", err))
		tt = &timetable.Timetable{}
	} else {
		logger.Info(fmt.Sprintf("Timetable loaded with %d sets.", tt.Size()))
	}

	client := mixlr.New(cfg.APIBaseURL)
	group := util.SystemUser()
	var post recorder.PostRecorder
	if cfg.SplitSets {
		post = split.New(tools.Resolve(cfg.ToolsDir).FFmpeg, cfg.RecordingsDir, group, cfg.ID3Album, cfg.ID3Genre, tt, logger)
	}
	rec := recorder.New(cfg.RecordingsDir, cfg.StalledTimeout, cfg.ToolsDir, group, post, logger)
	reg := status.New(cfg.Channels)

	// Resolve which channels may be recorded: start from all-enabled, apply the
	// RECORDING_BLACKLIST defaults, then let the persisted INI win so the user's
	// last toggles survive a restart.
	store := prefs.New(cfg.PreferencesPath)
	savedPrefs, err := store.LoadAll()
	if err != nil {
		logger.Warn(fmt.Sprintf("preferences: %s; starting with defaults", err))
		savedPrefs = prefs.Preferences{
			Recording: map[string]bool{},
			YouTube:   map[string]string{},
		}
	}
	saved := savedPrefs.Recording
	enabled := make(map[string]bool, len(cfg.Channels))
	for _, ch := range cfg.Channels {
		enabled[ch] = true
	}
	for _, ch := range cfg.RecordingBlacklist {
		enabled[ch] = false
	}
	for ch, on := range saved {
		enabled[ch] = on
	}

	ytFeeds := make([]youtube.Feed, 0, len(cfg.YouTubeFeeds))
	ytModes := make(map[string]youtube.RecordMode, len(cfg.YouTubeFeeds))
	prefsChanged := false
	if len(savedPrefs.Recording) == 0 {
		savedPrefs.Recording = enabled
		prefsChanged = true
	}
	if savedPrefs.YouTube == nil {
		savedPrefs.YouTube = map[string]string{}
	}
	for _, feed := range cfg.YouTubeFeeds {
		ytFeeds = append(ytFeeds, youtube.Feed{Name: feed.Name, URL: feed.URL})
		mode := youtube.ModeVideoAudio
		if raw, ok := savedPrefs.YouTube[feed.Name]; ok {
			mode = youtube.ParseRecordMode(raw)
		} else {
			savedPrefs.YouTube[feed.Name] = string(mode)
			prefsChanged = true
		}
		ytModes[feed.Name] = mode
	}
	// Seed missing preferences so the user has a visible template.
	if prefsChanged {
		if err := store.SavePreferences(savedPrefs); err != nil {
			logger.Warn(fmt.Sprintf("preferences: cannot write %s: %s", cfg.PreferencesPath, err))
		}
	}

	yt := youtube.New(youtube.Config{
		Enabled:       cfg.YouTubeEnabled,
		Feeds:         ytFeeds,
		Modes:         ytModes,
		RecordingsDir: cfg.RecordingsDir,
		ToolsDir:      cfg.ToolsDir,
		Group:         group,
		PollInterval:  cfg.YouTubePollInterval,
		StalledAfter:  cfg.YouTubeStalledAfter,
		Persister:     store,
	}, logger)

	ctrl := controller.New(client, rec, tt, reg, logger, cfg.Channels, enabled, store)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ui := tui.New(cfg, rec, ctrl, tt, reg, yt, logCh, logger)

	go ctrl.Run(ctx, cfg.CheckInterval, cfg.StalledCheckInterval)
	go yt.Run(ctx)
	go ui.RunRefresh(ctx)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
		ui.Stop()
	}()

	logger.Info(fmt.Sprintf("--- Starting Stream Recorder (v%s) ---", version))

	if err := ui.Run(); err != nil {
		logger.Error(fmt.Sprintf("UI error: %s", err))
	}

	cancel()
	ui.Close()
	yt.StopAll(10 * time.Second)
	rec.StopAll(10 * time.Second)
}
