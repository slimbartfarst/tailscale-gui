// internal/ssh/ssh_test.go
package ssh

import (
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"testing"

	"tailscale.com/ipn/ipnstate"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func makePeer(hostname, dnsName, osName string, sshEnabled bool, ips ...string) *ipnstate.PeerStatus {
	p := &ipnstate.PeerStatus{
		HostName:            hostname,
		DNSName:             dnsName,
		OS:                  osName,
		Online:              true,
		TailscaleSSHEnabled: sshEnabled,
	}
	for _, s := range ips {
		if a, err := netip.ParseAddr(s); err == nil {
			p.TailscaleIPs = append(p.TailscaleIPs, a)
		}
	}
	return p
}

// ── PeerSupportsSSH ───────────────────────────────────────────────────────────

func TestPeerSupportsSSH_TailscaleSSHEnabled(t *testing.T) {
	p := makePeer("alice", "", "linux", true, "100.64.0.1")
	if !PeerSupportsSSH(p) {
		t.Error("should support SSH when TailscaleSSHEnabled=true")
	}
}

func TestPeerSupportsSSH_LinuxOnline(t *testing.T) {
	p := makePeer("bob", "", "linux", false, "100.64.0.2")
	if !PeerSupportsSSH(p) {
		t.Error("online Linux peer should show SSH option")
	}
}

func TestPeerSupportsSSH_macOS(t *testing.T) {
	p := makePeer("macbook", "", "darwin", false, "100.64.0.3")
	if !PeerSupportsSSH(p) {
		t.Error("online macOS peer should show SSH option")
	}
}

func TestPeerSupportsSSH_Windows(t *testing.T) {
	p := makePeer("winpc", "", "windows", false, "100.64.0.4")
	if PeerSupportsSSH(p) {
		t.Error("Windows peer without TailscaleSSH should not show SSH option")
	}
}

func TestPeerSupportsSSH_WindowsWithTailscaleSSH(t *testing.T) {
	p := makePeer("winpc", "", "windows", true, "100.64.0.4")
	if !PeerSupportsSSH(p) {
		t.Error("Windows peer with TailscaleSSH enabled should show SSH option")
	}
}

func TestPeerSupportsSSH_Offline(t *testing.T) {
	p := makePeer("offline", "", "linux", true, "100.64.0.5")
	p.Online = false
	if PeerSupportsSSH(p) {
		t.Error("offline peer should never show SSH option")
	}
}

func TestPeerSupportsSSH_NoIP(t *testing.T) {
	p := &ipnstate.PeerStatus{HostName: "noip", OS: "linux", Online: true}
	if PeerSupportsSSH(p) {
		t.Error("peer with no IP and no TailscaleSSH should not show option")
	}
}

// ── Target ────────────────────────────────────────────────────────────────────

func TestTarget_DNSName(t *testing.T) {
	p := makePeer("alice-laptop", "alice-laptop.tailnet.example.ts.net.", "linux", false, "100.64.0.1")
	got := Target(p)
	want := "alice-laptop.tailnet.example.ts.net"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTarget_DNSName_NoTrailingDot(t *testing.T) {
	p := makePeer("alice", "alice.example.ts.net", "linux", false, "100.64.0.1")
	got := Target(p)
	if strings.HasSuffix(got, ".") {
		t.Errorf("Target should not have trailing dot, got %q", got)
	}
}

func TestTarget_HostnameFallback(t *testing.T) {
	p := makePeer("bob-desktop", "", "linux", false, "100.64.0.2")
	got := Target(p)
	if got != "bob-desktop" {
		t.Errorf("got %q, want bob-desktop", got)
	}
}

func TestTarget_IPFallback(t *testing.T) {
	p := &ipnstate.PeerStatus{Online: true, OS: "linux"}
	if a, _ := netip.ParseAddr("100.64.0.9"); true {
		p.TailscaleIPs = []netip.Addr{a}
	}
	got := Target(p)
	if got != "100.64.0.9" {
		t.Errorf("got %q, want 100.64.0.9", got)
	}
}

func TestTarget_Empty(t *testing.T) {
	p := &ipnstate.PeerStatus{}
	if got := Target(p); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

// ── SSHCommandString ──────────────────────────────────────────────────────────

func TestSSHCommandString_NoUser(t *testing.T) {
	p := makePeer("alice", "alice.ts.net.", "linux", true, "100.64.0.1")
	got := SSHCommandString(p, Config{})
	want := "ssh alice.ts.net"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSSHCommandString_WithUser(t *testing.T) {
	p := makePeer("alice", "alice.ts.net.", "linux", true, "100.64.0.1")
	got := SSHCommandString(p, Config{SSHUser: "admin"})
	want := "ssh -l admin alice.ts.net"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ── resolveTerminal ───────────────────────────────────────────────────────────

func TestResolveTerminal_Override(t *testing.T) {
	// xterm is almost always present; skip if not
	if _, err := lookPath("xterm"); err != nil {
		t.Skip("xterm not installed")
	}
	bin, args, err := resolveTerminal("xterm -e")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bin, "xterm") {
		t.Errorf("got bin %q, expected xterm", bin)
	}
	if len(args) == 0 || args[0] != "-e" {
		t.Errorf("got args %v, expected [-e]", args)
	}
}

func TestResolveTerminal_InvalidOverride(t *testing.T) {
	_, _, err := resolveTerminal("nonexistent-terminal-xyz -e")
	if err == nil {
		t.Error("expected error for nonexistent terminal")
	}
}

func TestResolveTerminal_EnvVar(t *testing.T) {
	if _, err := lookPath("xterm"); err != nil {
		t.Skip("xterm not installed")
	}
	os.Setenv("TERMINAL", "xterm")
	defer os.Unsetenv("TERMINAL")

	bin, _, err := resolveTerminal("")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bin, "xterm") {
		t.Errorf("expected xterm from $TERMINAL, got %q", bin)
	}
}

func TestResolveTerminal_AutoDetect(t *testing.T) {
	// Just verify it doesn't panic and either finds something or returns a
	// clear error — don't assert a specific terminal (CI has xterm available).
	os.Unsetenv("TERMINAL")
	bin, args, err := resolveTerminal("")
	if err != nil {
		t.Logf("no terminal found (acceptable in minimal CI): %v", err)
		return
	}
	t.Logf("detected terminal: %s %v", bin, args)
}

// ── DetectTerminal ────────────────────────────────────────────────────────────

func TestDetectTerminal_SameAsResolve(t *testing.T) {
	bin1, args1, err1 := resolveTerminal("")
	bin2, args2, err2 := DetectTerminal("")
	if (err1 == nil) != (err2 == nil) {
		t.Errorf("DetectTerminal and resolveTerminal disagree on error: %v vs %v", err1, err2)
	}
	if bin1 != bin2 {
		t.Errorf("bin mismatch: %q vs %q", bin1, bin2)
	}
	if len(args1) != len(args2) {
		t.Errorf("args length mismatch: %v vs %v", args1, args2)
	}
}

// lookPath is a test helper wrapping exec.LookPath.
func lookPath(name string) (string, error) {
	return exec.LookPath(name)
}
