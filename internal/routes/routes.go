// internal/routes/routes.go
//
// Subnet route advertising — the "server side" of Tailscale subnets.
//
// Concepts
// ────────
// AdvertisedRoutes  Prefixes this device is offering to the tailnet.
//                   Set via ipn.Prefs.AdvertiseRoutes.
//                   Changes take effect immediately but the admin console
//                   must approve them before peers can use them (unless
//                   auto-approval is on in the tailnet policy).
//
// ApprovedRoutes    Routes the admin console has approved.
//                   Read from ipnstate.PeerStatus.PrimaryRoutes on Self.
//
// This package provides:
//   - Route           — a parsed, annotated prefix
//   - Manager         — reads/writes routes via client.Client
//   - Suggest()       — auto-discovers local interfaces and proposes sensible
//                       prefixes to advertise (LAN subnets, RFC-1918, etc.)
//   - Validate()      — checks a CIDR string before sending it to the daemon
package routes

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"

	"github.com/yourname/tailscale-gui/internal/client"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
)

// Route represents a single advertised (or candidate) subnet prefix.
type Route struct {
	Prefix   netip.Prefix
	Approved bool   // approved by the admin console
	Label    string // human-readable description, e.g. "Home LAN (192.168.1.0/24)"
}

// String returns the CIDR notation of the prefix.
func (r Route) String() string { return r.Prefix.String() }

// Manager handles reading and writing advertised subnet routes.
type Manager struct {
	ts *client.Client
}

// New creates a Manager.
func New(ts *client.Client) *Manager {
	return &Manager{ts: ts}
}

// ── Reading ───────────────────────────────────────────────────────────────────

// Current returns the routes this device is currently advertising, annotated
// with approval status.
func (m *Manager) Current(ctx context.Context) ([]Route, error) {
	prefs, err := m.ts.Prefs(ctx)
	if err != nil {
		return nil, fmt.Errorf("routes current: %w", err)
	}

	// Get approved routes from the daemon status.
	approvedSet, err := m.approvedRoutes(ctx)
	if err != nil {
		// Non-fatal — we can still show what's advertised.
		approvedSet = map[netip.Prefix]bool{}
	}

	routes := make([]Route, 0, len(prefs.AdvertiseRoutes))
	for _, pfx := range prefs.AdvertiseRoutes {
		r := Route{
			Prefix:   pfx,
			Approved: approvedSet[pfx],
			Label:    labelFor(pfx),
		}
		routes = append(routes, r)
	}
	sort.Slice(routes, func(i, j int) bool {
		return routes[i].Prefix.String() < routes[j].Prefix.String()
	})
	return routes, nil
}

// approvedRoutes returns the set of routes approved by the admin console,
// read from the self peer's PrimaryRoutes field.
func (m *Manager) approvedRoutes(ctx context.Context) (map[netip.Prefix]bool, error) {
	st, err := m.ts.Status(ctx)
	if err != nil {
		return nil, err
	}
	approved := map[netip.Prefix]bool{}
	if st.Self == nil {
		return approved, nil
	}
	for _, pfx := range primaryRoutes(st.Self) {
		approved[pfx] = true
	}
	return approved, nil
}

// primaryRoutes extracts PrimaryRoutes from a PeerStatus as a plain slice.
// In tailscale v1.56+ PrimaryRoutes is views.Slice[netip.Prefix]; we use
// the .AsSlice() method which returns []netip.Prefix regardless of the
// underlying representation.
func primaryRoutes(self *ipnstate.PeerStatus) []netip.Prefix {
	return self.PrimaryRoutes.AsSlice()
}

// ── Writing ───────────────────────────────────────────────────────────────────

// Set replaces the full set of advertised routes with the given prefixes.
// Pass an empty slice to stop advertising everything.
func (m *Manager) Set(ctx context.Context, prefixes []netip.Prefix) error {
	_, err := m.ts.LocalClient().EditPrefs(ctx, &ipn.MaskedPrefs{
		AdvertiseRoutesSet: true,
		Prefs: ipn.Prefs{
			AdvertiseRoutes: prefixes,
		},
	})
	if err != nil {
		return fmt.Errorf("set routes: %w", err)
	}
	return nil
}

