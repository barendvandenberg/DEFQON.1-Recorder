//go:build !windows

package youtube

import (
	"os"
	"os/exec"
)

func prepareCommand(_ *exec.Cmd) {}

func interruptCommand(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Signal(os.Interrupt)
}
