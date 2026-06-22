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
	// FFmpegDir is a directory passed to yt-dlp via --ffmpeg-location, or empty
	// to let yt-dlp fall back to PATH.
	FFmpegDir string
}

// Resolve looks for bundled yt-dlp and ffmpeg binaries inside dir (the
// directory next to the application executable). Missing tools fall back to
// PATH so the app keeps working with system-installed copies.
func Resolve(dir string) Paths {
	p := Paths{
		YtDLP:   ytDLPName(),
		FFmpeg:  ffmpegName(),
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

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return true
}
