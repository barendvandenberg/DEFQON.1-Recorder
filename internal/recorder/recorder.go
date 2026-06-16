package recorder

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/revunix/defqon1-recorder/internal/logging"
)

type Snapshot struct {
	Stage     string
	Path      string
	Listeners int
}

type recording struct {
	stage     string
	cmd       *exec.Cmd
	path      string
	fileName  string
	listeners int
	lastSize  int64
	lastCheck time.Time
	mu        sync.Mutex
}

type Manager struct {
	dir          string
	stalledAfter time.Duration
	log          logging.Logger

	mu     sync.RWMutex
	active map[string]*recording
	wg     sync.WaitGroup

	stopped int32
}

func New(dir string, stalledAfter time.Duration, log logging.Logger) *Manager {
	if log == nil {
		log = logging.Noop{}
	}
	return &Manager{
		dir:          dir,
		stalledAfter: stalledAfter,
		log:          log,
		active:       make(map[string]*recording),
	}
}

func (m *Manager) IsRecording(stage string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.active[stage]
	return ok
}

func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.active)
}

func (m *Manager) Start(stage, streamURL string, listeners int) {
	m.mu.Lock()
	if rec, ok := m.active[stage]; ok {
		rec.listeners = listeners
		m.mu.Unlock()
		return
	}

	fileName := fmt.Sprintf("%s_%s.mp3", stage, time.Now().UTC().Format("2006-01-02T15-04-05.000Z"))
	outputPath := filepath.Join(m.dir, fileName)
	cmd := exec.Command("yt-dlp",
		"--no-part", "-f", "bestaudio", "--extract-audio",
		"--audio-format", "mp3", "--live-from-start",
		"-o", outputPath, streamURL,
	)
	rec := &recording{
		stage:     stage,
		cmd:       cmd,
		path:      outputPath,
		fileName:  fileName,
		listeners: listeners,
		lastCheck: time.Now(),
	}
	m.active[stage] = rec
	m.mu.Unlock()

	m.log.Info(fmt.Sprintf("[%s] Starting recording...", stage))

	stderr, err := cmd.StderrPipe()
	if err != nil {
		m.log.Error(fmt.Sprintf("[%s] Pipe error: %s", stage, err))
		m.remove(stage)
		return
	}
	if err := cmd.Start(); err != nil {
		m.log.Error(fmt.Sprintf("[%s] Failed to start: %s", stage, err))
		m.remove(stage)
		return
	}

	m.wg.Add(1)
	go m.watchStderr(stage, stderr)
	go m.wait(stage, cmd)
}

func (m *Manager) wait(stage string, cmd *exec.Cmd) {
	defer m.wg.Done()
	err := cmd.Wait()

	if atomic.LoadInt32(&m.stopped) == 0 {
		code := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else {
				m.log.Error(fmt.Sprintf("[%s] Wait error: %s", stage, err))
			}
		}
		m.log.Info(fmt.Sprintf("[%s] Recording finished (code %d).", stage, code))
	}
	m.remove(stage)
}

func (m *Manager) watchStderr(stage string, r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.Contains(strings.ToLower(line), "error") {
			m.log.Error(fmt.Sprintf("[%s] yt-dlp: %s", stage, line))
		}
	}
}

func (m *Manager) Stop(stage string) {
	m.mu.Lock()
	rec, ok := m.active[stage]
	delete(m.active, stage)
	m.mu.Unlock()
	if ok && rec.cmd.Process != nil {
		_ = rec.cmd.Process.Signal(os.Interrupt)
	}
}

func (m *Manager) forceKill(stage string) {
	m.mu.Lock()
	rec, ok := m.active[stage]
	delete(m.active, stage)
	m.mu.Unlock()
	if ok && rec.cmd.Process != nil {
		_ = rec.cmd.Process.Kill()
	}
}

func (m *Manager) UpdateListeners(stage string, listeners int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rec, ok := m.active[stage]; ok {
		rec.listeners = listeners
	}
}

func (m *Manager) MonitorStalled() {
	m.mu.RLock()
	stages := make([]string, 0, len(m.active))
	for stage := range m.active {
		stages = append(stages, stage)
	}
	m.mu.RUnlock()

	for _, stage := range stages {
		m.mu.RLock()
		rec := m.active[stage]
		m.mu.RUnlock()
		if rec == nil {
			continue
		}

		info, err := os.Stat(rec.path)
		if err != nil {
			if !os.IsNotExist(err) {
				m.log.Error(fmt.Sprintf("[%s] Stat error: %s", stage, err))
			}
			continue
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
			m.log.Warn(fmt.Sprintf("[%s] Stalled. Restarting...", stage))
			m.forceKill(stage)
		}
	}
}

func (m *Manager) Active() []Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Snapshot, 0, len(m.active))
	for _, rec := range m.active {
		rec.mu.Lock()
		out = append(out, Snapshot{
			Stage:     rec.stage,
			Path:      rec.path,
			Listeners: rec.listeners,
		})
		rec.mu.Unlock()
	}
	return out
}

func (m *Manager) TotalListeners() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	total := 0
	for _, rec := range m.active {
		total += rec.listeners
	}
	return total
}

func (m *Manager) StopAll(timeout time.Duration) {
	atomic.StoreInt32(&m.stopped, 1)

	m.mu.Lock()
	for _, rec := range m.active {
		if rec.cmd.Process != nil {
			_ = rec.cmd.Process.Signal(os.Interrupt)
		}
	}
	m.mu.Unlock()

	m.log.Info("--- Gracefully shutting down ---")

	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(timeout):
		m.mu.Lock()
		for _, rec := range m.active {
			if rec.cmd.Process != nil {
				_ = rec.cmd.Process.Kill()
			}
		}
		m.mu.Unlock()
		<-done
	}
}

func (m *Manager) remove(stage string) {
	m.mu.Lock()
	delete(m.active, stage)
	m.mu.Unlock()
}
