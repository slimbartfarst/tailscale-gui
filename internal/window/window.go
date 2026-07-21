// internal/window/window.go
//
// A lightweight status window served as a localhost HTTP page and opened in
// the user's default browser. This avoids a hard dependency on any particular
// GUI toolkit (GTK, Qt, Fyne) while still giving a rich, scrollable view of
// tailnet peers, exit nodes, and preferences.
//
// The page auto-refreshes every 5 s via a small fetch() loop and exposes
// action endpoints (/api/connect, /api/disconnect, /api/set-exit-node, etc.)
// so the user can perform common actions without going to the terminal.
//
// To open the window: call Open(). Subsequent calls just focus the tab.
package window

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"github.com/yourname/tailscale-gui/internal/client"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
)

//go:embed static
var staticFS embed.FS

// Manager owns the status window HTTP server.
type Manager struct {
	ts       *client.Client
	port     int
	once     sync.Once
	addr     string // resolved listen address, e.g. "127.0.0.1:54321"
	ctx      context.Context
}

// New creates a Manager. port=0 picks a random free port.
func New(ctx context.Context, ts *client.Client, port int) *Manager {
	return &Manager{ctx: ctx, ts: ts, port: port}
}

// Open starts the server (on first call) and opens the browser.
func (m *Manager) Open() {
	m.once.Do(func() {
		addr, err := m.start()
		if err != nil {
			log.Printf("window: failed to start server: %v", err)
			return
		}
		m.addr = addr
	})
	if m.addr != "" {
		openBrowser("http://" + m.addr)
	}
}

// start launches the HTTP server and returns its listen address.
func (m *Manager) start() (string, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", m.port))
	if err != nil {
		return "", err
	}

	mux := http.NewServeMux()

	// Static files (index.html, CSS, JS embedded in binary)
	mux.Handle("/static/", http.FileServer(http.FS(staticFS)))
	mux.HandleFunc("/", m.handleIndex)

	// JSON data endpoint
	mux.HandleFunc("/api/status", m.handleStatus)

	// Action endpoints
	mux.HandleFunc("/api/connect", m.handleConnect)
	mux.HandleFunc("/api/disconnect", m.handleDisconnect)
	mux.HandleFunc("/api/set-exit-node", m.handleSetExitNode)
	mux.HandleFunc("/api/clear-exit-node", m.handleClearExitNode)
	mux.HandleFunc("/api/set-pref", m.handleSetPref)
	mux.HandleFunc("/api/ping", m.handlePing)

	srv := &http.Server{
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		<-m.ctx.Done()
		_ = srv.Close()
	}()

	go func() {
		if err := srv.Serve(ln); err != nil && m.ctx.Err() == nil {
			log.Printf("window: server error: %v", err)
		}
	}()

	log.Printf("window: listening on http://%s", ln.Addr())
	return ln.Addr().String(), nil
}

// ── HTTP handlers ─────────────────────────────────────────────────────────────

func (m *Manager) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(indexHTML))
}

// statusPayload is the JSON shape sent to the browser.
type statusPayload struct {
	Self        *ipnstate.PeerStatus   `json:"self"`
	Peers       []*ipnstate.PeerStatus `json:"peers"`
	ExitNodes   []*ipnstate.PeerStatus `json:"exit_nodes"`
	ActiveExit  ipn.StableNodeID       `json:"active_exit_node"`
	TailnetIPs  []string               `json:"tailnet_ips"`
	BackendState string                `json:"backend_state"`
	Prefs       *prefPayload           `json:"prefs"`
}

type prefPayload struct {
	AcceptDNS    bool `json:"accept_dns"`
	AcceptRoutes bool `json:"accept_routes"`
	ShieldsUp    bool `json:"shields_up"`
}

func (m *Manager) handleStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(m.ctx, 4*time.Second)
	defer cancel()

	st, err := m.ts.Status(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	prefs, _ := m.ts.Prefs(ctx)

	peers := make([]*ipnstate.PeerStatus, 0, len(st.Peer))
	var exitNodes []*ipnstate.PeerStatus
	for _, p := range st.Peer {
		peers = append(peers, p)
		if p.ExitNodeOption {
			exitNodes = append(exitNodes, p)
		}
	}

	activeExit, _ := m.ts.ActiveExitNodeID(ctx)

	ips := make([]string, len(st.TailscaleIPs))
	for i, ip := range st.TailscaleIPs {
		ips[i] = ip.String()
	}

	payload := statusPayload{
		Self:         st.Self,
		Peers:        peers,
		ExitNodes:    exitNodes,
		ActiveExit:   activeExit,
		TailnetIPs:   ips,
		BackendState: st.BackendState,
	}
	if prefs != nil {
		payload.Prefs = &prefPayload{
			AcceptDNS:    prefs.CorpDNS,
			AcceptRoutes: prefs.RouteAll,
			ShieldsUp:    prefs.ShieldsUp,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func (m *Manager) handleConnect(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(m.ctx, 8*time.Second)
	defer cancel()
	err := m.ts.Connect(ctx)
	writeResult(w, err)
}

func (m *Manager) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(m.ctx, 8*time.Second)
	defer cancel()
	err := m.ts.Disconnect(ctx)
	writeResult(w, err)
}

func (m *Manager) handleSetExitNode(w http.ResponseWriter, r *http.Request) {
	id := ipn.StableNodeID(r.URL.Query().Get("id"))
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(m.ctx, 8*time.Second)
	defer cancel()
	writeResult(w, m.ts.SetExitNode(ctx, id))
}

func (m *Manager) handleClearExitNode(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(m.ctx, 8*time.Second)
	defer cancel()
	writeResult(w, m.ts.ClearExitNode(ctx))
}

func (m *Manager) handleSetPref(w http.ResponseWriter, r *http.Request) {
	pref := r.URL.Query().Get("pref")
	val := r.URL.Query().Get("value") == "true"

	ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	defer cancel()

	var err error
	switch pref {
	case "dns":
		err = m.ts.SetAcceptDNS(ctx, val)
	case "routes":
		err = m.ts.SetAcceptRoutes(ctx, val)
	case "shields":
		err = m.ts.SetShieldsUp(ctx, val)
	default:
		http.Error(w, "unknown pref: "+pref, http.StatusBadRequest)
		return
	}
	writeResult(w, err)
}

// handlePing pings a peer by its Tailscale IP and returns the result.
func (m *Manager) handlePing(w http.ResponseWriter, r *http.Request) {
	ip := r.URL.Query().Get("ip")
	if ip == "" {
		http.Error(w, "missing ip", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
	defer cancel()

	result, err := m.ts.LocalClient().Ping(ctx, ip, "ts")
	if err != nil {
		writeResult(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func writeResult(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
}

func openBrowser(url string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "linux":
		cmd, args = "xdg-open", []string{url}
	case "darwin":
		cmd, args = "open", []string{url}
	default:
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	}
	if err := exec.Command(cmd, args...).Start(); err != nil {
		log.Printf("openBrowser: %v", err)
	}
}