// Add advertises an additional prefix. If it is already advertised, this is
// a no-op.
func (m *Manager) Add(ctx context.Context, prefix netip.Prefix) error {
	current, err := m.Current(ctx)
	if err != nil {
		return err
	}
	normalized := prefix.Masked() // normalise host bits (e.g. 192.168.1.5/24 → 192.168.1.0/24)
	for _, r := range current {
		if r.Prefix == normalized {
			return nil // already advertising
		}
	}
	prefixes := make([]netip.Prefix, 0, len(current)+1)
	for _, r := range current {
		prefixes = append(prefixes, r.Prefix)
	}
	prefixes = append(prefixes, normalized)
	return m.Set(ctx, prefixes)
}

// Remove stops advertising a specific prefix.
func (m *Manager) Remove(ctx context.Context, prefix netip.Prefix) error {
	current, err := m.Current(ctx)
	if err != nil {
		return err
	}
	normalized := prefix.Masked()
	prefixes := make([]netip.Prefix, 0, len(current))
	for _, r := range current {
		if r.Prefix != normalized {
			prefixes = append(prefixes, r.Prefix)
		}
	}
	return m.Set(ctx, prefixes)
}

// Toggle adds a prefix if not present, removes it if present.
// Returns whether the prefix is now advertised (true) or not (false).
func (m *Manager) Toggle(ctx context.Context, prefix netip.Prefix) (advertising bool, err error) {
	current, err := m.Current(ctx)
	if err != nil {
		return false, err
	}
	normalized := prefix.Masked()
	found := false
	prefixes := make([]netip.Prefix, 0, len(current))
	for _, r := range current {
		if r.Prefix == normalized {
			found = true
			// Skip — removing it
		} else {
			prefixes = append(prefixes, r.Prefix)
		}
	}
	if !found {
		prefixes = append(prefixes, normalized)
	}
	if err := m.Set(ctx, prefixes); err != nil {
		return false, err
	}
	return !found, nil
}

// ── Validation ────────────────────────────────────────────────────────────────

// Validate checks that a CIDR string is a valid, non-link-local prefix that
// makes sense to advertise. Returns a user-facing error string or "".
func Validate(cidr string) error {
	cidr = strings.TrimSpace(cidr)
	if cidr == "" {
		return fmt.Errorf("prefix cannot be empty")
	}

	// Must parse as a prefix.
	pfx, err := netip.ParsePrefix(cidr)
	if err != nil {
		// Also try parsing as a bare address and defaulting to /32 or /128.
		addr, addrErr := netip.ParseAddr(cidr)
		if addrErr != nil {
			return fmt.Errorf("not a valid CIDR prefix (e.g. 192.168.1.0/24): %v", err)
		}
		bits := 32
		if addr.Is6() {
			bits = 128
		}
		pfx = netip.PrefixFrom(addr, bits)
	}

	// Reject link-local.
	if pfx.Addr().IsLinkLocalUnicast() || pfx.Addr().IsLinkLocalMulticast() {
		return fmt.Errorf("link-local addresses (%s) cannot be advertised", pfx)
	}

	// Reject loopback.
	if pfx.Addr().IsLoopback() {
		return fmt.Errorf("loopback addresses cannot be advertised")
	}

	// Reject Tailscale's own range (100.64.0.0/10).
	tsRange := netip.MustParsePrefix("100.64.0.0/10")
	if tsRange.Overlaps(pfx) {
		return fmt.Errorf("the Tailscale address range (100.64.0.0/10) cannot be advertised")
	}

	// Warn if prefix is too broad (e.g. 0.0.0.0/0 as a regular route, not exit node).
	if pfx.Bits() == 0 {
		return fmt.Errorf("use the exit node feature for default routes (0.0.0.0/0)")
	}

	return nil
}

// ParsePrefix parses and normalises a CIDR string (host bits are masked).
func ParsePrefix(cidr string) (netip.Prefix, error) {
	cidr = strings.TrimSpace(cidr)
	pfx, err := netip.ParsePrefix(cidr)
	if err != nil {
		// Bare IP → /32 or /128
		addr, addrErr := netip.ParseAddr(cidr)
		if addrErr != nil {
			return netip.Prefix{}, err
		}
		bits := 32
		if addr.Is6() {
			bits = 128
		}
		pfx = netip.PrefixFrom(addr, bits)
	}
	return pfx.Masked(), nil
}

