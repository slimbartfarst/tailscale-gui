// internal/account/account_test.go
package account

import (
	"context"
	"errors"
	"testing"

	"tailscale.com/ipn"
	"tailscale.com/tailcfg"
)

// ── mock localClient ──────────────────────────────────────────────────────────

type mockClient struct {
	profiles    []ipn.LoginProfile
	switchedTo  ipn.ProfileID
	addCalled   bool
	deletedID   ipn.ProfileID
	loginCalled bool
	logoutCalled bool
	listErr     error
	switchErr   error
	addErr      error
	deleteErr   error
	loginErr    error
}

func (m *mockClient) ListProfiles(_ context.Context) ([]ipn.LoginProfile, error) {
	return m.profiles, m.listErr
}
func (m *mockClient) SwitchProfile(_ context.Context, id ipn.ProfileID) error {
	m.switchedTo = id
	return m.switchErr
}
func (m *mockClient) AddProfile(_ context.Context) error {
	m.addCalled = true
	return m.addErr
}
func (m *mockClient) DeleteProfile(_ context.Context, id ipn.ProfileID) error {
	m.deletedID = id
	return m.deleteErr
}
func (m *mockClient) StartLoginInteractive(_ context.Context) error {
	m.loginCalled = true
	return m.loginErr
}
func (m *mockClient) Logout(_ context.Context) error {
	m.logoutCalled = true
	return nil
}

// ── Profiles ──────────────────────────────────────────────────────────────────

func TestProfiles_Empty(t *testing.T) {
	mgr := New(&mockClient{})
	profiles, err := mgr.Profiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 0 {
		t.Errorf("expected 0 profiles, got %d", len(profiles))
	}
}

func TestProfiles_ActiveFirst(t *testing.T) {
	mc := &mockClient{
		profiles: []ipn.LoginProfile{
			{ID: "b", Name: "bob@example.com", Active: false},
			{ID: "a", Name: "alice@example.com", Active: true},
		},
	}
	mgr := New(mc)
	profiles, err := mgr.Profiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 2 {
		t.Fatalf("expected 2, got %d", len(profiles))
	}
	if !profiles[0].Active {
		t.Error("first profile should be active")
	}
	if profiles[0].ID != "a" {
		t.Errorf("expected alice first, got %q", profiles[0].ID)
	}
}

func TestProfiles_SortedAlphabetically(t *testing.T) {
	mc := &mockClient{
		profiles: []ipn.LoginProfile{
			{ID: "z", Name: "zoe@example.com", Active: false},
			{ID: "a", Name: "alice@example.com", Active: false},
			{ID: "m", Name: "mallory@example.com", Active: false},
		},
	}
	mgr := New(mc)
	profiles, _ := mgr.Profiles(context.Background())
	if profiles[0].Name != "alice@example.com" {
		t.Errorf("wrong order: %v", profiles[0].Name)
	}
	if profiles[2].Name != "zoe@example.com" {
		t.Errorf("wrong order: %v", profiles[2].Name)
	}
}

func TestProfiles_ListError(t *testing.T) {
	mc := &mockClient{listErr: errors.New("daemon not running")}
	mgr := New(mc)
	_, err := mgr.Profiles(context.Background())
	if err == nil {
		t.Error("expected error")
	}
}

// ── ActiveProfile ─────────────────────────────────────────────────────────────

func TestActiveProfile_Found(t *testing.T) {
	mc := &mockClient{
		profiles: []ipn.LoginProfile{
			{ID: "a", Name: "alice@example.com", Active: true},
			{ID: "b", Name: "bob@example.com", Active: false},
		},
	}
	mgr := New(mc)
	p, err := mgr.ActiveProfile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "a" {
		t.Errorf("got %q, want a", p.ID)
	}
}

func TestActiveProfile_NoneActive(t *testing.T) {
	mc := &mockClient{
		profiles: []ipn.LoginProfile{
			{ID: "b", Name: "bob@example.com", Active: false},
		},
	}
	mgr := New(mc)
	_, err := mgr.ActiveProfile(context.Background())
	if err == nil {
		t.Error("expected error when no active profile")
	}
}

