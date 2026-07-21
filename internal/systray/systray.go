// internal/systray/systray.go
//
// System tray application. Owns the OS main thread via systray.Run().
//
// Menu layout:
//   Status: Connected ✓
//   ─────────────────
//   This device: 100.x.x.x
//   ─────────────────
//   Connect / Disconnect
//   ─────────────────
//   Peers ▶  [submenu: online peers with IP copy]
//   Exit nodes ▶  [submenu: None + list of exit nodes]
//   ─────────────────
//   ✓ Use Tailscale DNS
//   ✓ Accept subnet routes
//     Shields up
//   ─────────────────
//   Send file…        [opens file picker → Taildrop]
//   Taildrop folder   [opens receive dir in file manager]
//   ─────────────────
//   Admin console…
//   ─────────────────
//   Quit
package systray

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/tailscale/systray"
	"github.com/yourname/tailscale-gui/internal/client"
	"github.com/yourname/tailscale-gui/internal/config"
	"github.com/yourname/tailscale-gui/internal/notify"
	"github.com/yourname/tailscale-gui/internal/taildrop"
	"github.com/yourname/tailscale-gui/internal/window"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
)

// App is the system tray application.
type App struct {
	ctx      context.Context
	cancel   context.CancelFunc
	ts       *client.Client
	cfg      *config.Config
	notifier *notify.Notifier
	tdrop    *taildrop.Manager
	win      *window.Manager

	// ── static menu items ────────────────────────────────────────────────────
	mStatus       *systray.MenuItem
	mSelf         *systray.MenuItem
	mConnect      *systray.MenuItem
	mDisconnect   *systray.MenuItem
	mPeers        *systray.MenuItem // parent of peer submenu
	mExitNodes    *systray.MenuItem // parent of exit node submenu
	mAcceptDNS    *systray.MenuItem
	mAcceptRoutes *systray.MenuItem
	mShieldsUp    *systray.MenuItem
	mSendFile     *systray.MenuItem
	mTaildropDir  *systray.MenuItem
	mStatusWindow *systray.MenuItem
	mAdminConsole *systray.MenuItem
	mQuit         *systray.MenuItem

	// ── dynamic submenu state ─────────────────────────────────────────────────
	mu            sync.Mutex
	peerItems     []*peerItem     // live peer submenu items
	exitNodeItems []*exitNodeItem // live exit node submenu items
	currentState  ipn.State
	activeExitID  ipn.StableNodeID
}

type peerItem struct {
	peer *ipnstate.PeerStatus
	item *systray.MenuItem
}

type exitNodeItem struct {
	peer *ipnstate.PeerStatus
	item *systray.MenuItem
}

// New creates the App. Call Run() from main().
func New(
	ctx context.Context,
	ts *client.Client,
	cfg *config.Config,
	notifier *notify.Notifier,
	tdrop *taildrop.Manager,
	win *window.Manager,
) *App {
	appCtx, cancel := context.WithCancel(ctx)
	return &App{
		ctx:      appCtx,
		cancel:   cancel,
		ts:       ts,
		cfg:      cfg,
		notifier: notifier,
		tdrop:    tdrop,
		win:      win,
	}
}

// Run blocks until quit. Must be called from the main goroutine.
func (a *App) Run() {
	systray.Run(a.onReady, a.onExit)
}

// ── Lifecycle ─────────────────────────────────────────────────────────────────

