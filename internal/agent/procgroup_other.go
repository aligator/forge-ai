//go:build !unix

package agent

import "os/exec"

// setProcessGroupCancel is a no-op on platforms without process groups; the
// default exec.Cmd cancellation (killing the direct child) is used instead.
func setProcessGroupCancel(cmd *exec.Cmd) {}
