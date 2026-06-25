// Package prefs persists user preferences that should survive a process
// restart, such as which channels are allowed to be recorded and which
// YouTube recording mode each feed should use.
//
// The file is a tiny INI subset with [recording] and [youtube] sections. Only
// the standard library is used on purpose to avoid pulling in an INI dependency
// for something this small.
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

const (
	recordingSection = "[recording]"
	youtubeSection   = "[youtube]"
)

// Preferences is the complete persisted settings file.
type Preferences struct {
	Recording map[string]bool
	YouTube   map[string]string
}

// Store reads and writes the preferences file. It keeps an in-memory copy so
// single-entry updates can be written without re-reading the file each time.
type Store struct {
	path string

	mu   sync.Mutex
	data Preferences
}

func New(path string) *Store {
	return &Store{
		path: path,
		data: emptyPreferences(),
	}
}

// Load reads only the recording preferences for older callers.
func (s *Store) Load() (map[string]bool, error) {
	p, err := s.LoadAll()
	if err != nil {
		return nil, err
	}
	return p.Recording, nil
}

// LoadAll reads the preferences file into the store and returns a copy. A missing
// file is not an error: an empty map is returned so callers can seed defaults.
func (s *Store) LoadAll() (Preferences, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			s.mu.Lock()
			s.data = emptyPreferences()
			s.mu.Unlock()
			return emptyPreferences(), nil
		}
		return Preferences{}, fmt.Errorf("read %s: %w", s.path, err)
	}

	data, err := parse(raw)
	if err != nil {
		return Preferences{}, fmt.Errorf("parse %s: %w", s.path, err)
	}

	s.mu.Lock()
	s.data = data
	out := copyPreferences(data)
	s.mu.Unlock()
	return out, nil
}

// Save updates a single channel and rewrites the file atomically.
func (s *Store) Save(channel string, enabled bool) error {
	s.mu.Lock()
	ensurePreferences(&s.data)
	s.data.Recording[channel] = enabled
	snapshot := copyPreferences(s.data)
	s.mu.Unlock()
	return write(s.path, snapshot)
}

// SaveYouTube updates a single YouTube feed mode and rewrites the file
// atomically.
func (s *Store) SaveYouTube(feed, mode string) error {
	s.mu.Lock()
	ensurePreferences(&s.data)
	s.data.YouTube[feed] = mode
	snapshot := copyPreferences(s.data)
	s.mu.Unlock()
	return write(s.path, snapshot)
}

// SaveAll replaces recording preferences and rewrites the file atomically.
// YouTube preferences already loaded in this Store are preserved.
func (s *Store) SaveAll(data map[string]bool) error {
	s.mu.Lock()
	ensurePreferences(&s.data)
	s.data.Recording = copyBoolMap(data)
	snapshot := copyPreferences(s.data)
	s.mu.Unlock()
	return write(s.path, snapshot)
}

// SavePreferences replaces the whole preferences file and rewrites it atomically.
func (s *Store) SavePreferences(data Preferences) error {
	s.mu.Lock()
	s.data = copyPreferences(data)
	ensurePreferences(&s.data)
	snapshot := copyPreferences(s.data)
	s.mu.Unlock()
	return write(s.path, snapshot)
}

func emptyPreferences() Preferences {
	return Preferences{
		Recording: map[string]bool{},
		YouTube:   map[string]string{},
	}
}

func ensurePreferences(p *Preferences) {
	if p.Recording == nil {
		p.Recording = map[string]bool{}
	}
	if p.YouTube == nil {
		p.YouTube = map[string]string{}
	}
}

func copyPreferences(in Preferences) Preferences {
	return Preferences{
		Recording: copyBoolMap(in.Recording),
		YouTube:   copyStringMap(in.YouTube),
	}
}

func copyBoolMap(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func parse(raw []byte) (Preferences, error) {
	out := emptyPreferences()
	section := ""
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			switch {
			case strings.EqualFold(line, recordingSection):
				section = recordingSection
			case strings.EqualFold(line, youtubeSection):
				section = youtubeSection
			default:
				section = ""
			}
			continue
		}
		if section == "" {
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
		val = strings.TrimSpace(val)
		switch section {
		case recordingSection:
			out.Recording[key] = parseBool(val)
		case youtubeSection:
			if val != "" {
				out.YouTube[key] = val
			}
		}
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
func write(path string, data Preferences) error {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create prefs dir: %w", err)
		}
	}

	var buf bytes.Buffer
	buf.WriteString("# DEFQON.1 Recorder preferences\n")
	buf.WriteString("# Recording toggle per channel: on = record, off = skip.\n")
	buf.WriteString("# YouTube modes: video_audio = video + MP3, audio = MP3 only, none = skip.\n")
	buf.WriteString("# This file is rewritten whenever a recording mode is toggled in the TUI (d key).\n")
	buf.WriteString(recordingSection + "\n")

	ensurePreferences(&data)

	keys := make([]string, 0, len(data.Recording))
	for k := range data.Recording {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&buf, "%s=%s\n", k, formatBool(data.Recording[k]))
	}

	buf.WriteString("\n" + youtubeSection + "\n")
	keys = make([]string, 0, len(data.YouTube))
	for k := range data.YouTube {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&buf, "%s=%s\n", k, data.YouTube[k])
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
