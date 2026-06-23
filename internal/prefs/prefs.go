// Package prefs persists user preferences that should survive a process
// restart, such as which channels are allowed to be recorded.
//
// The file is a tiny INI subset: a single [recording] section with
// "<channel>=<on|off>" lines. Only the standard library is used on purpose to
// avoid pulling in an INI dependency for something this small.
package prefs

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const recordingSection = "[recording]"

// Store reads and writes the preferences file. It keeps an in-memory copy so
// single-channel updates can be written without re-reading the file each time.
type Store struct {
	path string

	mu   sync.Mutex
	data map[string]bool
}

func New(path string) *Store {
	return &Store{
		path: path,
		data: map[string]bool{},
	}
}

// Load reads the preferences file into the store and returns a copy. A missing
// file is not an error: an empty map is returned so callers can seed defaults.
func (s *Store) Load() (map[string]bool, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			s.mu.Lock()
			s.data = map[string]bool{}
			s.mu.Unlock()
			return map[string]bool{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", s.path, err)
	}

	data, err := parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.path, err)
	}

	s.mu.Lock()
	s.data = data
	out := copyMap(data)
	s.mu.Unlock()
	return out, nil
}

// Save updates a single channel and rewrites the file atomically.
func (s *Store) Save(channel string, enabled bool) error {
	s.mu.Lock()
	s.data[channel] = enabled
	snapshot := copyMap(s.data)
	s.mu.Unlock()
	return write(s.path, snapshot)
}

// SaveAll replaces every stored preference and rewrites the file atomically.
func (s *Store) SaveAll(data map[string]bool) error {
	s.mu.Lock()
	s.data = copyMap(data)
	snapshot := copyMap(s.data)
	s.mu.Unlock()
	return write(s.path, snapshot)
}

func copyMap(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func parse(raw []byte) (map[string]bool, error) {
	out := map[string]bool{}
	inSection := false
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			inSection = strings.EqualFold(line, recordingSection)
			continue
		}
		if !inSection {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = parseBool(strings.TrimSpace(val))
	}
	return out, scanner.Err()
}

func parseBool(s string) bool {
	switch strings.ToLower(s) {
	case "1", "true", "yes", "on", "enabled":
		return true
	default:
		return false
	}
}

func formatBool(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// write renders the preferences and replaces the file atomically via a temp
// file in the same directory, so a crash mid-write cannot corrupt it.
func write(path string, data map[string]bool) error {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create prefs dir: %w", err)
		}
	}

	var buf bytes.Buffer
	buf.WriteString("# DEFQON.1 Recorder preferences\n")
	buf.WriteString("# Recording toggle per channel: on = record, off = skip.\n")
	buf.WriteString("# This file is rewritten whenever a channel is toggled in the TUI (d key).\n")
	buf.WriteString(recordingSection + "\n")

	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&buf, "%s=%s\n", k, formatBool(data[k]))
	}

	tmp, err := os.CreateTemp(dir, ".recorder-prefs-*")
	if err != nil {
		return fmt.Errorf("create temp prefs: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := buf.WriteTo(tmp); err != nil {
		tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("write temp prefs: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close temp prefs: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("replace prefs: %w", err)
	}
	return nil
}
