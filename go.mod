module github.com/yourname/tailscale-gui

go 1.23

require (
	github.com/tailscale/tailscale v1.78.0
	github.com/tailscale/systray v1.1.0
	golang.org/x/net v0.30.0
)

// Run `go mod tidy` after cloning to resolve all transitive deps.
// tailscale brings in wireguard-go, netstack, and many others automatically.
