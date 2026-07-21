// internal/picker/picker_test.go
package picker

import (
	"testing"

	"net/netip"
	"tailscale.com/ipn/ipnstate"
)

// makePeer is a test helper.
func makePeer(hostname, os string, ips ...string) *ipnstate.PeerStatus {
	p := &ipnstate.PeerStatus{
		HostName: hostname,
		OS:       os,
		Online:   true,
	}
	for _, ipStr := range ips {
		addr, err := netip.ParseAddr(ipStr)
		if err == nil {
			p.TailscaleIPs = append(p.TailscaleIPs, addr)
		}
	}
	return p
}

var testPeers = []*ipnstate.PeerStatus{
	makePeer("alice-laptop", "linux", "100.64.0.1"),
	makePeer("bob-desktop", "windows", "100.64.0.2"),
	makePeer("charlie-phone", "android", "100.64.0.3"),
	makePeer("alice-phone", "ios", "100.64.0.4"),
}

func TestMatchPeer_ExactHostname(t *testing.T) {
	p, err := matchPeer(testPeers, "bob-desktop")
	if err != nil {
		t.Fatal(err)
	}
	if p.HostName != "bob-desktop" {
		t.Errorf("got %s, want bob-desktop", p.HostName)
	}
}

func TestMatchPeer_CaseInsensitive(t *testing.T) {
	p, err := matchPeer(testPeers, "BOB-DESKTOP")
	if err != nil {
		t.Fatal(err)
	}
	if p.HostName != "bob-desktop" {
		t.Errorf("got %s", p.HostName)
	}
}

func TestMatchPeer_ExactIP(t *testing.T) {
	p, err := matchPeer(testPeers, "100.64.0.3")
	if err != nil {
		t.Fatal(err)
	}
	if p.HostName != "charlie-phone" {
		t.Errorf("got %s", p.HostName)
	}
}

func TestMatchPeer_Number(t *testing.T) {
	p, err := matchPeer(testPeers, "2")
	if err != nil {
		t.Fatal(err)
	}
	if p.HostName != "bob-desktop" {
		t.Errorf("got %s", p.HostName)
	}
}

func TestMatchPeer_NumberOutOfRange(t *testing.T) {
	_, err := matchPeer(testPeers, "99")
	if err == nil {
		t.Fatal("expected error for out-of-range number")
	}
}

func TestMatchPeer_UniquePrefix(t *testing.T) {
	// "bob" is unique
	p, err := matchPeer(testPeers, "bob")
	if err != nil {
		t.Fatal(err)
	}
	if p.HostName != "bob-desktop" {
		t.Errorf("got %s", p.HostName)
	}
}

func TestMatchPeer_AmbiguousPrefix(t *testing.T) {
	// "alice" matches alice-laptop AND alice-phone
	_, err := matchPeer(testPeers, "alice")
	if err == nil {
		t.Fatal("expected ambiguous error")
	}
}

func TestMatchPeer_IPPrefix(t *testing.T) {
	// "100.64.0.2" exact
	p, err := matchPeer(testPeers, "100.64.0.2")
	if err != nil {
		t.Fatal(err)
	}
	if p.HostName != "bob-desktop" {
		t.Errorf("got %s", p.HostName)
	}
}

func TestMatchPeer_NoMatch(t *testing.T) {
	_, err := matchPeer(testPeers, "nobody")
	if err == nil {
		t.Fatal("expected error for no match")
	}
}

func TestMatchPeer_SinglePeer(t *testing.T) {
	single := []*ipnstate.PeerStatus{makePeer("only-one", "linux", "100.64.1.1")}
	p, err := matchPeer(single, "1")
	if err != nil {
		t.Fatal(err)
	}
	if p.HostName != "only-one" {
		t.Errorf("got %s", p.HostName)
	}
}

func TestOsLabel(t *testing.T) {
	cases := []struct{ in, want string }{
		{"linux", "Linux"},
		{"windows", "Windows"},
		{"darwin", "macOS"},
		{"android", "Android"},
		{"ios", "iOS"},
		{"freebsd", "FreeBSD"},
		{"", "—"},
		{"plan9", "plan9"}, // unknown passthrough
	}
	for _, c := range cases {
		got := osLabel(c.in)
		if got != c.want {
			t.Errorf("osLabel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFirstIP_Empty(t *testing.T) {
	p := &ipnstate.PeerStatus{}
	if got := firstIP(p); got != "—" {
		t.Errorf("got %q, want —", got)
	}
}

func TestFirstIP_Present(t *testing.T) {
	p := makePeer("x", "linux", "100.100.1.2")
	if got := firstIP(p); got != "100.100.1.2" {
		t.Errorf("got %q", got)
	}
}