// ── Switch ────────────────────────────────────────────────────────────────────

func TestSwitch_CallsClient(t *testing.T) {
	mc := &mockClient{}
	mgr := New(mc)
	if err := mgr.Switch(context.Background(), "target-id"); err != nil {
		t.Fatal(err)
	}
	if mc.switchedTo != "target-id" {
		t.Errorf("got %q, want target-id", mc.switchedTo)
	}
}

func TestSwitch_Error(t *testing.T) {
	mc := &mockClient{switchErr: errors.New("not allowed")}
	mgr := New(mc)
	if err := mgr.Switch(context.Background(), "x"); err == nil {
		t.Error("expected error")
	}
}

// ── AddAndLogin ───────────────────────────────────────────────────────────────

func TestAddAndLogin_BothCalled(t *testing.T) {
	mc := &mockClient{}
	mgr := New(mc)
	if err := mgr.AddAndLogin(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !mc.addCalled {
		t.Error("AddProfile should have been called")
	}
	if !mc.loginCalled {
		t.Error("StartLoginInteractive should have been called")
	}
}

func TestAddAndLogin_AddError(t *testing.T) {
	mc := &mockClient{addErr: errors.New("max profiles")}
	mgr := New(mc)
	if err := mgr.AddAndLogin(context.Background()); err == nil {
		t.Error("expected error")
	}
	// StartLoginInteractive should NOT be called if AddProfile fails
	if mc.loginCalled {
		t.Error("StartLoginInteractive should not be called after AddProfile error")
	}
}

// ── Delete ────────────────────────────────────────────────────────────────────

func TestDelete_CallsClient(t *testing.T) {
	mc := &mockClient{}
	mgr := New(mc)
	if err := mgr.Delete(context.Background(), "del-id"); err != nil {
		t.Fatal(err)
	}
	if mc.deletedID != "del-id" {
		t.Errorf("got %q, want del-id", mc.deletedID)
	}
}

// ── displayName ───────────────────────────────────────────────────────────────

func TestDisplayName_Name(t *testing.T) {
	p := ipn.LoginProfile{Name: "alice@example.com"}
	if got := displayName(p); got != "alice@example.com" {
		t.Errorf("got %q", got)
	}
}

func TestDisplayName_LoginName(t *testing.T) {
	p := ipn.LoginProfile{
		UserProfile: tailcfg.UserProfile{LoginName: "bob@corp.example"},
	}
	if got := displayName(p); got != "bob@corp.example" {
		t.Errorf("got %q", got)
	}
}

func TestDisplayName_NetworkSuffix(t *testing.T) {
	p := ipn.LoginProfile{NetworkMagicDNSSuffix: "example.ts.net"}
	got := displayName(p)
	if got == "" {
		t.Error("expected non-empty display name from network suffix")
	}
}

func TestDisplayName_IDFallback(t *testing.T) {
	p := ipn.LoginProfile{ID: "profile-abc123"}
	if got := displayName(p); got != "profile-abc123" {
		t.Errorf("got %q, want profile-abc123", got)
	}
}

// ── IsMultiAccountSupported ───────────────────────────────────────────────────

func TestIsMultiAccountSupported_Success(t *testing.T) {
	mc := &mockClient{profiles: []ipn.LoginProfile{}}
	if !IsMultiAccountSupported(context.Background(), mc) {
		t.Error("should be supported when ListProfiles succeeds")
	}
}

func TestIsMultiAccountSupported_NotImplemented(t *testing.T) {
	mc := &mockClient{listErr: errors.New("not implemented")}
	if IsMultiAccountSupported(context.Background(), mc) {
		t.Error("should not be supported when not implemented")
	}
}

func TestIsMultiAccountSupported_OtherError(t *testing.T) {
	// A real daemon error (e.g. connection refused) should not be mistaken
	// for "not supported".
	mc := &mockClient{listErr: errors.New("connection refused")}
	if !IsMultiAccountSupported(context.Background(), mc) {
		t.Error("connection error should not mark feature as unsupported")
	}
}
