// internal/ssh/ssh.go
//
// SSH peer launch — opens a terminal emulator running `ssh <target>`.
//
// Target resolution order:
//  1. MagicDNS hostname (e.g. alice-laptop.tailnet.example.ts.net) — if MagicDNS
//     is enabled this just works without needing an IP.
//  2. Short hostname (e.g. alice-laptop) — works when Tailscale DNS is active.
//  3. First Tailscale IPv4 address — always works regardless of DNS.
//
// Terminal detection order (respects $TERMINAL env var first):
//  1. $TERMINAL environment variable
//  2. Detected running desktop environment's preferred terminal
//  3. Ordered fallback list: xterm, gnome-terminal, konsole, xfce4-terminal,
//     mate-terminal, lxterminal, alacritty, kitty, wezterm, foot
//
// SSH user:
//  - If cfg.SSHUser is set, uses that.
//  - Otherwise omits -l flag and lets ssh use its own default resolution
//    (~/.ssh/config, then $USER).
//
// Tailscale SSH detection:
//  PeerStatus.TailscaleSSHEnabled reports whether the peer is running the
//  Tailscale SSH server. We show the SSH button only for those peers, plus
//  any peer the user has explicitly marked as SSH-able via config.
package ssh

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"tailscale.com/ipn/ipnstate"
)

// Config holds SSH-related user preferences.
type Config struct {
	// TerminalCmd overrides automatic terminal detection.
	// May include arguments, e.g. "alacritty -e" or "xterm -e".
	// If empty, DetectTerminal() is used.
	TerminalCmd string

	// SSHUser is the remote username. Empty = let ssh decide.
	SSHUser string

	// ExtraSSHArgs are appended to the ssh command before the host,
	// e.g. []string{"-p", "2222"} or []string{"-A"} for agent forwarding.
	ExtraSSHArgs []string
}

// ── Capability check ──────────────────────────────────────────────────────────

// PeerSupportsSSH reports whether a peer is a valid SSH target.
// It returns true if:
//   - TailscaleSSHEnabled is true (the peer runs Tailscale SSH), OR
//   - the peer is online and has at least one Tailscale IP (user may have
//     their own sshd running; we show the button and let ssh handle refusal).
//
// The second condition can be tightened by callers if desired.
func PeerSupportsSSH(p *ipnstate.PeerStatus) bool {
	if !p.Online {
		return false
	}
	if p.TailscaleSSHEnabled {
		return true
	}
	// Show SSH option for any online Linux/macOS/FreeBSD peer —
	// they're likely running sshd even if not Tailscale SSH.
	switch strings.ToLower(p.OS) {
	case "linux", "darwin", "freebsd", "openbsd", "netbsd":
		return len(p.TailscaleIPs) > 0
	}
	return false
}

// ── Target resolution ─────────────────────────────────────────────────────────

// Target returns the best SSH hostname/IP for a peer.
// Prefers the short DNS name (first label of DNSName), then HostName, then IP.
func Target(p *ipnstate.PeerStatus) string {
	if p.DNSName != "" {
		// DNSName is fully-qualified with a trailing dot: "alice.tailnet.ts.net."
		// Strip the trailing dot; keep the full name so ssh can use it.
		fqdn := strings.TrimSuffix(p.DNSName, ".")
		return fqdn
	}
	if p.HostName != "" {
		return p.HostName
	}
	if len(p.TailscaleIPs) > 0 {
		return p.TailscaleIPs[0].String()
	}
	return ""
}

// ── SSH command builder ───────────────────────────────────────────────────────

// Command builds the full exec.Cmd to SSH into a peer.
// It returns an error if no terminal emulator can be found.
func Command(p *ipnstate.PeerStatus, cfg Config) (*exec.Cmd, error) {
	target := Target(p)
	if target == "" {
		return nil, fmt.Errorf("peer %q has no usable address", p.HostName)
	}

	// Build the ssh argument list.
	sshArgs := []string{"ssh"}
	if cfg.SSHUser != "" {
		sshArgs = append(sshArgs, "-l", cfg.SSHUser)
	}
	sshArgs = append(sshArgs, cfg.ExtraSSHArgs...)
	sshArgs = append(sshArgs, target)

	// Wrap in a terminal emulator.
	termBin, termArgs, err := resolveTerminal(cfg.TerminalCmd)
	if err != nil {
		return nil, err
	}

	// Combine: terminal [terminal-args] ssh [ssh-args]
	allArgs := append(termArgs, sshArgs...)
	cmd := exec.Command(termBin, allArgs...)

	// Detach from our process group so the terminal outlives us.
	// (SysProcAttr is set in the platform file.)
	setSysProcAttr(cmd)

	return cmd, nil
}

