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

	"github.com/slimbartfarst/tailscale-gui/internal/picker"
)

// Icons are embedded at build time from assets/icons/.
// Replace the PNGs with real 32×32 artwork before shipping.

//go:embed icons/connected.png
var iconConnected []byte

//go:embed icons/disconnected.png
var iconDisconnected []byte

//go:embed icons/connecting.png
var iconConnecting []byte

//go:embed icons/warning.png
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

// openPath opens a file with the desktop's default application.
func openPath(path string) {
	cmd, args := fileManagerCmd(path) // xdg-open / open works for files too
	if err := exec.Command(cmd, args...).Start(); err != nil {
		log.Printf("openPath: %v", err)
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

// runZenity is a package-level shim that delegates to picker.RunZenity so
// all zenity calls in the systray package share the same error-normalisation
// logic (ErrCancelled, ErrZenityNotFound).
func runZenity(args ...string) (string, error) {
	return picker.RunZenity(args...)
}
