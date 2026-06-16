#!/usr/bin/env bash
#
# Downloads bundled yt-dlp + ffmpeg for a given target platform into a directory.
# The resulting files are placed next to the recorder binary so it can use them
# without any system-installed copies.
#
# Usage: scripts/fetch-tools.sh <os> <arch> <dest_dir>
#
set -euo pipefail

OS="${1:?missing os (darwin|linux|windows)}"
ARCH="${2:?missing arch (amd64|arm64)}"
DEST="${3:?missing destination directory}"

YTDLP_BASE="https://github.com/yt-dlp/yt-dlp/releases/latest/download"
FFMPEG_BASE="https://github.com/eugeneware/ffmpeg-static/releases/download/b6.1.1"
BTBN_BASE="https://github.com/BtbN/FFmpeg-Builds/releases/download/latest"

mkdir -p "$DEST"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

dl() { curl -fsSL --max-time 180 -o "$2" "$1"; }

# --- yt-dlp ------------------------------------------------------------------
case "$OS-$ARCH" in
	linux-amd64) ytdlp_asset="yt-dlp_linux" ;;
	linux-arm64) ytdlp_asset="yt-dlp_linux_aarch64" ;;
	darwin-*)    ytdlp_asset="yt-dlp_macos" ;;   # universal binary
	windows-*)   ytdlp_asset="yt-dlp.exe" ;;     # x64 (runs on arm64 via emulation)
	*) echo "unsupported yt-dlp target: $OS-$ARCH" >&2; exit 1 ;;
esac
ytdlp_out="yt-dlp"; [ "$OS" = windows ] && ytdlp_out="yt-dlp.exe"
echo "  -> yt-dlp  ($ytdlp_asset)"
dl "$YTDLP_BASE/$ytdlp_asset" "$DEST/$ytdlp_out"
chmod +x "$DEST/$ytdlp_out" 2>/dev/null || true

# --- ffmpeg ------------------------------------------------------------------
ff_out="ffmpeg"; [ "$OS" = windows ] && ff_out="ffmpeg.exe"
ff_asset=""
case "$OS-$ARCH" in
	darwin-arm64) ff_asset="ffmpeg-darwin-arm64" ;;
	darwin-amd64) ff_asset="ffmpeg-darwin-x64" ;;
	linux-amd64)  ff_asset="ffmpeg-linux-x64" ;;
	linux-arm64)  ff_asset="ffmpeg-linux-arm64" ;;
	windows-amd64) ff_asset="ffmpeg-win32-x64" ;;
esac

if [ -n "$ff_asset" ]; then
	echo "  -> ffmpeg (ffmpeg-static $ff_asset)"
	dl "$FFMPEG_BASE/$ff_asset" "$DEST/$ff_out"
	chmod +x "$DEST/$ff_out" 2>/dev/null || true
elif [ "$OS-$ARCH" = "windows-arm64" ]; then
	echo "  -> ffmpeg (BtbN winarm64)"
	dl "$BTBN_BASE/ffmpeg-master-latest-winarm64-gpl.zip" "$tmp/ff.zip"
	unzip -o "$tmp/ff.zip" '*/bin/ffmpeg.exe' -d "$tmp" >/dev/null
	bin="$(find "$tmp" -name ffmpeg.exe -type f | head -1)"
	[ -n "$bin" ] || { echo "ffmpeg.exe not found in BtbN archive" >&2; exit 1; }
	mv "$bin" "$DEST/$ff_out"
else
	echo "no ffmpeg source for $OS-$ARCH" >&2
	exit 1
fi

# --- macOS: ad-hoc sign so unsigned arm64 binaries can execute ---------------
if [ "$OS" = darwin ] && command -v codesign >/dev/null 2>&1; then
	codesign --force -s - "$DEST/$ytdlp_out" >/dev/null 2>&1 || true
	codesign --force -s - "$DEST/$ff_out" >/dev/null 2>&1 || true
fi

echo "  -> bundled into $DEST"
ls -lh "$DEST"