// Launch builds and starts the SSH command. Non-blocking — the terminal runs
// independently. Returns an error only if the terminal can't be found or the
// process fails to start.
func Launch(p *ipnstate.PeerStatus, cfg Config) error {
	cmd, err := Command(p, cfg)
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launch terminal: %w", err)
	}
	// Reap the child in the background so it doesn't become a zombie.
	go func() { _ = cmd.Wait() }()
	return nil
}

// ── Terminal detection ────────────────────────────────────────────────────────

// resolveTerminal returns (binary, extraArgs, error) for a terminal emulator.
// If override is non-empty it is parsed and used directly.
func resolveTerminal(override string) (string, []string, error) {
	if override != "" {
		parts := strings.Fields(override)
		bin := parts[0]
		if _, err := exec.LookPath(bin); err != nil {
			return "", nil, fmt.Errorf("configured terminal %q not found: %w", bin, err)
		}
		return bin, parts[1:], nil
	}

	// $TERMINAL takes priority.
	if t := os.Getenv("TERMINAL"); t != "" {
		parts := strings.Fields(t)
		if path, err := exec.LookPath(parts[0]); err == nil {
			return path, append(parts[1:], "-e"), nil
		}
	}

	// Try desktop-environment-specific preference first.
	if t, args := desktopPreferredTerminal(); t != "" {
		return t, args, nil
	}

	// General fallback list — order reflects prevalence on modern Linux.
	candidates := []termCandidate{
		{"xterm", []string{"-e"}},
		{"gnome-terminal", []string{"--"}},
		{"konsole", []string{"-e"}},
		{"xfce4-terminal", []string{"-e"}},
		{"mate-terminal", []string{"-e"}},
		{"lxterminal", []string{"-e"}},
		{"alacritty", []string{"-e"}},
		{"kitty", []string{}},        // kitty takes command directly
		{"wezterm", []string{"start", "--"}},
		{"foot", []string{}},         // foot takes command directly
		{"tilix", []string{"-e"}},
		{"terminator", []string{"-e"}},
	}

	for _, c := range candidates {
		if path, err := exec.LookPath(c.bin); err == nil {
			return path, c.args, nil
		}
	}

	return "", nil, fmt.Errorf(
		"no terminal emulator found — install xterm or set $TERMINAL\n" +
			"  sudo apt install xterm",
	)
}

type termCandidate struct {
	bin  string
	args []string
}

// desktopPreferredTerminal returns the preferred terminal for the running
// desktop environment by checking $XDG_CURRENT_DESKTOP and $DESKTOP_SESSION.
func desktopPreferredTerminal() (string, []string) {
	desktop := strings.ToLower(os.Getenv("XDG_CURRENT_DESKTOP") + " " + os.Getenv("DESKTOP_SESSION"))

	type entry struct {
		keyword string
		bin     string
		args    []string
	}

	entries := []entry{
		{"gnome", "gnome-terminal", []string{"--"}},
		{"kde", "konsole", []string{"-e"}},
		{"xfce", "xfce4-terminal", []string{"-e"}},
		{"mate", "mate-terminal", []string{"-e"}},
		{"lxde", "lxterminal", []string{"-e"}},
		{"lxqt", "qterminal", []string{"-e"}},
		{"cosmic", "cosmic-term", []string{}},
		{"sway", "foot", []string{}},
		{"hyprland", "foot", []string{}},
	}

	for _, e := range entries {
		if strings.Contains(desktop, e.keyword) {
			if path, err := exec.LookPath(e.bin); err == nil {
				return path, e.args
			}
		}
	}
	return "", nil
}

// DetectTerminal returns the binary path and extra args that would be used
// for a terminal launch, without actually launching anything.
// Useful for displaying the detected terminal in a settings UI.
func DetectTerminal(override string) (bin string, args []string, err error) {
	return resolveTerminal(override)
}

// SSHCommandString returns the ssh command as a human-readable string,
// useful for tooltips and log messages.
func SSHCommandString(p *ipnstate.PeerStatus, cfg Config) string {
	target := Target(p)
	if cfg.SSHUser != "" {
		return fmt.Sprintf("ssh -l %s %s", cfg.SSHUser, target)
	}
	return "ssh " + target
}