func (a *App) onReady() {
	systray.SetIcon(iconDisconnected)
	systray.SetTitle("Tailscale")
	systray.SetTooltip("Tailscale — starting…")

	// Status row (disabled, informational)
	a.mStatus = systray.AddMenuItem("Tailscale: starting…", "")
	a.mStatus.Disable()
	systray.AddSeparator()

	// This device row
	a.mSelf = systray.AddMenuItem("This device: —", "Your Tailscale addresses")
	a.mSelf.Disable()
	systray.AddSeparator()

	// Connect / Disconnect (one shown at a time)
	a.mConnect = systray.AddMenuItem("Connect", "Bring Tailscale up")
	a.mDisconnect = systray.AddMenuItem("Disconnect", "Bring Tailscale down")
	a.mDisconnect.Hide()
	systray.AddSeparator()

	// Peers submenu (populated dynamically)
	a.mPeers = systray.AddMenuItem("Peers", "Devices on your tailnet")
	a.mPeers.Disable()

	// Exit nodes submenu (populated dynamically)
	a.mExitNodes = systray.AddMenuItem("Exit nodes", "Route all traffic through a peer")
	a.mExitNodes.Disable()
	systray.AddSeparator()

	// Toggles
	a.mAcceptDNS = systray.AddMenuItemCheckbox("Use Tailscale DNS", "Toggle MagicDNS", false)
	a.mAcceptRoutes = systray.AddMenuItemCheckbox("Accept subnet routes", "Accept routes from peers", false)
	a.mShieldsUp = systray.AddMenuItemCheckbox("Shields up", "Block all incoming connections", false)
	systray.AddSeparator()

	// Taildrop
	a.mSendFile = systray.AddMenuItem("Send file…", "Send a file via Taildrop")
	a.mTaildropDir = systray.AddMenuItem("Open Taildrop folder", "Open the folder where received files are saved")
	systray.AddSeparator()

	// Status window + admin
	a.mStatusWindow = systray.AddMenuItem("Open status window…", "Open the full status dashboard in a browser")
	a.mAdminConsole = systray.AddMenuItem("Admin console…", "Open login.tailscale.com/admin in a browser")
	systray.AddSeparator()
	a.mQuit = systray.AddMenuItem("Quit", "Quit the tray app")

	// Background workers
	go a.initialLoad()
	go a.watchState()
	go a.pollPeers()
	go a.runTaildrop()

	// Menu event loop
	go a.handleMenuEvents()
}

func (a *App) onExit() {
	a.cancel()
}

// ── Initial load ──────────────────────────────────────────────────────────────

func (a *App) initialLoad() {
	ctx, cancel := context.WithTimeout(a.ctx, 6*time.Second)
	defer cancel()

	st, err := a.ts.Status(ctx)
	if err != nil {
		log.Printf("systray: initial status: %v", err)
		return
	}
	a.applyFullStatus(st)

	prefs, err := a.ts.Prefs(ctx)
	if err != nil {
		log.Printf("systray: initial prefs: %v", err)
		return
	}
	a.applyPrefs(prefs)
}

// ── State watching ────────────────────────────────────────────────────────────

func (a *App) watchState() {
	prev := ipn.NoState
	_ = a.ts.WatchState(a.ctx, func(sc client.StateChange) {
		if sc.HasError {
			log.Printf("systray: daemon error: %s", sc.Error)
			a.notifier.Send("Tailscale error", sc.Error)
			return
		}
		if !sc.HasState {
			return
		}
		a.mu.Lock()
		a.currentState = sc.State
		a.mu.Unlock()

		a.applyState(sc.State)

		switch {
		case prev != ipn.Running && sc.State == ipn.Running:
			a.notifier.Send("Tailscale", "Connected")
			// Refresh peers immediately on connect.
			go a.refreshPeers()
		case prev == ipn.Running && sc.State != ipn.Running:
			a.notifier.Send("Tailscale", "Disconnected")
		}
		prev = sc.State
	})
}

// pollPeers refreshes the peer / exit-node submenus periodically.
func (a *App) pollPeers() {
	// Initial refresh after a short delay to let the daemon settle.
	select {
	case <-a.ctx.Done():
		return
	case <-time.After(2 * time.Second):
	}
	a.refreshPeers()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			a.refreshPeers()
		}
	}
}

func (a *App) refreshPeers() {
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()

	st, err := a.ts.Status(ctx)
	if err != nil {
		log.Printf("systray: refresh peers: %v", err)
		return
	}
	a.applyFullStatus(st)
}

// ── Status application ────────────────────────────────────────────────────────

func (a *App) applyFullStatus(st *ipnstate.Status) {
	// Self row
	if st.Self != nil && len(st.TailscaleIPs) > 0 {
		ipStr := ""
		for i, ip := range st.TailscaleIPs {
			if i > 0 {
				ipStr += "  "
			}
			ipStr += ip.String()
		}
		a.mSelf.SetTitle(fmt.Sprintf("This device: %s", ipStr))
		a.mSelf.SetTooltip(fmt.Sprintf("Hostname: %s", st.Self.HostName))
	}

	// Connection state
	if st.BackendState != "" {
		a.applyStateByName(st.BackendState)
	}

	// Peers
	peers := make([]*ipnstate.PeerStatus, 0, len(st.Peer))
	for _, p := range st.Peer {
		peers = append(peers, p)
	}
	a.rebuildPeerSubmenu(peers)

	// Exit nodes — capture active selection
	activeID, _ := a.ts.ActiveExitNodeID(a.ctx)
	a.mu.Lock()
	a.activeExitID = activeID
	a.mu.Unlock()
	a.rebuildExitNodeSubmenu(peers, activeID)
}

