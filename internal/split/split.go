// Package split performs post-recording processing: it cuts a raw recording
// into per-set MP3 files (one folder per stage), each tagged with ID3 metadata
// and the channel artwork. The raw recording file is never modified.
package split

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/revunix/defqon1-recorder/internal/genre"
	"github.com/revunix/defqon1-recorder/internal/logging"
	"github.com/revunix/defqon1-recorder/internal/timetable"
	"github.com/revunix/defqon1-recorder/internal/util"
)

// segment is the planned cut for a single set within a recording window.
type segment struct {
	set      timetable.Set
	offset   time.Duration
	duration time.Duration
}

// planSets returns the per-set segments that fall inside [started, ended] for
// the given stage. It is a pure function (no I/O) so it can be unit-tested.
func planSets(stage string, started, ended time.Time, tt *timetable.Timetable) []segment {
	if tt == nil || ended.Before(started) {
		return nil
	}
	var segs []segment
	for _, s := range tt.SetsForStage(stage) {
		segStart := s.Start
		if started.After(segStart) {
			segStart = started
		}
		segEnd := s.End
		if ended.Before(segEnd) {
			segEnd = ended
		}
		dur := segEnd.Sub(segStart)
		if dur <= 0 {
			continue
		}
		segs = append(segs, segment{
			set:      s,
			offset:   segStart.Sub(started),
			duration: dur,
		})
	}
	return segs
}

// liveWatcher tracks the real-time cutting of one recording: a cancellable
// context for its watcher goroutine, a done channel closed when the goroutine
// has fully stopped, and the set start times already cut live (so the finish
// handler can cut only the remaining tail).
type liveWatcher struct {
	cancel context.CancelFunc
	done   chan struct{}
	cut    map[time.Time]bool
}

// Splitter cuts finished recordings into tagged per-set files.
type Splitter struct {
	ffmpeg string
	dir    string
	group  string
	album  string
	genre  string
	tt     *timetable.Timetable
	log    logging.Logger

	http    *http.Client
	mu      sync.Mutex
	artwork map[string]string // artwork url -> cached local path

	wmu  sync.Mutex
	live map[string]*liveWatcher // stage -> active live watcher
}

func New(ffmpeg, dir, group, album, genre string, tt *timetable.Timetable, log logging.Logger) *Splitter {
	if log == nil {
		log = logging.Noop{}
	}
	return &Splitter{
		ffmpeg:  ffmpeg,
		dir:     dir,
		group:   group,
		album:   album,
		genre:   genre,
		tt:      tt,
		log:     log,
		http:    &http.Client{Timeout: 15 * time.Second},
		artwork: map[string]string{},
		live:    map[string]*liveWatcher{},
	}
}

// OnRecordingStarted launches a live watcher that cuts each set as soon as its
// end boundary is reached while the recording is still running, so per-set
// files appear in real time. The raw recording is left untouched.
func (s *Splitter) OnRecordingStarted(stage, rawPath string, started time.Time, artworkURL string) {
	cover, err := s.fetchArtwork(artworkURL)
	if err != nil {
		s.log.Warn(fmt.Sprintf("[%s] artwork unavailable: %s (splitting without cover)", stage, err))
		cover = ""
	}

	ctx, cancel := context.WithCancel(context.Background())
	w := &liveWatcher{cancel: cancel, done: make(chan struct{}), cut: map[time.Time]bool{}}
	s.wmu.Lock()
	if old := s.live[stage]; old != nil {
		old.cancel()
		<-old.done
	}
	s.live[stage] = w
	s.wmu.Unlock()

	go s.watchSets(ctx, w, stage, rawPath, started, cover)
}

// watchSets waits for each set boundary (relative to the recording start) and
// cuts the completed set. It returns when the context is cancelled (recording
// stopped); the in-progress tail set is handled by OnRecordingFinished.
func (s *Splitter) watchSets(ctx context.Context, w *liveWatcher, stage, rawPath string, started time.Time, cover string) {
	defer close(w.done)

	// Far-future end so every set from the recording start onward is planned.
	segs := planSets(stage, started, started.Add(100*365*24*time.Hour), s.tt)
	for _, seg := range segs {
		boundary := started.Add(seg.offset + seg.duration)
		if wait := time.Until(boundary); wait > 0 {
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return
			}
		}
		if err := s.cutSegment(rawPath, stage, cover, seg); err != nil {
			s.log.Error(fmt.Sprintf("[%s] live split %s failed: %s", stage, seg.set.DJ, err))
			continue
		}
		w.cut[seg.set.Start] = true
		s.log.Info(fmt.Sprintf("[%s] Live split set: %s (%s)",
			stage, seg.set.DJ, util.FormatDuration(int(seg.duration.Minutes()))))
	}
}

