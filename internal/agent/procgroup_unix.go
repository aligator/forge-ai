//go:build unix

package agent

import (
	"os/exec"
	"syscall"
)

// setProcessGroupCancel runs cmd in its own process group and, on context
// cancellation, kills the entire group so child processes spawned by the agent
// CLI are terminated too rather than left as orphans.
func setProcessGroupCancel(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}
