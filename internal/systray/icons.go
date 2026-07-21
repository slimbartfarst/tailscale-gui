// internal/systray/icons.go
//
// Embedded PNG icons and OS desktop helpers (browser, file manager,
// clipboard, file picker).
package systray

import (
	_ "embed"
	"log"
	"os/exec"
	"runtime"
	"strings"
)

// Icons are embedded at build time from assets/icons/.
// Replace the PNGs with real 32×32 artwork before shipping.

//go:embed ../../assets/icons/connected.png
var iconConnected []byte

//go:embed ../../assets/icons/disconnected.png
var iconDisconnected []byte

//go:embed ../../assets/icons/connecting.png
var iconConnecting []byte

//go:embed ../../assets/icons/warning.png
var iconWarning []byte

// ── OS helpers ────────────────────────────────────────────────────────────────

// openBrowser opens a URL in the default browser.
func openBrowser(url string) {
	cmd, args := browserCmd(url)
	if err := exec.Command(cmd, args...).Start(); err != nil {
		log.Printf("openBrowser: %v", err)
	}
}

func browserCmd(url string) (string, []string) {
	switch runtime.GOOS {
	case "linux":
		return "xdg-open", []string{url}
	case "darwin":
		return "open", []string{url}
	default:
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}
	}
}

// openFileManager opens a directory in the desktop file manager.
func openFileManager(dir string) {
	cmd, args := fileManagerCmd(dir)
	if err := exec.Command(cmd, args...).Start(); err != nil {
		log.Printf("openFileManager: %v", err)
	}
}

func fileManagerCmd(dir string) (string, []string) {
	switch runtime.GOOS {
	case "linux":
		// xdg-open works for directories too on most desktops.
		return "xdg-open", []string{dir}
	case "darwin":
		return "open", []string{dir}
	default:
		return "explorer", []string{dir}
	}
}

// copyToClipboard copies text to the system clipboard.
// Uses xclip if available, then xsel, then wl-copy (Wayland).
func copyToClipboard(text string) error {
	tools := []struct {
		cmd  string
		args []string
	}{
		{"xclip", []string{"-selection", "clipboard"}},
		{"xsel", []string{"--clipboard", "--input"}},
		{"wl-copy", nil},
	}
	for _, t := range tools {
		cmd := exec.Command(t.cmd, t.args...)
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err == nil {
			return nil
		}
	}
	return exec.ErrNotFound
}

// pickFileWithZenity opens a GTK file-chooser dialog and returns the chosen path.
// Returns ("", nil) if the user cancels, or an error if zenity is unavailable.
func pickFileWithZenity() (string, error) {
	out, err := exec.Command(
		"zenity",
		"--file-selection",
		"--title=Select file to send via Taildrop",
	).Output()
	if err != nil {
		// Exit code 1 means user pressed Cancel — not a real error.
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return "", nil
		}
		return "", err
	}
	path := strings.TrimRight(string(out), "\n\r")
	return path, nil
}
