// internal/client/client.go
//
// Wraps tailscale.com/client/local so the rest of the app stays insulated
// from the upstream API. All daemon interaction lives here.
package client

import (
	"context"
	"fmt"
	"io"
	"net/netip"
	"sort"
	"time"

	"tailscale.com/client/local"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
)

// Client is a handle to the local tailscaled daemon.
type Client struct {
	lc *local.Client
}

// New returns a Client connected to tailscaled.
// socketPath may be empty to use the platform default.
func New(ctx context.Context, socketPath string) (*Client, error) {
	lc := &local.Client{}
	if socketPath != "" {
		lc.Socket = socketPath
	}

	probe, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	if _, err := lc.Status(probe); err != nil {
		return nil, fmt.Errorf("tailscaled not reachable at %q: %w", lc.Socket, err)
	}
	return &Client{lc: lc}, nil
}

// ── Status ────────────────────────────────────────────────────────────────────

// Status returns the full daemon status.
func (c *Client) Status(ctx context.Context) (*ipnstate.Status, error) {
	return c.lc.Status(ctx)
}

// Self returns this device's own PeerStatus.
func (c *Client) Self(ctx context.Context) (*ipnstate.PeerStatus, error) {
	st, err := c.lc.Status(ctx)
	if err != nil {
		return nil, err
	}
	return st.Self, nil
}

// TailscaleIPs returns the Tailscale IP addresses for this device.
func (c *Client) TailscaleIPs(ctx context.Context) ([]netip.Addr, error) {
	st, err := c.lc.Status(ctx)
	if err != nil {
		return nil, err
	}
	return st.TailscaleIPs, nil
}

// ── Peers ─────────────────────────────────────────────────────────────────────

// Peers returns all peers, sorted by hostname.
func (c *Client) Peers(ctx context.Context) ([]*ipnstate.PeerStatus, error) {
	st, err := c.lc.Status(ctx)
	if err != nil {
		return nil, err
	}
	peers := make([]*ipnstate.PeerStatus, 0, len(st.Peer))
	for _, p := range st.Peer {
		peers = append(peers, p)
	}
	sort.Slice(peers, func(i, j int) bool {
		return peers[i].HostName < peers[j].HostName
	})
	return peers, nil
}

// ExitNodes returns peers that advertise themselves as exit nodes,
// sorted by hostname.
func (c *Client) ExitNodes(ctx context.Context) ([]*ipnstate.PeerStatus, error) {
	peers, err := c.Peers(ctx)
	if err != nil {
		return nil, err
	}
	var out []*ipnstate.PeerStatus
	for _, p := range peers {
		if p.ExitNodeOption {
			out = append(out, p)
		}
	}
	return out, nil
}

// ActiveExitNodeID returns the StableNodeID of the currently active exit node,
// or "" if none is set.
func (c *Client) ActiveExitNodeID(ctx context.Context) (ipn.StableNodeID, error) {
	prefs, err := c.lc.GetPrefs(ctx)
	if err != nil {
		return "", err
	}
	return prefs.ExitNodeID, nil
}

// ── Connect / Disconnect / Logout ─────────────────────────────────────────────

// Connect brings Tailscale up.
func (c *Client) Connect(ctx context.Context) error {
	_, err := c.lc.EditPrefs(ctx, &ipn.MaskedPrefs{
		WantRunningSet: true,
		Prefs:          ipn.Prefs{WantRunning: true},
	})
	return err
}

// Disconnect brings Tailscale down without logging out.
func (c *Client) Disconnect(ctx context.Context) error {
	_, err := c.lc.EditPrefs(ctx, &ipn.MaskedPrefs{
		WantRunningSet: true,
		Prefs:          ipn.Prefs{WantRunning: false},
	})
	return err
}

// Logout logs out the current account.
func (c *Client) Logout(ctx context.Context) error {
	return c.lc.Logout(ctx)
}