// OnRecordingFinished stops the live watcher and cuts the sets it did not
// already produce — i.e. the in-progress tail set when the recording stopped.
// No-ops on a missing or empty raw file (e.g. an empty recording that was
// removed); the watcher is still cancelled to avoid a leak.
func (s *Splitter) OnRecordingFinished(stage, rawPath string, started, ended time.Time, artworkURL string) {
	s.wmu.Lock()
	w := s.live[stage]
	delete(s.live, stage)
	s.wmu.Unlock()
	if w != nil {
		w.cancel()
		<-w.done
	}

	if info, err := os.Stat(rawPath); err != nil || info.Size() == 0 {
		return
	}

	segs := planSets(stage, started, ended, s.tt)
	if len(segs) == 0 {
		return
	}

	var cut map[time.Time]bool
	if w != nil {
		cut = w.cut
	}
	cover, err := s.fetchArtwork(artworkURL)
	if err != nil {
		s.log.Warn(fmt.Sprintf("[%s] artwork unavailable: %s (splitting without cover)", stage, err))
		cover = ""
	}

	for _, seg := range pendingSegments(segs, cut) {
		if err := s.cutSegment(rawPath, stage, cover, seg); err != nil {
			s.log.Error(fmt.Sprintf("[%s] split %s failed: %s", stage, seg.set.DJ, err))
			continue
		}
		s.log.Info(fmt.Sprintf("[%s] Split set: %s (%s)",
			stage, seg.set.DJ, util.FormatDuration(int(seg.duration.Minutes()))))
	}
}

// pendingSegments drops the segments already cut live (keyed by set start time).
func pendingSegments(segs []segment, cut map[time.Time]bool) []segment {
	if len(cut) == 0 {
		return segs
	}
	out := make([]segment, 0, len(segs))
	for _, seg := range segs {
		if !cut[seg.set.Start] {
			out = append(out, seg)
		}
	}
	return out
}

// cutSegment writes one per-set MP3 under <dir>/<stage>/, tagged with ID3
// metadata and cover art. It is shared by the live watcher and the finish
// (tail) cut so both produce identical files.
func (s *Splitter) cutSegment(rawPath, stage, cover string, seg segment) error {
	stageDir := filepath.Join(s.dir, util.Alnum(stage))
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		return err
	}
	out := filepath.Join(stageDir, setReleaseName(stage, seg.set.DJ, seg.set.Start, s.group)+".mp3")
	return s.cut(rawPath, out, seg, stage, cover)
}

func (s *Splitter) cut(rawPath, out string, seg segment, stage, cover string) error {
	args := []string{
		"-y", "-nostdin", "-loglevel", "error",
		"-ss", seconds(seg.offset),
		"-t", seconds(seg.duration),
		"-i", rawPath,
	}
	if cover != "" {
		args = append(args, "-i", cover)
	}
	args = append(args, "-map", "0:a")
	if cover != "" {
		args = append(args, "-map", "1:v", "-c:v", "copy", "-disposition:v", "attached_pic")
	}
	args = append(args, "-c:a", "copy",
		"-id3v2_version", "3",
		"-write_id3v2", "1",
		"-metadata", "title="+metadataTitle(stage, seg.set.DJ, seg.set.Start),
		"-metadata", "artist="+strings.TrimSpace(seg.set.DJ),
		"-metadata", "album_artist=DEFQON.1",
		"-metadata", "album="+s.album,
		"-metadata", "date="+fmt.Sprint(seg.set.Start.Year()),
		"-metadata", "genre="+genre.ForStage(stage, s.genre),
		"-metadata", "comment=Recorded with DEFQON.1 Stream Recorder",
		out,
	)
	cmd := exec.Command(s.ffmpeg, args...)
	return cmd.Run()
}

func seconds(d time.Duration) string { return fmt.Sprintf("%.3f", d.Seconds()) }

func metadataTitle(stage, dj string, start time.Time) string {
	dj = strings.TrimSpace(dj)
	if dj == "" {
		dj = "TBA"
	}
	return fmt.Sprintf("DEFQON.1 %d - %s - %s", start.Year(), stage, dj)
}

// setReleaseName builds a scene-style name for a per-set file, e.g.
// "DEFQON.1.2026.UV.D-Sturb.LIVE.MP3-revunix".
func setReleaseName(stage, dj string, start time.Time, group string) string {
	stg := util.Alnum(stage)
	if stg == "" {
		stg = "UNKNOWN"
	}
	artist := util.AlnumDash(dj)
	if artist == "" {
		artist = "TBA"
	}
	grp := util.Alnum(group)
	if grp == "" {
		grp = "anonymous"
	}
	return fmt.Sprintf("DEFQON.1.%d.%s.%s.LIVE.MP3-%s",
		start.Year(), stg, artist, grp)
}

// fetchArtwork downloads (and caches) the artwork for a URL in the OS temp dir.
// Returns "" with nil error when no URL is set.
func (s *Splitter) fetchArtwork(url string) (string, error) {
	if url == "" {
		return "", nil
	}
	s.mu.Lock()
	if p, ok := s.artwork[url]; ok {
		s.mu.Unlock()
		return p, nil
	}
	s.mu.Unlock()

	sum := sha1.Sum([]byte(url))
	dst := filepath.Join(os.TempDir(), "defqon-artwork-"+hex.EncodeToString(sum[:])+extFromURL(url))
	if _, err := os.Stat(dst); err == nil {
		s.cache(url, dst)
		return dst, nil
	}

	resp, err := s.http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("artwork http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // 8 MB cap
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(dst, body, 0o644); err != nil {
		return "", err
	}
	s.cache(url, dst)
	return dst, nil
}

func (s *Splitter) cache(url, path string) {
	s.mu.Lock()
	s.artwork[url] = path
	s.mu.Unlock()
}

func extFromURL(url string) string {
	if ext := filepath.Ext(url); ext != "" {
		return ext
	}
	return ".jpg"
}
