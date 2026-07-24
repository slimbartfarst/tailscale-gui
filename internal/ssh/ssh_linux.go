// internal/ssh/ssh_linux.go
//
// Linux-specific process attribute: start the terminal in a new session
// so it doesn't receive SIGHUP when tailscale-gui exits.
package ssh

import (
	"os/exec"
	"syscall"
)

func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // new session → detached from our controlling terminal
	}
}
