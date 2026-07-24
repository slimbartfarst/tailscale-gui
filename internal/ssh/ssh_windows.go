// internal/ssh/ssh_windows.go
package ssh

import "os/exec"

func setSysProcAttr(cmd *exec.Cmd) {
	// Windows doesn't have Setsid; the terminal process is already detached
	// by virtue of being a GUI application.
}
