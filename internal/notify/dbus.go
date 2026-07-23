// internal/notify/dbus.go
//
// Action-button desktop notifications via the freedesktop.org Notifications
// D-Bus interface (org.freedesktop.Notifications).
//
// Why not just notify-send?
// notify-send supports --action on versions ≥ 0.8.0 (libnotify 0.8), but
// that version only landed in Ubuntu 23.04 / Debian 12. On older systems
// (20.04, 22.04) --action is silently ignored.
//
// We therefore send the notification ourselves over D-Bus using `gdbus call`,
// which is available on every systemd-era desktop (comes with glib2), and
// then watch for the ActionInvoked signal on the same connection.
//
// Protocol summary
// ────────────────
// Call:   org.freedesktop.Notifications.Notify(...)
//         returns uint32 notification_id
//
// Signal: org.freedesktop.Notifications.ActionInvoked(uint32 id, string action_key)
//         fired when the user clicks a button
//
// Signal: org.freedesktop.Notifications.NotificationClosed(uint32 id, uint32 reason)
//         fired when the notification is dismissed or times out
//
// We use `gdbus call` to send and `gdbus monitor` to watch for the signal,
// running both as subprocesses so we don't need a cgo D-Bus binding.
package notify

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	dbusService   = "org.freedesktop.Notifications"
	dbusPath      = "/org/freedesktop/Notifications"
	dbusInterface = "org.freedesktop.Notifications"
)

// ActionNotification is a notification with clickable action buttons.
type ActionNotification struct {
	Summary string
	Body    string
	Icon    string            // XDG icon name or file path
	Actions []Action          // buttons shown in the notification
	Timeout time.Duration     // 0 = server default, -1 = never expire
}

// Action is a single button in an action notification.
type Action struct {
	Key   string // internal identifier sent back via ActionInvoked
	Label string // visible button text
}

// SendWithActions sends a notification with action buttons and calls handler
// when the user clicks one. It returns immediately; handler is called from a
// new goroutine. ctx controls how long we wait for a response (the
// notification stays on screen regardless).
//
// Falls back to plain notify-send if gdbus is not available.
func (n *Notifier) SendWithActions(ctx context.Context, notif ActionNotification, handler func(actionKey string)) {
	if !n.enabled {
		return
	}

	id, err := sendDBusNotification(notif)
	if err != nil {
		// gdbus not available — fall back to a plain notification.
		log.Printf("notify: dbus send failed (%v), falling back to notify-send", err)
		n.Send(notif.Summary, notif.Body)
		return
	}

	// Watch for the action click in the background.
	go func() {
		watchCtx, cancel := context.WithTimeout(ctx, 5*time.Minute) // reasonable max wait
		defer cancel()
		key, err := waitForAction(watchCtx, id)
		if err != nil {
			// Notification dismissed/timed out — not an error we surface.
			return
		}
		handler(key)
	}()
}

// ── D-Bus send ────────────────────────────────────────────────────────────────

// sendDBusNotification calls org.freedesktop.Notifications.Notify via gdbus
// and returns the notification ID.
//
// Notify signature:
//   Notify(app_name, replaces_id, app_icon, summary, body,
//          actions, hints, expire_timeout) → uint32
//
// actions is a flat array: [key1, label1, key2, label2, ...]
func sendDBusNotification(notif ActionNotification) (uint32, error) {
	icon := notif.Icon
	if icon == "" {
		icon = "dialog-information"
	}

	timeoutMs := int(-1)
	if notif.Timeout > 0 {
		timeoutMs = int(notif.Timeout.Milliseconds())
	} else if notif.Timeout == 0 {
		timeoutMs = 8000 // 8 s default
	}

	// Build the actions array as a GVariant string array.
	// gdbus format: "['key1', 'label1', 'key2', 'label2']"
	actionParts := make([]string, 0, len(notif.Actions)*2)
	for _, a := range notif.Actions {
		actionParts = append(actionParts,
			"'"+gvariantEscape(a.Key)+"'",
			"'"+gvariantEscape(a.Label)+"'",
		)
	}
	actionsGV := "[" + strings.Join(actionParts, ", ") + "]"

	// Call via gdbus
	out, err := exec.Command("gdbus", "call",
		"--session",
		"--dest", dbusService,
		"--object-path", dbusPath,
		"--method", dbusInterface+".Notify",
		"tailscale-gui",          // app_name
		"0",                      // replaces_id (0 = new)
		icon,                     // app_icon
		notif.Summary,            // summary
		notif.Body,               // body
		actionsGV,                // actions
		"{}",                     // hints (empty dict)
		strconv.Itoa(timeoutMs),  // expire_timeout (-1 = default)
	).Output()
	if err != nil {
		return 0, fmt.Errorf("gdbus call: %w", err)
	}

	// gdbus prints: (uint32 12345,)
	id, err := parseNotifyID(string(out))
	if err != nil {
		return 0, fmt.Errorf("parse notify id from %q: %w", strings.TrimSpace(string(out)), err)
	}
	return id, nil
}