// ── Exit nodes ────────────────────────────────────────────────────────────────

// SetExitNode sets the exit node to the peer with the given StableNodeID.
func (c *Client) SetExitNode(ctx context.Context, id ipn.StableNodeID) error {
	_, err := c.lc.EditPrefs(ctx, &ipn.MaskedPrefs{
		ExitNodeIDSet: true,
		Prefs:         ipn.Prefs{ExitNodeID: id},
	})
	return err
}

// ClearExitNode removes the exit node selection.
func (c *Client) ClearExitNode(ctx context.Context) error {
	_, err := c.lc.EditPrefs(ctx, &ipn.MaskedPrefs{
		ExitNodeIDSet: true,
		Prefs:         ipn.Prefs{ExitNodeID: ""},
	})
	return err
}

// ── Preferences ───────────────────────────────────────────────────────────────

// Prefs returns the current IPN preferences.
func (c *Client) Prefs(ctx context.Context) (*ipn.Prefs, error) {
	return c.lc.GetPrefs(ctx)
}

// SetAcceptDNS enables or disables MagicDNS.
func (c *Client) SetAcceptDNS(ctx context.Context, accept bool) error {
	_, err := c.lc.EditPrefs(ctx, &ipn.MaskedPrefs{
		CorpDNSSet: true,
		Prefs:      ipn.Prefs{CorpDNS: accept},
	})
	return err
}

// SetAcceptRoutes enables or disables subnet route acceptance.
func (c *Client) SetAcceptRoutes(ctx context.Context, accept bool) error {
	_, err := c.lc.EditPrefs(ctx, &ipn.MaskedPrefs{
		RouteAllSet: true,
		Prefs:       ipn.Prefs{RouteAll: accept},
	})
	return err
}

// SetShieldsUp enables or disables shields-up mode (block all incoming).
func (c *Client) SetShieldsUp(ctx context.Context, up bool) error {
	_, err := c.lc.EditPrefs(ctx, &ipn.MaskedPrefs{
		ShieldsUpSet: true,
		Prefs:        ipn.Prefs{ShieldsUp: up},
	})
	return err
}

// ── IPN state bus ─────────────────────────────────────────────────────────────

// StateChange carries a meaningful IPN event.
type StateChange struct {
	State      ipn.State
	Error      string
	HasState   bool
	HasError   bool
}

// WatchState streams IPN state changes to fn. Blocks until ctx is cancelled.
// Automatically reconnects on transient errors. Call from a goroutine.
func (c *Client) WatchState(ctx context.Context, fn func(StateChange)) error {
	for {
		err := c.watchOnce(ctx, fn)
		if ctx.Err() != nil {
			return nil
		}
		// Transient error — back off and retry.
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(3 * time.Second):
		}
		_ = err
	}
}

func (c *Client) watchOnce(ctx context.Context, fn func(StateChange)) error {
	watcher, err := c.lc.WatchIPNBus(ctx, 0)
	if err != nil {
		return err
	}
	defer watcher.Close()
	for {
		n, err := watcher.Next()
		if err != nil {
			return err
		}
		if n.State != nil {
			fn(StateChange{State: *n.State, HasState: true})
		}
		if n.ErrMessage != nil && *n.ErrMessage != "" {
			fn(StateChange{Error: *n.ErrMessage, HasError: true})
		}
	}
}

// ── File sharing (Taildrop) ───────────────────────────────────────────────────

// LocalClient exposes the underlying local.Client for packages that need it
// directly (e.g. Taildrop, Ping).
func (c *Client) LocalClient() *local.Client {
	return c.lc
}

// PushFile sends a file to a peer. Thin pass-through to local.Client.PushFile.
func (c *Client) PushFile(ctx context.Context, target ipn.StableNodeID, size int64, name, contentType string, r io.Reader) error {
	return c.lc.PushFile(ctx, target, size, name, contentType, r)
}
