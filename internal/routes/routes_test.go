// internal/routes/routes_test.go
package routes

import (
	"net/netip"
	"testing"
)

// ── Validate ──────────────────────────────────────────────────────────────────

func TestValidate_Valid(t *testing.T) {
	cases := []string{
		"192.168.1.0/24",
		"10.0.0.0/8",
		"172.16.0.0/12",
		"10.10.5.0/24",
		"192.168.100.0/22",
		"fc00::/7",
		"fd00::/8",
	}
	for _, c := range cases {
		if err := Validate(c); err != nil {
			t.Errorf("Validate(%q) unexpected error: %v", c, err)
		}
	}
}

func TestValidate_Invalid(t *testing.T) {
	cases := []struct {
		input string
		desc  string
	}{
		{"", "empty"},
		{"not-a-cidr", "garbage"},
		{"169.254.0.0/16", "link-local"},
		{"fe80::/10", "IPv6 link-local"},
		{"127.0.0.1/8", "loopback"},
		{"100.64.0.0/10", "Tailscale range exact"},
		{"100.100.0.0/16", "Tailscale range subset"},
		{"0.0.0.0/0", "default route"},
	}
	for _, c := range cases {
		if err := Validate(c.input); err == nil {
			t.Errorf("Validate(%q) should fail (%s)", c.input, c.desc)
		}
	}
}

// ── ParsePrefix ───────────────────────────────────────────────────────────────

func TestParsePrefix_CIDR(t *testing.T) {
	pfx, err := ParsePrefix("192.168.1.0/24")
	if err != nil {
		t.Fatal(err)
	}
	if pfx.String() != "192.168.1.0/24" {
		t.Errorf("got %s", pfx)
	}
}

func TestParsePrefix_NormalisesHostBits(t *testing.T) {
	// 192.168.1.5/24 → 192.168.1.0/24
	pfx, err := ParsePrefix("192.168.1.5/24")
	if err != nil {
		t.Fatal(err)
	}
	if pfx.String() != "192.168.1.0/24" {
		t.Errorf("host bits not masked: got %s", pfx)
	}
}

func TestParsePrefix_BareIPv4(t *testing.T) {
	pfx, err := ParsePrefix("10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if pfx.Bits() != 32 {
		t.Errorf("expected /32, got /%d", pfx.Bits())
	}
}

func TestParsePrefix_BareIPv6(t *testing.T) {
	pfx, err := ParsePrefix("fd00::1")
	if err != nil {
		t.Fatal(err)
	}
	if pfx.Bits() != 128 {
		t.Errorf("expected /128, got /%d", pfx.Bits())
	}
}

// ── isPrivate ─────────────────────────────────────────────────────────────────

func TestIsPrivate(t *testing.T) {
	yes := []string{"10.0.0.1", "172.16.5.1", "192.168.1.100", "fd12::1"}
	no := []string{"8.8.8.8", "1.1.1.1", "203.0.113.1", "2001:db8::1"}

	for _, ip := range yes {
		addr := netip.MustParseAddr(ip)
		if !isPrivate(addr) {
			t.Errorf("isPrivate(%s) should be true", ip)
		}
	}
	for _, ip := range no {
		addr := netip.MustParseAddr(ip)
		if isPrivate(addr) {
			t.Errorf("isPrivate(%s) should be false", ip)
		}
	}
}

// ── isVirtualInterface ────────────────────────────────────────────────────────

func TestIsVirtualInterface(t *testing.T) {
	virtual := []string{"tailscale0", "tun0", "docker0", "br-abc123", "veth1a2b", "wg0", "lo"}
	real := []string{"eth0", "enp3s0", "wlan0", "ens160"}

	for _, n := range virtual {
		if !isVirtualInterface(n) {
			t.Errorf("isVirtualInterface(%s) should be true", n)
		}
	}
	for _, n := range real {
		if isVirtualInterface(n) {
			t.Errorf("isVirtualInterface(%s) should be false", n)
		}
	}
}

// ── labelFor ─────────────────────────────────────────────────────────────────

func TestLabelFor_ExactMatch(t *testing.T) {
	pfx := netip.MustParsePrefix("10.0.0.0/8")
	label := labelFor(pfx)
	if label != "RFC-1918 Class A (10.x.x.x)" {
		t.Errorf("got %q", label)
	}
}

func TestLabelFor_Subnet(t *testing.T) {
	pfx := netip.MustParsePrefix("192.168.5.0/24")
	label := labelFor(pfx)
	if label == pfx.String() {
		t.Errorf("expected a friendly label, got raw prefix %q", label)
	}
	t.Logf("label: %s", label)
}

func TestLabelFor_Unknown(t *testing.T) {
	pfx := netip.MustParsePrefix("203.0.113.0/24")
	label := labelFor(pfx)
	if label != pfx.String() {
		t.Errorf("expected raw prefix for unknown range, got %q", label)
	}
}

// ── Suggest ───────────────────────────────────────────────────────────────────

func TestSuggest_NoPanic(t *testing.T) {
	// Just verify it runs without panicking in the test environment.
	// The result depends on the machine's network config.
	suggestions, err := Suggest()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("suggestions: %d found", len(suggestions))
	for _, s := range suggestions {
		t.Logf("  %s via %s", s.Prefix, s.Interface)
	}
}
