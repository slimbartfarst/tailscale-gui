// internal/notify/notify.go
//
// Desktop notifications via notify-send (libnotify).
// All notifications are best-effort; errors are logged, never fatal.
package notify

import (
	"log"
	"os/exec"
)

const (
	appName    = "Tailscale"
	xdgIcon    = "network-vpn" // falls back gracefully if not installed
	expireMs   = "5000"        // auto-dismiss after 5 s
)

// Notifier sends desktop notifications.
type Notifier struct {
	enabled bool
}

// New creates a Notifier. Pass enabled=false to suppress all notifications.
func New(enabled bool) *Notifier {
	return &Notifier{enabled: enabled}
}

// Send sends a normal-urgency desktop notification.
func (n *Notifier) Send(summary, body string) {
	if !n.enabled {
		return
	}
	n.run("normal", summary, body)
}

// SendUrgent sends a critical-urgency notification that stays until dismissed.
func (n *Notifier) SendUrgent(summary, body string) {
	if !n.enabled {
		return
	}
	n.run("critical", summary, body)
}

func (n *Notifier) run(urgency, summary, body string) {
	args := []string{
		"--app-name", appName,
		"--icon", xdgIcon,
		"--urgency", urgency,
		"--expire-time", expireMs,
		summary,
	}
	if body != "" {
		args = append(args, body)
	}
	if err := exec.Command("notify-send", args...).Run(); err != nil {
		// notify-send not installed is not fatal — just log once.
		log.Printf("notify: %v (install libnotify-bin to enable notifications)", err)
	}
}