func (a *App) applyStateByName(name string) {
	states := map[string]ipn.State{
		"Running":          ipn.Running,
		"Stopped":          ipn.Stopped,
		"NeedsLogin":       ipn.NeedsLogin,
		"NeedsMachineAuth": ipn.NeedsMachineAuth,
		"Starting":         ipn.Starting,
	}
	if s, ok := states[name]; ok {
		a.applyState(s)
	}
}

func (a *App) applyState(state ipn.State) {
	switch state {
	case ipn.Running:
		systray.SetIcon(iconConnected)
		systray.SetTooltip("Tailscale — connected")
		a.mStatus.SetTitle("Status: Connected ✓")
		a.mConnect.Hide()
		a.mDisconnect.Show()
		a.mExitNodes.Enable()
		a.mSendFile.Enable()

	case ipn.Stopped:
		systray.SetIcon(iconDisconnected)
		systray.SetTooltip("Tailscale — disconnected")
		a.mStatus.SetTitle("Status: Disconnected")
		a.mConnect.Show()
		a.mDisconnect.Hide()
		a.mExitNodes.Disable()
		a.mSendFile.Disable()

	case ipn.NeedsLogin:
		systray.SetIcon(iconWarning)
		systray.SetTooltip("Tailscale — login required")
		a.mStatus.SetTitle("Status: Login required — visit admin console")
		a.mConnect.Show()
		a.mDisconnect.Hide()

	case ipn.NeedsMachineAuth:
		systray.SetIcon(iconWarning)
		systray.SetTooltip("Tailscale — awaiting approval")
		a.mStatus.SetTitle("Status: Awaiting machine authorisation")

	case ipn.Starting:
		systray.SetIcon(iconConnecting)
		systray.SetTooltip("Tailscale — connecting…")
		a.mStatus.SetTitle("Status: Connecting…")

	default:
		systray.SetIcon(iconDisconnected)
		systray.SetTooltip("Tailscale")
		a.mStatus.SetTitle("Status: Unknown")
	}
}

func (a *App) applyPrefs(prefs *ipn.Prefs) {
	if prefs.CorpDNS {
		a.mAcceptDNS.Check()
	} else {
		a.mAcceptDNS.Uncheck()
	}
	if prefs.RouteAll {
		a.mAcceptRoutes.Check()
	} else {
		a.mAcceptRoutes.Uncheck()
	}
	if prefs.ShieldsUp {
		a.mShieldsUp.Check()
	} else {
		a.mShieldsUp.Uncheck()
	}
}

// ── Peer submenu ──────────────────────────────────────────────────────────────

// rebuildPeerSubmenu replaces the peer submenu items with the current peer list.
// Each peer shows its hostname and first Tailscale IP; clicking it copies the IP.
func (a *App) rebuildPeerSubmenu(peers []*ipnstate.PeerStatus) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Hide old items. We can't remove systray items, so we hide them and
	// accumulate. On most desktops the list is short enough that this is fine;
	// for very large tailnets you'd want to page or filter.
	for _, pi := range a.peerItems {
		pi.item.Hide()
	}
	a.peerItems = a.peerItems[:0]

	online := 0
	for _, p := range peers {
		if !p.Online {
			continue
		}
		online++

		ip := ""
		if len(p.TailscaleIPs) > 0 {
			ip = p.TailscaleIPs[0].String()
		}
		label := fmt.Sprintf("  %s  %s", p.HostName, ip)
		tooltip := fmt.Sprintf("OS: %s | Active: %v", p.OS, p.Active)

		item := a.mPeers.AddSubMenuItem(label, tooltip)

		pi := &peerItem{peer: p, item: item}
		a.peerItems = append(a.peerItems, pi)

		// Click → copy IP to clipboard
		go func(copyIP string, mi *systray.MenuItem) {
			for {
				select {
				case <-a.ctx.Done():
					return
				case _, ok := <-mi.ClickedCh:
					if !ok {
						return
					}
					if err := copyToClipboard(copyIP); err != nil {
						log.Printf("clipboard: %v", err)
					} else {
						a.notifier.Send("Tailscale", fmt.Sprintf("Copied %s", copyIP))
					}
				}
			}
		}(ip, item)
	}

	if online == 0 {
		a.mPeers.SetTitle("Peers (none online)")
		a.mPeers.Disable()
	} else {
		a.mPeers.SetTitle(fmt.Sprintf("Peers (%d online)", online))
		a.mPeers.Enable()
	}
}

