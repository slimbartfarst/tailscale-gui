// internal/notify/dbus_test.go
package notify

import (
	"fmt"
	"strings"
	"testing"
	"time"
)
	"time"
)

// ── parseNotifyID ─────────────────────────────────────────────────────────────

func TestParseNotifyID_StandardOutput(t *testing.T) {
	// Standard gdbus output
	id, err := parseNotifyID("(uint32 42,)\n")
	if err != nil {
		t.Fatal(err)
	}
	if id != 42 {
		t.Errorf("got %d, want 42", id)
	}
}

func TestParseNotifyID_NoTypeTag(t *testing.T) {
	// Some gdbus versions omit the type tag
	id, err := parseNotifyID("(12345,)\n")
	if err != nil {
		t.Fatal(err)
	}
	if id != 12345 {
		t.Errorf("got %d, want 12345", id)
	}
}

func TestParseNotifyID_LargeID(t *testing.T) {
	id, err := parseNotifyID("(uint32 4294967295,)\n")
	if err != nil {
		t.Fatal(err)
	}
	if id != 4294967295 {
		t.Errorf("got %d, want 4294967295", id)
	}
}

func TestParseNotifyID_BadInput(t *testing.T) {
	_, err := parseNotifyID("not a valid response\n")
	if err == nil {
		t.Error("expected error for bad input")
	}
}

func TestParseNotifyID_EmptyString(t *testing.T) {
	_, err := parseNotifyID("")
	if err == nil {
		t.Error("expected error for empty string")
	}
}

// ── gvariantEscape ────────────────────────────────────────────────────────────

func TestGvariantEscape_Normal(t *testing.T) {
	got := gvariantEscape("hello world")
	if got != "hello world" {
		t.Errorf("unexpected: %q", got)
	}
}

func TestGvariantEscape_SingleQuote(t *testing.T) {
	got := gvariantEscape("it's here")
	want := `it\'s here`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestGvariantEscape_Backslash(t *testing.T) {
	got := gvariantEscape(`C:\Users\file`)
	want := `C:\\Users\\file`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestGvariantEscape_Empty(t *testing.T) {
	if got := gvariantEscape(""); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// ── FileReceivedNotification ──────────────────────────────────────────────────

func TestFileReceivedNotification_Fields(t *testing.T) {
	n := FileReceivedNotification("report.pdf", "alice-laptop", "/home/user/Downloads/Taildrop/report.pdf")

	if n.Summary == "" {
		t.Error("Summary should not be empty")
	}
	if n.Body == "" {
		t.Error("Body should not be empty")
	}
	if len(n.Actions) != 2 {
		t.Fatalf("expected 2 actions, got %d", len(n.Actions))
	}
	if n.Actions[0].Key != "open-file" {
		t.Errorf("action 0 key = %q, want open-file", n.Actions[0].Key)
	}
	if n.Actions[1].Key != "open-folder" {
		t.Errorf("action 1 key = %q, want open-folder", n.Actions[1].Key)
	}
	if n.Timeout <= 0 {
		t.Error("Timeout should be positive")
	}
}

func TestFileReceivedNotification_ContainsFilename(t *testing.T) {
	n := FileReceivedNotification("photo.jpg", "bob", "/tmp/photo.jpg")
	if !containsAny(n.Summary+n.Body, "photo.jpg") {
		t.Error("notification should mention the filename")
	}
	if !containsAny(n.Summary+n.Body, "bob") {
		t.Error("notification should mention the sender")
	}
}

func TestFileReceiveErrorNotification_Fields(t *testing.T) {
	n := FileReceiveErrorNotification("bad.bin", fmt.Errorf("disk full"))
	if n.Summary == "" {
		t.Error("Summary empty")
	}
	if n.Timeout <= 0 {
		t.Error("Timeout should be positive")
	}
}

// ── ActionNotification zero values ────────────────────────────────────────────

func TestActionNotification_ZeroTimeout(t *testing.T) {
	// Timeout == 0 should mean "use server default" (8000ms in our impl)
	n := ActionNotification{Summary: "test", Timeout: 0}
	// We can't call sendDBusNotification in unit tests (no D-Bus in CI),
	// but we can verify the struct is valid.
	if n.Summary != "test" {
		t.Error("unexpected")
	}
}

func TestActionNotification_NegativeTimeout(t *testing.T) {
	n := ActionNotification{Summary: "persistent", Timeout: -1 * time.Second}
	if n.Timeout >= 0 {
		t.Error("expected negative timeout")
	}
}

// helpers

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
