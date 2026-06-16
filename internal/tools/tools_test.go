package tools

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveBundled(t *testing.T) {
	dir := t.TempDir()

	yt := filepath.Join(dir, ytDLPName())
	ff := filepath.Join(dir, ffmpegName())
	if err := os.WriteFile(yt, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ff, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}

	p := Resolve(dir)
	if p.YtDLP != yt {
		t.Errorf("YtDLP = %q, want %q", p.YtDLP, yt)
	}
	if p.FFmpegDir != dir {
		t.Errorf("FFmpegDir = %q, want %q", p.FFmpegDir, dir)
	}
}

func TestResolveFallbackToPath(t *testing.T) {
	// Empty dir -> PATH fallback, ffmpeg dir empty.
	p := Resolve("")
	want := "yt-dlp"
	if runtime.GOOS == "windows" {
		want = "yt-dlp.exe"
	}
	if p.YtDLP != want {
		t.Errorf("YtDLP = %q, want %q", p.YtDLP, want)
	}
	if p.FFmpegDir != "" {
		t.Errorf("FFmpegDir = %q, want empty", p.FFmpegDir)
	}

	// Existing dir without the binaries -> still PATH fallback.
	p = Resolve(t.TempDir())
	if p.YtDLP != want {
		t.Errorf("YtDLP = %q, want %q", p.YtDLP, want)
	}
	if p.FFmpegDir != "" {
		t.Errorf("FFmpegDir = %q, want empty", p.FFmpegDir)
	}
}
