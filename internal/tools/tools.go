package tools

import (
	"os"
	"path/filepath"
	"runtime"
)

// Paths holds the resolved locations of the external recording tools.
type Paths struct {
	// YtDLP is the yt-dlp executable to run. Defaults to "yt-dlp" (PATH lookup)
	// when no bundled copy is found.
	YtDLP string
	// FFmpeg is the ffmpeg executable used for live MP3 transcoding. Defaults
	// to "ffmpeg" (PATH lookup) when no bundled copy is found.
	FFmpeg string
	// MPV is the mpv executable used for optional YouTube preview playback.
	// Defaults to "mpv" (PATH lookup) when no bundled copy is found.
	MPV string
	// FFmpegDir is a directory passed to yt-dlp via --ffmpeg-location, or empty
	// to let yt-dlp fall back to PATH.
	FFmpegDir string
}

// Resolve looks for bundled yt-dlp and ffmpeg binaries inside dir (the
// directory next to the application executable). Missing tools fall back to
// PATH so the app keeps working with system-installed copies.
func Resolve(dir string) Paths {
	p := Paths{
		YtDLP:  ytDLPName(),
		FFmpeg: ffmpegName(),
		MPV:    mpvName(),
	}
	if dir == "" {
		return p
	}
	if exe := filepath.Join(dir, ytDLPName()); isExecutable(exe) {
		p.YtDLP = exe
	}
	if exe := filepath.Join(dir, ffmpegName()); isExecutable(exe) {
		p.FFmpeg = exe
		p.FFmpegDir = dir
	}
	if exe := filepath.Join(dir, mpvName()); isExecutable(exe) {
		p.MPV = exe
	}
	if runtime.GOOS == "darwin" {
		if exe := filepath.Join(dir, "mpv.app", "Contents", "MacOS", "mpv"); isExecutable(exe) {
			p.MPV = exe
		}
	}
	return p
}

func ytDLPName() string {
	if runtime.GOOS == "windows" {
		return "yt-dlp.exe"
	}
	return "yt-dlp"
}

func ffmpegName() string {
	if runtime.GOOS == "windows" {
		return "ffmpeg.exe"
	}
	return "ffmpeg"
}

func mpvName() string {
	if runtime.GOOS == "windows" {
		return "mpv.exe"
	}
	return "mpv"
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return true
}
