package config

import (
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type Config struct {
	APIBaseURL           string
	Channels             []string
	RecordingsDir        string
	TimetablePath        string
	ToolsDir             string
	CheckInterval        time.Duration
	StalledCheckInterval time.Duration
	StalledTimeout       time.Duration
	TUIUpdateInterval    time.Duration
}

func Default() Config {
	return Config{
		APIBaseURL: "https://apicdn.mixlr.com/v3/channel_view/",
		Channels: []string{
			"defqon-1-magenta", "defqon1purple", "defqon1white", "defqon-1-brown",
			"defqon1pink", "defqon1blue", "defqon1indigo", "defqon1yellow",
			"defqon1orange", "defqon1silver", "defqon1green", "defqon1gold",
			"defqon1black", "defqon1uv",
		},
		RecordingsDir:        envOr("RECORDINGS_DIR", "recordings"),
		TimetablePath:        envOr("TIMETABLE_PATH", "dq-timetable.json"),
		ToolsDir:             envOr("TOOLS_DIR", exeDir()),
		CheckInterval:        envDurationMSOr("CHECK_INTERVAL_MS", 60_000*time.Millisecond),
		StalledCheckInterval: 30 * time.Second,
		StalledTimeout:       60 * time.Second,
		TUIUpdateInterval:    envDurationMSOr("TUI_UPDATE_INTERVAL_MS", 2_000*time.Millisecond),
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// exeDir returns the directory of the running executable, used to locate
// bundled yt-dlp / ffmpeg binaries.
func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Dir(exe)
}

func envDurationMSOr(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	ms, err := strconv.Atoi(v)
	if err != nil || ms <= 0 {
		return def
	}
	return time.Duration(ms) * time.Millisecond
}
