package logging

type Level int

const (
	LevelInfo Level = iota
	LevelWarn
	LevelError
)

type Logger interface {
	Info(msg string)
	Warn(msg string)
	Error(msg string)
}

type Noop struct{}

func (Noop) Info(string)  {}
func (Noop) Warn(string)  {}
func (Noop) Error(string) {}