// ── Exit node submenu ─────────────────────────────────────────────────────────

// rebuildExitNodeSubmenu replaces the exit node submenu with the current list.
func (a *App) rebuildExitNodeSubmenu(peers []*ipnstate.PeerStatus, activeID ipn.StableNodeID) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Hide old items.
	for _, ei := range a.exitNodeItems {
		ei.item.Hide()
	}
	a.exitNodeItems = a.exitNodeItems[:0]

	var exitPeers []*ipnstate.PeerStatus
	for _, p := range peers {
		if p.ExitNodeOption {
			exitPeers = append(exitPeers, p)
		}
	}

	if len(exitPeers) == 0 {
		a.mExitNodes.SetTitle("Exit nodes (none available)")
		return
	}

	// "None" option — clears any active exit node
	noneLabel := "  None"
	if activeID == "" {
		noneLabel = "✓ None"
	}
	noneItem := a.mExitNodes.AddSubMenuItem(noneLabel, "Do not use an exit node")
	go func() {
		for {
			select {
			case <-a.ctx.Done():
				return
			case _, ok := <-noneItem.ClickedCh:
				if !ok {
					return
				}
				go a.doClearExitNode()
			}
		}
	}()

	// One item per exit-capable peer
	for _, p := range exitPeers {
		isActive := p.ID == activeID
		checkmark := "  "
		if isActive {
			checkmark = "✓ "
		}
		ip := ""
		if len(p.TailscaleIPs) > 0 {
			ip = p.TailscaleIPs[0].String()
		}
		label := fmt.Sprintf("%s%s  %s", checkmark, p.HostName, ip)
		tooltip := fmt.Sprintf("Exit via %s (%s)", p.HostName, p.OS)
		item := a.mExitNodes.AddSubMenuItem(label, tooltip)

		ei := &exitNodeItem{peer: p, item: item}
		a.exitNodeItems = append(a.exitNodeItems, ei)

		go func(nodeID ipn.StableNodeID, hostname string, mi *systray.MenuItem) {
			for {
				select {
				case <-a.ctx.Done():
					return
				case _, ok := <-mi.ClickedCh:
					if !ok {
						return
					}
					go a.doSetExitNode(nodeID, hostname)
				}
			}
		}(p.ID, p.HostName, item)
	}

	activeLabel := "Exit nodes"
	if activeID != "" {
		// Find the active node's hostname for display
		for _, p := range exitPeers {
			if p.ID == activeID {
				activeLabel = fmt.Sprintf("Exit: %s ✓", p.HostName)
				break
			}
		}
	}
	a.mExitNodes.SetTitle(activeLabel)
}

// ── Taildrop ──────────────────────────────────────────────────────────────────

func (a *App) runTaildrop() {
	a.tdrop.Watch(a.ctx, func(ev taildrop.FileEvent) {
		if ev.Err != nil {
			log.Printf("taildrop receive error: %v", ev.Err)
			a.notifier.Send("Taildrop error", ev.Err.Error())
			return
		}
		from := ev.From
		if from == "" {
			from = "a peer"
		}
		a.notifier.Send(
			"File received via Taildrop",
			fmt.Sprintf("%s from %s\nSaved to %s", ev.Name, from, ev.Path),
		)
		log.Printf("taildrop: received %s → %s", ev.Name, ev.Path)
	})
}

// ── Menu event handling ───────────────────────────────────────────────────────

func (a *App) handleMenuEvents() {
	for {
		select {
		case <-a.ctx.Done():
			return

		case <-a.mConnect.ClickedCh:
			go a.doConnect()

		case <-a.mDisconnect.ClickedCh:
			go a.doDisconnect()

		case <-a.mAcceptDNS.ClickedCh:
			go a.doToggle("dns", func(p *ipn.Prefs) bool { return !p.CorpDNS },
				func(ctx context.Context, v bool) error { return a.ts.SetAcceptDNS(ctx, v) })

		case <-a.mAcceptRoutes.ClickedCh:
			go a.doToggle("routes", func(p *ipn.Prefs) bool { return !p.RouteAll },
				func(ctx context.Context, v bool) error { return a.ts.SetAcceptRoutes(ctx, v) })

		case <-a.mShieldsUp.ClickedCh:
			go a.doToggle("shields", func(p *ipn.Prefs) bool { return !p.ShieldsUp },
				func(ctx context.Context, v bool) error { return a.ts.SetShieldsUp(ctx, v) })

		case <-a.mSendFile.ClickedCh:
			go a.doSendFile()

		case <-a.mTaildropDir.ClickedCh:
			openFileManager(a.tdrop.ReceiveDir())

		case <-a.mStatusWindow.ClickedCh:
			go a.win.Open()

		case <-a.mAdminConsole.ClickedCh:
			openBrowser(a.cfg.AdminConsoleURL)

		case <-a.mQuit.ClickedCh:
			systray.Quit()
			a.cancel()
			return
		}
	}
}

