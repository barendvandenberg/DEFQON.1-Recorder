package listener

import (
	"fmt"
	"net/url"
)

type State string

const (
	StateStopped  State = "Stopped"
	StateStarting State = "Starting"
	StatePlaying  State = "Playing"
)

type Snapshot struct {
	State State
	Stage string
}

func validateURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("unsupported URL scheme %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return fmt.Errorf("stream URL is missing a host")
	}
	return nil
}
