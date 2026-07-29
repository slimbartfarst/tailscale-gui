// internal/account/account.go
//
// Multi-account management via the Tailscale local API.
//
// Tailscale supports multiple login profiles on one device (added in
// tailscaled v1.56). Each profile is an independent logged-in account with
// its own node key, peers, and preferences.
//
// API surface used
// ────────────────
//   local.Client.ListProfiles()           → []ipn.LoginProfile
//   local.Client.SwitchProfile(id)        → error
//   local.Client.AddProfile()             → error   (creates blank profile, triggers NeedsLogin)
//   local.Client.DeleteProfile(id)        → error
//   local.Client.StartLoginInteractive()  → error   (kicks off browser-based login)
//   ipn.Notify.LoginFinished             field in IPN bus — signals auth complete
//   ipn.Status.AuthURL                   URL to open for pending login
//
// Flow for adding a second account
// ──────────────────────────────────
//   1. Call AddProfile()       → daemon creates empty profile
//   2. Daemon emits NeedsLogin state
//   3. Call StartLoginInteractive() → daemon emits an AuthURL in the next Notify
//   4. We open AuthURL in the browser
//   5. User authenticates → daemon emits LoginFinished
//   6. We reload profile list and switch to the new profile
package account

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"tailscale.com/ipn"
)

// Profile is a local representation of a Tailscale login profile.
type Profile struct {
	ID          ipn.ProfileID
	Name        string // display name: "alice@example.com" or tailnet name
	NetworkName string // tailnet name (e.g. "example.com")
	Active      bool   // is this the currently-active profile?
}

// Manager provides multi-account operations.
// It wraps a narrow interface so it can be tested without a real daemon.
type Manager struct {
	lc localClient
}

// localClient is the subset of local.Client used here.
// Using an interface makes unit testing possible.
type localClient interface {
	ProfileStatus(ctx context.Context) (current ipn.LoginProfile, all []ipn.LoginProfile, err error)
	SwitchProfile(ctx context.Context, id ipn.ProfileID) error
	SwitchToEmptyProfile(ctx context.Context) error
	DeleteProfile(ctx context.Context, id ipn.ProfileID) error
	StartLoginInteractive(ctx context.Context) error
	Logout(ctx context.Context) error
}

// New creates a Manager backed by the given local.Client.
// Pass a *local.Client directly; it satisfies the interface.
func New(lc localClient) *Manager {
	return &Manager{lc: lc}
}

// ── Reading ───────────────────────────────────────────────────────────────────

// Profiles returns all known login profiles, with the active one marked.
// Profiles are sorted: active first, then alphabetically by name.
func (m *Manager) Profiles(ctx context.Context) ([]Profile, error) {
	current, raw, err := m.lc.ProfileStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("list profiles: %w", err)
	}

	profiles := make([]Profile, 0, len(raw))
	for _, r := range raw {
		profiles = append(profiles, Profile{
			ID:          r.ID,
			Name:        displayName(r),
			NetworkName: r.NetworkProfile.MagicDNSName,
			Active:      r.ID == current.ID,
		})
	}

	sort.Slice(profiles, func(i, j int) bool {
		if profiles[i].Active != profiles[j].Active {
			return profiles[i].Active
		}
		return profiles[i].Name < profiles[j].Name
	})

	return profiles, nil
}

// ActiveProfile returns only the currently-active profile, or an error if
// none is active (e.g. no accounts added yet).
func (m *Manager) ActiveProfile(ctx context.Context) (Profile, error) {
	profiles, err := m.Profiles(ctx)
	if err != nil {
		return Profile{}, err
	}
	for _, p := range profiles {
		if p.Active {
			return p, nil
		}
	}
	return Profile{}, fmt.Errorf("no active profile")
}

// ── Writing ───────────────────────────────────────────────────────────────────

// Switch makes profileID the active profile.
// The daemon will transition to Running (if that profile is already logged in)
// or NeedsLogin (if auth is required).
func (m *Manager) Switch(ctx context.Context, id ipn.ProfileID) error {
	if err := m.lc.SwitchProfile(ctx, id); err != nil {
		return fmt.Errorf("switch profile %q: %w", id, err)
	}
	return nil
}

// AddAndLogin creates a new blank profile and starts the login flow.
// The daemon emits NeedsLogin; the caller should then watch for an AuthURL
// on the IPN bus and open it in the browser.
func (m *Manager) AddAndLogin(ctx context.Context) error {
	if err := m.lc.SwitchToEmptyProfile(ctx); err != nil {
		return fmt.Errorf("add profile: %w", err)
	}
	if err := m.lc.StartLoginInteractive(ctx); err != nil {
		return fmt.Errorf("start login: %w", err)
	}
	return nil
}

// Delete removes a profile permanently.
// You cannot delete the currently-active profile; switch away first.
func (m *Manager) Delete(ctx context.Context, id ipn.ProfileID) error {
	if err := m.lc.DeleteProfile(ctx, id); err != nil {
		return fmt.Errorf("delete profile %q: %w", id, err)
	}
	return nil
}

// Logout logs out the current profile (keeps the profile entry but clears auth).
func (m *Manager) Logout(ctx context.Context) error {
	if err := m.lc.Logout(ctx); err != nil {
		return fmt.Errorf("logout: %w", err)
	}
	return nil
}

// StartLoginInteractive triggers browser-based login for the current profile.
// After calling this, watch the IPN bus for an AuthURL in the next Notify.
func (m *Manager) StartLoginInteractive(ctx context.Context) error {
	if err := m.lc.StartLoginInteractive(ctx); err != nil {
		return fmt.Errorf("start login interactive: %w", err)
	}
	return nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// displayName returns the best human-readable name for a profile.
// Priority: DisplayName > UserProfile.LoginName > NetworkMagicDNSSuffix > ID
func displayName(p ipn.LoginProfile) string {
	if p.Name != "" {
		return p.Name
	}
	if p.UserProfile.LoginName != "" {
		return p.UserProfile.LoginName
	}
	if p.NetworkProfile.MagicDNSName != "" {
		// "alice.example.ts.net" → "example.ts.net" (strip first label)
		parts := strings.SplitN(p.NetworkProfile.MagicDNSName, ".", 2)
		if len(parts) == 2 {
			return parts[1]
		}
		return p.NetworkProfile.MagicDNSName
	}
	return string(p.ID)
}

// IsMultiAccountSupported checks whether the running daemon supports profile
// switching (tailscaled v1.56+). Returns false if ListProfiles errors with
// "not implemented" or "unimplemented".
func IsMultiAccountSupported(ctx context.Context, lc localClient) bool {
	_, _, err := lc.ProfileStatus(ctx)
	if err == nil {
		return true
	}
	msg := strings.ToLower(err.Error())
	return !strings.Contains(msg, "not implement") &&
		!strings.Contains(msg, "unimplemented") &&
		!strings.Contains(msg, "unknown method")
}