// ── Actions ───────────────────────────────────────────────────────────────────

func (a *App) doConnect() {
	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
	defer cancel()
	if err := a.ts.Connect(ctx); err != nil {
		log.Printf("connect: %v", err)
		a.notifier.Send("Tailscale", fmt.Sprintf("Connect failed: %v", err))
	}
}

func (a *App) doDisconnect() {
	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
	defer cancel()
	if err := a.ts.Disconnect(ctx); err != nil {
		log.Printf("disconnect: %v", err)
		a.notifier.Send("Tailscale", fmt.Sprintf("Disconnect failed: %v", err))
	}
}

func (a *App) doSetExitNode(id ipn.StableNodeID, hostname string) {
	ctx, cancel := context.WithTimeout(a.ctx, 8*time.Second)
	defer cancel()
	if err := a.ts.SetExitNode(ctx, id); err != nil {
		log.Printf("set exit node: %v", err)
		a.notifier.Send("Tailscale", fmt.Sprintf("Could not set exit node: %v", err))
		return
	}
	a.notifier.Send("Tailscale", fmt.Sprintf("Exit node: %s", hostname))
	// Refresh the submenu to show the new checkmark.
	go a.refreshPeers()
}

func (a *App) doClearExitNode() {
	ctx, cancel := context.WithTimeout(a.ctx, 8*time.Second)
	defer cancel()
	if err := a.ts.ClearExitNode(ctx); err != nil {
		log.Printf("clear exit node: %v", err)
		return
	}
	a.notifier.Send("Tailscale", "Exit node cleared")
	go a.refreshPeers()
}

// doToggle reads current prefs, flips the given bool, writes back, then
// refreshes the checkbox display.
func (a *App) doToggle(
	name string,
	newVal func(*ipn.Prefs) bool,
	setter func(context.Context, bool) error,
) {
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()

	prefs, err := a.ts.Prefs(ctx)
	if err != nil {
		log.Printf("toggle %s: read prefs: %v", name, err)
		return
	}
	v := newVal(prefs)
	if err := setter(ctx, v); err != nil {
		log.Printf("toggle %s: set: %v", name, err)
		return
	}
	// Re-read and apply to keep checkboxes in sync.
	updated, err := a.ts.Prefs(ctx)
	if err == nil {
		a.applyPrefs(updated)
	}
}

func (a *App) doSendFile() {
	// Pick a file via zenity (a lightweight GTK dialog tool).
	// Falls back to a logged message if zenity is not installed.
	path, err := pickFileWithZenity()
	if err != nil {
		log.Printf("file picker: %v", err)
		a.notifier.Send("Taildrop", "Could not open file picker (is zenity installed?)")
		return
	}
	if path == "" {
		return // user cancelled
	}

	// Ask which peer to send to.
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	peers, err := a.ts.Peers(ctx)
	if err != nil || len(peers) == 0 {
		a.notifier.Send("Taildrop", "No peers available")
		return
	}

	// For the scaffold: send to the first online peer.
	// In a real app you'd show a peer-picker dialog here.
	var target *ipnstate.PeerStatus
	for _, p := range peers {
		if p.Online {
			target = p
			break
		}
	}
	if target == nil {
		a.notifier.Send("Taildrop", "No peers are online")
		return
	}

	log.Printf("taildrop: sending %s → %s", path, target.HostName)
	a.notifier.Send("Taildrop", fmt.Sprintf("Sending to %s…", target.HostName))

	ch, err := a.tdrop.SendFile(a.ctx, target.ID, path)
	if err != nil {
		a.notifier.Send("Taildrop", fmt.Sprintf("Send failed: %v", err))
		return
	}

	// Drain progress channel in background.
	go func() {
		for prog := range ch {
			if prog.Done {
				a.notifier.Send("Taildrop", fmt.Sprintf("Sent %s to %s", prog.Name, target.HostName))
			} else if prog.Err != nil {
				a.notifier.Send("Taildrop error", prog.Err.Error())
			}
		}
	}()
}
