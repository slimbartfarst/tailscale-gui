// internal/picker/picker.go
//
// Dialog helpers for the Taildrop send flow.
//
// Provides two pickers:
//
//  1. File picker  — wraps `zenity --file-selection`
//  2. Peer picker  — wraps `zenity --list` to let the user choose a destination
//     peer from a formatted table showing hostname, OS, and Tailscale IP.
//
// Both fall back gracefully when zenity is not installed:
//   - File picker returns ErrZenityNotFound so the caller can show a
//     notification asking the user to install zenity.
//   - Peer picker falls back to a plain `zenity --entry` prompt where the
//     user can type a hostname or IP prefix, and we fuzzy-match it against
//     the online peer list.
//
// All dialog functions are blocking — call them from a goroutine, not the
// systray main thread.
package picker

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"tailscale.com/ipn/ipnstate"
)

// ErrZenityNotFound is returned when zenity is not in PATH.
var ErrZenityNotFound = errors.New("zenity not found — install it with: sudo apt install zenity")

// ErrCancelled is returned when the user dismisses a dialog without choosing.
var ErrCancelled = errors.New("cancelled")

// ── File picker ───────────────────────────────────────────────────────────────

// PickFile opens a GTK file-chooser dialog and returns the selected path.
// Returns ErrCancelled if the user clicks Cancel.
// Returns ErrZenityNotFound if zenity is not installed.
func PickFile() (string, error) {
	out, err := runZenity(
		"--file-selection",
		"--title=Taildrop: Select file to send",
	)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(out, "\n\r"), nil
}

// PickFiles opens the file chooser in multi-select mode and returns all chosen
// paths (pipe-separated by zenity, split here).
func PickFiles() ([]string, error) {
	out, err := runZenity(
		"--file-selection",
		"--multiple",
		"--separator=|",
		"--title=Taildrop: Select files to send",
	)
	if err != nil {
		return nil, err
	}
	raw := strings.TrimRight(out, "\n\r")
	if raw == "" {
		return nil, ErrCancelled
	}
	return strings.Split(raw, "|"), nil
}

// ── Peer picker ───────────────────────────────────────────────────────────────

// PickPeer shows a list-dialog of online Taildrop-capable peers and returns
// the chosen one. Falls back to a text-entry dialog if --list is unavailable.
//
// peers must already be filtered to the ones you want to present (e.g. only
// online peers, or only peers that support file receiving).
func PickPeer(peers []*ipnstate.PeerStatus) (*ipnstate.PeerStatus, error) {
	if len(peers) == 0 {
		return nil, errors.New("no peers available to send to")
	}
	if len(peers) == 1 {
		// Only one choice — skip the dialog and confirm via a yes/no.
		p := peers[0]
		ip := firstIP(p)
		ok, err := confirm(fmt.Sprintf("Send to %s (%s)?", p.HostName, ip))
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrCancelled
		}
		return p, nil
	}

	// Try the rich list dialog first.
	chosen, err := pickPeerWithList(peers)
	if err == nil {
		return chosen, nil
	}

	// zenity --list not supported (older zenity, some Wayland compositors) —
	// fall back to free-text entry with fuzzy matching.
	if errors.Is(err, errListUnsupported) {
		return pickPeerWithEntry(peers)
	}
	return nil, err
}

// ── zenity --list implementation ──────────────────────────────────────────────

var errListUnsupported = errors.New("zenity --list unsupported")

