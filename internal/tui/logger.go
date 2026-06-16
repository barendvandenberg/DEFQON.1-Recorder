package tui

import "github.com/revunix/defqon1-recorder/internal/logging"

type LogMessage struct {
	Level logging.Level
	Text  string
}

type ChannelLogger struct {
	ch chan<- LogMessage
}

func NewLogger(ch chan<- LogMessage) *ChannelLogger {
	return &ChannelLogger{ch: ch}
}

func (l *ChannelLogger) Info(msg string)  { l.send(logging.LevelInfo, msg) }
func (l *ChannelLogger) Warn(msg string)  { l.send(logging.LevelWarn, msg) }
func (l *ChannelLogger) Error(msg string) { l.send(logging.LevelError, msg) }

// send is non-blocking: if the buffer is saturated the message is dropped to
// avoid stalling background workers.
func (l *ChannelLogger) send(level logging.Level, msg string) {
	select {
	case l.ch <- LogMessage{Level: level, Text: msg}:
	default:
	}
}
