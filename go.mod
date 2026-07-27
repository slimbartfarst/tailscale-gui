module github.com/yourname/tailscale-gui

go 1.23

require (
	fyne.io/systray v1.11.0
	tailscale.com v1.78.0
	proxy.golang.org/x/net v0.30.0
)

// Run `go mod tidy` after cloning to resolve all transitive deps.
// Requires Go 1.23+ (tailscale.com v1.78.0 minimum).
// Both fyne.io/systray and tailscale.com are hosted on proxy.golang.org.