// pickPeerWithList builds a zenity --list table with three columns:
// hostname | OS | Tailscale IP
//
// zenity --list returns the value from the first column of the chosen row.
func pickPeerWithList(peers []*ipnstate.PeerStatus) (*ipnstate.PeerStatus, error) {
	// Build the argument list.
	// zenity --list --column= ... <row0col0> <row0col1> <row0col2> ...
	args := []string{
		"--list",
		"--title=Taildrop: Choose destination",
		"--text=Select the device to send the file to:",
		"--column=Device",
		"--column=OS",
		"--column=Tailscale IP",
		"--width=540",
		"--height=340",
		"--print-column=1", // return the hostname (col 1)
	}

	// Index peer by hostname so we can look up the chosen one.
	byHostname := make(map[string]*ipnstate.PeerStatus, len(peers))
	for _, p := range peers {
		args = append(args,
			p.HostName,
			osLabel(p.OS),
			firstIP(p),
		)
		byHostname[p.HostName] = p
	}

	out, err := runZenity(args...)
	if err != nil {
		// zenity exits 1 on Cancel — already handled by runZenity.
		// If it fails with a different error it may be because --list is
		// not supported in this build/env.
		return nil, errListUnsupported
	}

	chosen := strings.TrimRight(out, "\n\r")
	p, ok := byHostname[chosen]
	if !ok {
		// Shouldn't happen unless the user somehow got an unknown hostname.
		return nil, fmt.Errorf("unknown peer %q selected", chosen)
	}
	return p, nil
}

// ── zenity --entry fallback ───────────────────────────────────────────────────

// pickPeerWithEntry shows a text-entry dialog pre-populated with a newline-
// separated list of available peers, and fuzzy-matches the user's input.
func pickPeerWithEntry(peers []*ipnstate.PeerStatus) (*ipnstate.PeerStatus, error) {
	// Build a human-readable peer list for the dialog text.
	var lines []string
	for i, p := range peers {
		lines = append(lines, fmt.Sprintf("%d. %s  %s  (%s)",
			i+1, p.HostName, firstIP(p), osLabel(p.OS)))
	}
	listText := strings.Join(lines, "\n")

	out, err := runZenity(
		"--entry",
		"--title=Taildrop: Choose destination",
		fmt.Sprintf("--text=Available devices:\n\n%s\n\nType a hostname, IP, or number:", listText),
		"--entry-text=",
		"--width=460",
	)
	if err != nil {
		return nil, err
	}

	input := strings.TrimSpace(strings.TrimRight(out, "\n\r"))
	if input == "" {
		return nil, ErrCancelled
	}

	return matchPeer(peers, input)
}

// matchPeer finds the best match for input among peers.
// It accepts:
//   - An exact hostname
//   - A hostname prefix (case-insensitive)
//   - A Tailscale IP or IP prefix
//   - A 1-based index number ("1", "2", …)
func matchPeer(peers []*ipnstate.PeerStatus, input string) (*ipnstate.PeerStatus, error) {
	lower := strings.ToLower(input)

	// Number?
	var idx int
	if n, _ := fmt.Sscanf(input, "%d", &idx); n == 1 && idx >= 1 && idx <= len(peers) {
		return peers[idx-1], nil
	}

	// Exact hostname
	for _, p := range peers {
		if strings.EqualFold(p.HostName, input) {
			return p, nil
		}
	}

	// Exact IP
	for _, p := range peers {
		if firstIP(p) == input {
			return p, nil
		}
	}

	// Hostname prefix
	var prefixMatches []*ipnstate.PeerStatus
	for _, p := range peers {
		if strings.HasPrefix(strings.ToLower(p.HostName), lower) {
			prefixMatches = append(prefixMatches, p)
		}
	}
	if len(prefixMatches) == 1 {
		return prefixMatches[0], nil
	}
	if len(prefixMatches) > 1 {
		names := make([]string, len(prefixMatches))
		for i, p := range prefixMatches {
			names[i] = p.HostName
		}
		return nil, fmt.Errorf("ambiguous: %s all match %q — be more specific",
			strings.Join(names, ", "), input)
	}

	// IP prefix
	for _, p := range peers {
		if strings.HasPrefix(firstIP(p), input) {
			return p, nil
		}
	}

	return nil, fmt.Errorf("no peer matches %q", input)
}

// ── Yes/No confirm ────────────────────────────────────────────────────────────