// parseNotifyID extracts the uint32 from gdbus output like "(uint32 42,)\n".
var reNotifyID = regexp.MustCompile(`\buint32\s+(\d+)`)

func parseNotifyID(out string) (uint32, error) {
	m := reNotifyID.FindStringSubmatch(out)
	if len(m) < 2 {
		// Some gdbus versions print just "(42,)" without the type tag.
		re2 := regexp.MustCompile(`\((\d+),`)
		m2 := re2.FindStringSubmatch(out)
		if len(m2) < 2 {
			return 0, fmt.Errorf("unexpected output: %q", out)
		}
		n, err := strconv.ParseUint(m2[1], 10, 32)
		return uint32(n), err
	}
	n, err := strconv.ParseUint(m[1], 10, 32)
	return uint32(n), err
}

// ── D-Bus signal watcher ──────────────────────────────────────────────────────

// waitForAction runs `gdbus monitor` and waits for ActionInvoked to fire for
// the given notification ID. Returns the action key, or an error if the
// notification is closed/dismissed before any action is taken.
func waitForAction(ctx context.Context, id uint32) (string, error) {
	cmd := exec.CommandContext(ctx, "gdbus", "monitor",
		"--session",
		"--dest", dbusService,
		"--object-path", dbusPath,
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("gdbus monitor: %w", err)
	}
	defer func() {
		cmd.Process.Kill() //nolint:errcheck
		cmd.Wait()         //nolint:errcheck
	}()

	// gdbus monitor output lines look like:
	//   /org/freedesktop/Notifications: org.freedesktop.Notifications.ActionInvoked (uint32 42, 'open-file')
	//   /org/freedesktop/Notifications: org.freedesktop.Notifications.NotificationClosed (uint32 42, uint32 2)
	reAction := regexp.MustCompile(
		fmt.Sprintf(`ActionInvoked\s*\(uint32\s+%d,\s+'([^']+)'\)`, id),
	)
	reClosed := regexp.MustCompile(
		fmt.Sprintf(`NotificationClosed\s*\(uint32\s+%d,`, id),
	)

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()

		if m := reAction.FindStringSubmatch(line); len(m) >= 2 {
			return m[1], nil
		}
		if reClosed.MatchString(line) {
			return "", fmt.Errorf("notification %d closed without action", id)
		}
	}

	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", ctx.Err()
}

// gvariantEscape escapes a string for inclusion in a GVariant literal.
func gvariantEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return s
}

// ── Convenience constructors ──────────────────────────────────────────────────

// FileReceivedNotification returns a pre-built ActionNotification for a
// received Taildrop file.
func FileReceivedNotification(filename, from, savedPath string) ActionNotification {
	body := fmt.Sprintf("From: %s\nSaved to: %s", from, savedPath)
	return ActionNotification{
		Summary: fmt.Sprintf("File received — %s", filename),
		Body:    body,
		Icon:    "document-save",
		Actions: []Action{
			{Key: "open-file",   Label: "Open file"},
			{Key: "open-folder", Label: "Show in folder"},
		},
		Timeout: 15 * time.Second,
	}
}

// FileReceiveErrorNotification returns a notification for a receive failure.
func FileReceiveErrorNotification(filename string, err error) ActionNotification {
	return ActionNotification{
		Summary: "Taildrop receive failed",
		Body:    fmt.Sprintf("File: %s\nError: %v", filename, err),
		Icon:    "dialog-error",
		Timeout: 10 * time.Second,
	}
}