// ── Suggestions ───────────────────────────────────────────────────────────────

// Suggestion is a candidate route discovered from the local system.
type Suggestion struct {
	Prefix    netip.Prefix
	Interface string // e.g. "eth0", "wlan0"
	Label     string // human-readable
}

// Suggest returns local network interfaces that look like good candidates to
// advertise (RFC-1918 subnets reachable from this machine).
// The caller should present these to the user and let them choose.
func Suggest() ([]Suggestion, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("list interfaces: %w", err)
	}

	var out []Suggestion
	seen := map[netip.Prefix]bool{}

	for _, iface := range ifaces {
		// Skip loopback, down, and virtual/tunnel interfaces.
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		if isVirtualInterface(iface.Name) {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			var pfx netip.Prefix
			switch v := addr.(type) {
			case *net.IPNet:
				a, ok := netip.AddrFromSlice(v.IP)
				if !ok {
					continue
				}
				ones, _ := v.Mask.Size()
				pfx = netip.PrefixFrom(a.Unmap(), ones).Masked()
			case *net.IPAddr:
				a, ok := netip.AddrFromSlice(v.IP)
				if !ok {
					continue
				}
				bits := 32
				if a.Is6() {
					bits = 128
				}
				pfx = netip.PrefixFrom(a.Unmap(), bits).Masked()
			}

			if !pfx.IsValid() || pfx.Addr().IsLoopback() || pfx.Addr().IsLinkLocalUnicast() {
				continue
			}
			if !isPrivate(pfx.Addr()) {
				continue
			}
			if seen[pfx] {
				continue
			}
			if Validate(pfx.String()) != nil {
				continue
			}

			seen[pfx] = true
			out = append(out, Suggestion{
				Prefix:    pfx,
				Interface: iface.Name,
				Label:     fmt.Sprintf("%s via %s", pfx, iface.Name),
			})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].Prefix.String() < out[j].Prefix.String()
	})
	return out, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// isPrivate reports whether addr is in an RFC-1918 / RFC-4193 private range.
func isPrivate(addr netip.Addr) bool {
	private4 := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.168.0.0/16"),
	}
	private6 := []netip.Prefix{
		netip.MustParsePrefix("fc00::/7"), // ULA
	}
	ranges := private4
	if addr.Is6() {
		ranges = private6
	}
	for _, r := range ranges {
		if r.Contains(addr) {
			return true
		}
	}
	return false
}

// isVirtualInterface reports whether an interface name looks like a virtual
// or tunnel interface we should skip for route suggestions.
func isVirtualInterface(name string) bool {
	prefixes := []string{
		"tailscale", "tun", "tap", "docker", "br-", "veth",
		"virbr", "lo", "dummy", "bond", "team", "wg",
	}
	nameLower := strings.ToLower(name)
	for _, p := range prefixes {
		if strings.HasPrefix(nameLower, p) {
			return true
		}
	}
	return false
}

// labelFor returns a human-friendly label for a known prefix range.
func labelFor(pfx netip.Prefix) string {
	known := []struct {
		prefix netip.Prefix
		label  string
	}{
		{netip.MustParsePrefix("10.0.0.0/8"), "RFC-1918 Class A (10.x.x.x)"},
		{netip.MustParsePrefix("172.16.0.0/12"), "RFC-1918 Class B (172.16-31.x.x)"},
		{netip.MustParsePrefix("192.168.0.0/16"), "RFC-1918 Class C (192.168.x.x)"},
		{netip.MustParsePrefix("fc00::/7"), "IPv6 ULA"},
	}
	for _, k := range known {
		if k.prefix == pfx {
			return k.label
		}
		// Subnet of a known range
		if k.prefix.Contains(pfx.Addr()) && pfx.Bits() >= k.prefix.Bits() {
			return fmt.Sprintf("%s subnet", labelShort(k.label))
		}
	}
	return pfx.String()
}

func labelShort(label string) string {
	if i := strings.Index(label, " ("); i > 0 {
		return label[:i]
	}
	return label
}
