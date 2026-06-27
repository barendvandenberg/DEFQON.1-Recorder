package config

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

type YouTubeFeed struct {
	Name string
	URL  string
}

type Config struct {
	APIBaseURL           string
	Channels             []string
	RecordingBlacklist   []string
	RecordingsDir        string
	TimetablePath        string
	ToolsDir             string
	PreferencesPath      string
	SplitSets            bool
	ID3Album             string
	ID3Genre             string
	CheckInterval        time.Duration
	StalledCheckInterval time.Duration
	StalledTimeout       time.Duration
	TUIUpdateInterval    time.Duration
	YouTubeEnabled       bool
	YouTubeFeeds         []YouTubeFeed
	YouTubePollInterval  time.Duration
	YouTubeStalledAfter  time.Duration
}

func Default() Config {
	channels := []string{
		"defqon-1-magenta", // DQ.1 Magenta
		"defqon1purple",    // DQ.1 Purple
		"defqon1white",     // DQ.1 White
		"defqon-1-brown",   // DQ.1 Brown
		"defqon1pink",      // DQ.1 Pink
		"defqon1blue",      // DQ.1 Blue
		"defqon1indigo",    // DQ.1 Indigo
		"defqon1yellow",    // DQ.1 Yellow
		"defqon1orange",    // DQ.1 Orange
		"defqon1silver",    // DQ.1 Silver
		"defqon1green",     // DQ.1 Green
		"defqon1gold",      // DQ.1 Gold
		"defqon1black",     // DQ.1 Black
		"defqon1uv",        // DQ.1 UV
	}
	channels = appendEnvChannels(channels, "TEST_MIXLR_CHANNEL")

	return Config{
		APIBaseURL:           "https://apicdn.mixlr.com/v3/channel_view/",
		Channels:             channels,
		RecordingBlacklist:   parseCSV(os.Getenv("RECORDING_BLACKLIST")),
		RecordingsDir:        envOr("RECORDINGS_DIR", "recordings"),
		TimetablePath:        envOr("TIMETABLE_PATH", "dq-timetable.json"),
		ToolsDir:             envOr("TOOLS_DIR", exeDir()),
		PreferencesPath:      envOr("PREFERENCES_PATH", "recorder.ini"),
		SplitSets:            envBoolOr("SPLIT_SETS", true),
		ID3Album:             envOr("ID3_ALBUM", "DEFQON.1"),
		ID3Genre:             envOr("ID3_GENRE", "Hardstyle"),
		CheckInterval:        envDurationMSOr("CHECK_INTERVAL_MS", 60_000*time.Millisecond),
		StalledCheckInterval: 30 * time.Second,
		StalledTimeout:       60 * time.Second,
		TUIUpdateInterval:    envDurationMSOr("TUI_UPDATE_INTERVAL_MS", 2_000*time.Millisecond),
		YouTubeEnabled:       envBoolOr("YOUTUBE_ENABLED", runtime.GOOS != "linux"),
		YouTubeFeeds: []YouTubeFeed{
			{Name: "Friday", URL: "https://www.youtube.com/watch?v=tY4BNcXezb0"},
			{Name: "Saturday", URL: "https://www.youtube.com/watch?v=W8h8JMNjQ1E"},
			{Name: "Sunday", URL: "https://www.youtube.com/watch?v=AkNo5ckWTBk"},
			// Temporary live test feed; remove this entry after validation.
			{Name: "Test", URL: "https://www.youtube.com/watch?v=Qnkxm62y8zM"},
		},
		YouTubePollInterval: envDurationMSOr("YOUTUBE_POLL_INTERVAL_MS", 30_000*time.Millisecond),
		YouTubeStalledAfter: envDurationMSOr("YOUTUBE_STALLED_TIMEOUT_MS", 90_000*time.Millisecond),
	}
}

func appendEnvChannels(channels []string, key string) []string {
	for _, raw := range strings.Split(os.Getenv(key), ",") {
		channel := strings.TrimSpace(raw)
		if channel == "" || slices.Contains(channels, channel) {
			continue
		}
		channels = append(channels, channel)
	}
	return channels
}

// parseCSV splits a comma-separated env value into trimmed, non-empty entries.
func parseCSV(env string) []string {
	var out []string
	for _, raw := range strings.Split(env, ",") {
		if s := strings.TrimSpace(raw); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// envBoolOr reads a boolean env var. Accepts 1/true/yes/on (case-insensitive);
// anything else (including unset) returns the default.
func envBoolOr(key string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
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