// confirm shows a yes/no question dialog. Returns true if the user clicks Yes.
func confirm(question string) (bool, error) {
	_, err := runZenity(
		"--question",
		"--title=Taildrop",
		"--text="+question,
		"--ok-label=Send",
		"--cancel-label=Cancel",
		"--width=340",
	)
	if errors.Is(err, ErrCancelled) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// ── Progress dialog ───────────────────────────────────────────────────────────

// ProgressDialog wraps a zenity --progress window.
// Call Update() as bytes arrive, Close() when done or on error.
type ProgressDialog struct {
	cmd   *exec.Cmd
	stdin *strings.Reader // not used; we write via cmd.Stdin pipe
	pipe  interface{ Write([]byte) (int, error) }
	total int64
}

// NewProgressDialog opens a zenity progress bar for a file transfer.
// Returns nil (not an error) if zenity is unavailable — the transfer will
// still proceed, just without a visible progress bar.
func NewProgressDialog(filename string, totalBytes int64) *ProgressDialog {
	cmd := exec.Command("zenity",
		"--progress",
		"--title=Taildrop",
		fmt.Sprintf("--text=Sending %s…", filename),
		"--percentage=0",
		"--auto-close",
		"--no-cancel",
		"--width=380",
	)
	pipe, err := cmd.StdinPipe()
	if err != nil {
		return nil
	}
	if err := cmd.Start(); err != nil {
		return nil // zenity not available
	}
	return &ProgressDialog{cmd: cmd, pipe: pipe, total: totalBytes}
}

// Update sets the progress bar to reflect bytesSent out of total.
func (d *ProgressDialog) Update(bytesSent int64) {
	if d == nil || d.pipe == nil || d.total == 0 {
		return
	}
	pct := int(float64(bytesSent) / float64(d.total) * 100)
	if pct > 100 {
		pct = 100
	}
	fmt.Fprintf(d.pipe, "%d\n", pct)
}

// Close finalises the progress dialog (sets to 100% and waits for zenity).
func (d *ProgressDialog) Close() {
	if d == nil || d.pipe == nil {
		return
	}
	fmt.Fprintf(d.pipe, "100\n")
	// Give zenity a moment to show 100% before it auto-closes.
	_ = d.cmd.Wait()
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// RunZenity runs zenity with the given arguments.
// Returns ErrZenityNotFound if not installed, ErrCancelled if the user
// pressed Cancel (exit code 1), or the raw stdout on success.
// This is exported so other packages (systray) can reuse the same
// error-normalisation logic.
func RunZenity(args ...string) (string, error) {
	return runZenity(args...)
}

// runZenity runs zenity with the given arguments.
// Returns ErrZenityNotFound if not installed, ErrCancelled if exit code 1
// (user pressed Cancel), or the raw stdout on success.
func runZenity(args ...string) (string, error) {
	cmd := exec.Command("zenity", args...)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if exitErr.ExitCode() == 1 {
				return "", ErrCancelled
			}
		}
		if errors.Is(err, exec.ErrNotFound) {
			return "", ErrZenityNotFound
		}
		// Some environments return exit code 5 for "zenity not found in this
		// Flatpak sandbox" — treat all unknown exits as not-found.
		return "", ErrZenityNotFound
	}
	return string(out), nil
}

// firstIP returns the first Tailscale IP for a peer as a string, or "—".
func firstIP(p *ipnstate.PeerStatus) string {
	if len(p.TailscaleIPs) > 0 {
		return p.TailscaleIPs[0].String()
	}
	return "—"
}

// osLabel normalises the OS string for display.
func osLabel(os string) string {
	switch strings.ToLower(os) {
	case "linux":
		return "Linux"
	case "windows":
		return "Windows"
	case "darwin":
		return "macOS"
	case "android":
		return "Android"
	case "ios":
		return "iOS"
	case "freebsd":
		return "FreeBSD"
	default:
		if os == "" {
			return "—"
		}
		return os
	}
}
