// internal/systray/systray.go
//
// System tray application. Owns the OS main thread via systray.Run().
//
// Menu layout:
//   Status: Connected ✓
//   ─────────────────────────────────
//   This device: 100.x.x.x
//   ─────────────────────────────────
//   Connect / Disconnect
//   Log in…                          (shown only when NeedsLogin)
//   ─────────────────────────────────
//   Peers (N online) ▶
//     hostname  100.x.x.x  [OS]
//       Send file…
//       SSH…               (only for SSH-capable peers)
//   Exit nodes ▶
//     ✓ None
//       peer-name  100.x.x.x
//   ─────────────────────────────────
//   Advertise subnets (N) ▶
//     ✓ 192.168.1.0/24  [approved ✓]
//         Remove
//     Add route…
//   ─────────────────────────────────
//   ✓ Use Tailscale DNS
//   ✓ Accept subnet routes
//     Shields up
//   ─────────────────────────────────
//   Send file…
//   Open Taildrop folder
//   ─────────────────────────────────
//   Open status window…
//   Admin console…
//   ─────────────────────────────────
//   Account (alice@example.com) ▶
//     ✓ alice@example.com            (active — disabled)
//       bob@example.com              (click to switch)
//     Add account…
//     Log out
//   ─────────────────────────────────
//   Quit
package systray

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/netip"
	"strings"
	"sync"
	"time"

	"fyne.io/systray"
	"github.com/slimbartfarst/tailscale-gui/internal/account"
	"github.com/slimbartfarst/tailscale-gui/internal/client"
	"github.com/slimbartfarst/tailscale-gui/internal/config"
	"github.com/slimbartfarst/tailscale-gui/internal/notify"
	"github.com/slimbartfarst/tailscale-gui/internal/picker"
	"github.com/slimbartfarst/tailscale-gui/internal/routes"
	sshlaunch "github.com/slimbartfarst/tailscale-gui/internal/ssh"
	"github.com/slimbartfarst/tailscale-gui/internal/taildrop"
	"github.com/slimbartfarst/tailscale-gui/internal/window"
	"tailscale.com/ipn"
	"tailscale.com/tailcfg"
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
	rm       *routes.Manager
	am       *account.Manager // multi-account

	// ── static menu items ────────────────────────────────────────────────────
	mStatus       *systray.MenuItem
	mSelf         *systray.MenuItem
	mConnect      *systray.MenuItem
	mDisconnect   *systray.MenuItem
	mPeers        *systray.MenuItem
	mExitNodes    *systray.MenuItem
	mAdvertise    *systray.MenuItem
	mAddRoute     *systray.MenuItem
	mAcceptDNS    *systray.MenuItem
	mAcceptRoutes *systray.MenuItem
	mShieldsUp    *systray.MenuItem
	mSendFile     *systray.MenuItem
	mTaildropDir  *systray.MenuItem
	mAccount      *systray.MenuItem // "Account ▶" parent
	mAddAccount   *systray.MenuItem // "Add account…"
	mLogout       *systray.MenuItem // "Log out"
	mLoginNow     *systray.MenuItem // "Log in…" (shown when NeedsLogin)
	mStatusWindow *systray.MenuItem
	mAdminConsole *systray.MenuItem
	mQuit         *systray.MenuItem

	// ── dynamic submenu state ─────────────────────────────────────────────────
	mu             sync.Mutex
	peerItems      []*peerItem
	exitNodeItems  []*exitNodeItem
	routeItems     []*routeItem
	accountItems   []*accountItem  // live profile switch sub-items
	currentState   ipn.State
	activeExitID   tailcfg.StableNodeID
	currentAuthURL string          // latest AuthURL from daemon (empty if none)
}

type peerItem struct {
	peer     *ipnstate.PeerStatus
	item     *systray.MenuItem
	sendItem *systray.MenuItem // "Send file…" sub-item
	sshItem  *systray.MenuItem // "SSH…"       sub-item (nil if peer doesn't support SSH)
}

type exitNodeItem struct {
	peer *ipnstate.PeerStatus
	item *systray.MenuItem
}

type routeItem struct {
	route    routes.Route
	item     *systray.MenuItem // the checkbox item showing the prefix
	removeIt *systray.MenuItem // "Remove" sub-item
}

type accountItem struct {
	profile account.Profile
	item    *systray.MenuItem // "✓ alice@example.com" or "  bob@example.com"
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
		rm:       routes.New(ts),
		am:       account.New(ts.LocalClient()),
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

	// Advertise subnets submenu (populated dynamically)
	a.mAdvertise = systray.AddMenuItem("Advertise subnets", "Share local subnets with the tailnet")
	a.mAddRoute = a.mAdvertise.AddSubMenuItem("  Add route…", "Advertise a new subnet prefix")
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

	// Status window + admin + account
	a.mStatusWindow = systray.AddMenuItem("Open status window…", "Open the full status dashboard in a browser")
	a.mAdminConsole = systray.AddMenuItem("Admin console…", "Open login.tailscale.com/admin in a browser")
	systray.AddSeparator()

	// Account submenu
	a.mAccount    = systray.AddMenuItem("Account", "Manage Tailscale accounts")
	a.mAddAccount = a.mAccount.AddSubMenuItem("  Add account…", "Log in with a different Tailscale account")
	a.mLogout     = a.mAccount.AddSubMenuItem("  Log out", "Log out of the current account")
	a.mLoginNow   = systray.AddMenuItem("Log in…", "Open browser to log in")
	a.mLoginNow.Hide() // shown only when NeedsLogin
	systray.AddSeparator()

	a.mQuit = systray.AddMenuItem("Quit", "Quit the tray app")

	// Background workers
	go a.initialLoad()
	go a.watchState()
	go a.pollPeers()
	go a.runTaildrop()
	go a.watchAuthURL()

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

	// Hide Account menu on older daemons that don't support profiles.
	if !account.IsMultiAccountSupported(ctx, a.ts.LocalClient()) {
		a.mAccount.Hide()
		log.Printf("account: multi-account not supported by this daemon (tailscaled < v1.56)")
	}

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
	a.refreshRoutes(ctx)
	a.refreshAccounts(ctx)
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
	a.refreshRoutes(ctx)
	a.refreshAccounts(ctx)
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
		a.mLoginNow.Hide()

	case ipn.Stopped:
		systray.SetIcon(iconDisconnected)
		systray.SetTooltip("Tailscale — disconnected")
		a.mStatus.SetTitle("Status: Disconnected")
		a.mConnect.Show()
		a.mDisconnect.Hide()
		a.mExitNodes.Disable()
		a.mSendFile.Disable()
		a.mLoginNow.Hide()

	case ipn.NeedsLogin:
		systray.SetIcon(iconWarning)
		systray.SetTooltip("Tailscale — login required")
		a.mStatus.SetTitle("Status: Login required")
		a.mConnect.Show()
		a.mDisconnect.Hide()
		a.mLoginNow.Show()
		// If we have a pending AuthURL, open it immediately.
		a.mu.Lock()
		authURL := a.currentAuthURL
		a.mu.Unlock()
		if authURL != "" {
			go openBrowser(authURL)
		} else {
			// Kick off interactive login to get an AuthURL from the daemon.
			go func() {
				ctx, cancel := context.WithTimeout(a.ctx, 8*time.Second)
				defer cancel()
				if err := a.ts.StartLoginInteractive(ctx); err != nil {
					log.Printf("start login interactive: %v", err)
				}
			}()
		}

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
//
// Each peer entry has two sub-items:
//   - The peer row itself (hostname + IP) — click to copy IP
//   - "Send file…" — opens the file picker and sends directly to that peer
func (a *App) rebuildPeerSubmenu(peers []*ipnstate.PeerStatus) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Hide old items (systray doesn't support removal).
	for _, pi := range a.peerItems {
		pi.item.Hide()
		if pi.sendItem != nil {
			pi.sendItem.Hide()
		}
		if pi.sshItem != nil {
			pi.sshItem.Hide()
		}
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

		// Peer row — parent item (click → copy IP)
		label := fmt.Sprintf("  %s  %s  [%s]", p.HostName, ip, osLabel(p.OS))
		tooltip := fmt.Sprintf("Click to copy %s", ip)
		item := a.mPeers.AddSubMenuItem(label, tooltip)

		// "  Send file…" — nested under the peer row
		sendItem := item.AddSubMenuItem("  Send file…", fmt.Sprintf("Send a file to %s via Taildrop", p.HostName))

		// "  SSH…" — shown only for SSH-capable peers
		var sshItem *systray.MenuItem
		if sshlaunch.PeerSupportsSSH(p) {
			sshCmd := sshlaunch.SSHCommandString(p, a.sshConfig())
			sshItem = item.AddSubMenuItem("  SSH…", fmt.Sprintf("Open terminal: %s", sshCmd))
		}

		pi := &peerItem{peer: p, item: item, sendItem: sendItem, sshItem: sshItem}
		a.peerItems = append(a.peerItems, pi)

		// Copy-IP click handler
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

		// Send-file click handler — bypasses the peer picker (target is known)
		go func(target *ipnstate.PeerStatus, mi *systray.MenuItem) {
			for {
				select {
				case <-a.ctx.Done():
					return
				case _, ok := <-mi.ClickedCh:
					if !ok {
						return
					}
					go a.doSendFileToPeer(target)
				}
			}
		}(p, sendItem)

		// SSH click handler
		if sshItem != nil {
			go func(target *ipnstate.PeerStatus, mi *systray.MenuItem) {
				for {
					select {
					case <-a.ctx.Done():
						return
					case _, ok := <-mi.ClickedCh:
						if !ok {
							return
						}
						go a.doSSH(target)
					}
				}
			}(p, sshItem)
		}
	}

	if online == 0 {
		a.mPeers.SetTitle("Peers (none online)")
		a.mPeers.Disable()
	} else {
		a.mPeers.SetTitle(fmt.Sprintf("Peers (%d online)", online))
		a.mPeers.Enable()
	}
}

// osLabel returns a short human-readable OS label.
func osLabel(os string) string {
	switch os {
	case "linux":
		return "Linux"
	case "windows":
		return "Windows"
	case "darwin":
		return "macOS"
	case "android":
		return "Android"
	case "ios":
		return "iOS"
	default:
		if os == "" {
			return "?"
		}
		return os
	}
}

// ── Exit node submenu ─────────────────────────────────────────────────────────

// rebuildExitNodeSubmenu replaces the exit node submenu with the current list.
func (a *App) rebuildExitNodeSubmenu(peers []*ipnstate.PeerStatus, activeID tailcfg.StableNodeID) {
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

		go func(nodeID tailcfg.StableNodeID, hostname string, mi *systray.MenuItem) {
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

// ── Subnet route advertising submenu ──────────────────────────────────────────

// refreshRoutes reads current advertised routes from the daemon and rebuilds
// the submenu. Call after any route change and on the regular poll cycle.
func (a *App) refreshRoutes(ctx context.Context) {
	current, err := a.rm.Current(ctx)
	if err != nil {
		log.Printf("systray: refresh routes: %v", err)
		return
	}
	a.rebuildRouteSubmenu(current)
}

// rebuildRouteSubmenu replaces the advertised-routes submenu contents.
//
// Layout:
//   Advertise subnets (N advertising)
//     ✓ 192.168.1.0/24  RFC-1918 subnet  [approved ✓ / pending…]
//         Remove
//     ─────────────────
//     Add route…
//     Suggest from local interfaces…
func (a *App) rebuildRouteSubmenu(current []routes.Route) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Hide old route items.
	for _, ri := range a.routeItems {
		ri.item.Hide()
		if ri.removeIt != nil {
			ri.removeIt.Hide()
		}
	}
	a.routeItems = a.routeItems[:0]

	for _, r := range current {
		approval := "pending approval…"
		if r.Approved {
			approval = "approved ✓"
		}
		label := fmt.Sprintf("✓ %s  [%s]", r.Prefix.String(), approval)
		tooltip := fmt.Sprintf("Label: %s | Status: %s", r.Label, approval)

		item := a.mAdvertise.AddSubMenuItem(label, tooltip)
		removeIt := item.AddSubMenuItem("    Remove", fmt.Sprintf("Stop advertising %s", r.Prefix))

		ri := &routeItem{route: r, item: item, removeIt: removeIt}
		a.routeItems = append(a.routeItems, ri)

		// Click on route row → toggle (re-add as a visual hint; real toggle via Remove)
		go func(pfx netip.Prefix, mi *systray.MenuItem) {
			for {
				select {
				case <-a.ctx.Done():
					return
				case _, ok := <-mi.ClickedCh:
					if !ok {
						return
					}
					// Clicking the route row opens an info notification.
					a.notifier.Send(
						fmt.Sprintf("Advertising %s", pfx),
						"Use 'Remove' to stop advertising this prefix.",
					)
				}
			}
		}(r.Prefix, item)

		// Remove click
		go func(pfx netip.Prefix, mi *systray.MenuItem) {
			for {
				select {
				case <-a.ctx.Done():
					return
				case _, ok := <-mi.ClickedCh:
					if !ok {
						return
					}
					go a.doRemoveRoute(pfx)
				}
			}
		}(r.Prefix, removeIt)
	}

	// Update parent label.
	if len(current) == 0 {
		a.mAdvertise.SetTitle("Advertise subnets")
		a.mAdvertise.SetTooltip("Share local subnets with the tailnet (none active)")
	} else {
		a.mAdvertise.SetTitle(fmt.Sprintf("Advertise subnets (%d)", len(current)))
		a.mAdvertise.SetTooltip(fmt.Sprintf("%d subnet(s) being advertised", len(current)))
	}
}

// ── Route actions ─────────────────────────────────────────────────────────────

func (a *App) doAddRoute() {
	// Step 1: offer suggestions from local interfaces via zenity --list,
	// with a "Manual entry…" option at the top.
	suggestions, err := routes.Suggest()
	if err != nil {
		log.Printf("routes suggest: %v", err)
	}

	prefix, err := a.pickRoutePrefix(suggestions)
	if err != nil {
		if !errors.Is(err, picker.ErrCancelled) {
			log.Printf("add route picker: %v", err)
			a.notifier.Send("Add route", fmt.Sprintf("Error: %v", err))
		}
		return
	}

	ctx, cancel := context.WithTimeout(a.ctx, 8*time.Second)
	defer cancel()

	if err := a.rm.Add(ctx, prefix); err != nil {
		log.Printf("add route: %v", err)
		a.notifier.Send("Add route", fmt.Sprintf("Failed: %v", err))
		return
	}

	a.notifier.Send(
		"Subnet advertised",
		fmt.Sprintf("%s is now being advertised.\nApprove it in the admin console if required.", prefix),
	)
	log.Printf("routes: added %s", prefix)
	go a.refreshPeers()
}

func (a *App) doRemoveRoute(prefix netip.Prefix) {
	ctx, cancel := context.WithTimeout(a.ctx, 8*time.Second)
	defer cancel()

	if err := a.rm.Remove(ctx, prefix); err != nil {
		log.Printf("remove route: %v", err)
		a.notifier.Send("Remove route", fmt.Sprintf("Failed: %v", err))
		return
	}

	a.notifier.Send("Subnet removed", fmt.Sprintf("No longer advertising %s", prefix))
	log.Printf("routes: removed %s", prefix)
	go a.refreshPeers()
}

// pickRoutePrefix presents the user with a list of interface suggestions plus
// a "Manual entry…" option. Returns the chosen prefix.
func (a *App) pickRoutePrefix(suggestions []routes.Suggestion) (netip.Prefix, error) {
	// Build a single-column list. Each row is "  192.168.1.0/24  (eth0)" so
	// the user sees a friendly label. We parse the CIDR back out of the chosen
	// row using the first word.
	//
	// We also keep a separate "Manual entry…" row at the top (empty prefix).
	type option struct {
		label  string
		prefix string // empty = manual entry
	}

	opts := []option{{label: "  Enter manually…", prefix: ""}}
	for _, s := range suggestions {
		opts = append(opts, option{
			label:  fmt.Sprintf("  %-22s  via %s", s.Prefix.String(), s.Interface),
			prefix: s.Prefix.String(),
		})
	}

	// zenity --list with one visible column; returns the chosen row text.
	args := []string{
		"--list",
		"--title=Advertise subnet",
		"--text=Select a local subnet to advertise:",
		"--column=Subnet",
		"--width=480",
		"--height=300",
	}
	for _, o := range opts {
		args = append(args, o.label)
	}

	out, err := runZenity(args...)
	if err != nil {
		if errors.Is(err, picker.ErrZenityNotFound) || errors.Is(err, picker.ErrCancelled) {
			return netip.Prefix{}, err
		}
		// --list unavailable — fall through to manual entry.
		out = ""
	}

	chosen := strings.TrimSpace(strings.TrimRight(out, "\n\r"))

	// Empty or "Enter manually…" → open the text-entry dialog.
	if chosen == "" || strings.Contains(chosen, "Enter manually") {
		return a.pickRouteManual()
	}

	// Extract the CIDR from the first non-space word of the chosen row.
	fields := strings.Fields(chosen)
	if len(fields) == 0 {
		return a.pickRouteManual()
	}
	cidr := fields[0]

	pfx, err := routes.ParsePrefix(cidr)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("parse selected prefix %q: %w", cidr, err)
	}
	if err := routes.Validate(pfx.String()); err != nil {
		return netip.Prefix{}, err
	}
	return pfx, nil
}

// pickRouteManual shows a zenity --entry dialog for freeform CIDR input.
func (a *App) pickRouteManual() (netip.Prefix, error) {
	out, err := runZenity(
		"--entry",
		"--title=Advertise subnet",
		"--text=Enter a subnet to advertise (CIDR notation):\n\nExamples:\n  192.168.1.0/24\n  10.0.0.0/8\n  172.16.5.0/24",
		"--entry-text=192.168.1.0/24",
		"--width=400",
	)
	if err != nil {
		return netip.Prefix{}, err
	}
	cidr := strings.TrimSpace(strings.TrimRight(out, "\n\r"))
	if cidr == "" {
		return netip.Prefix{}, picker.ErrCancelled
	}
	if err := routes.Validate(cidr); err != nil {
		// Show the validation error back to the user.
		_, _ = runZenity(
			"--error",
			"--title=Invalid subnet",
			"--text="+err.Error(),
			"--width=360",
		)
		return netip.Prefix{}, picker.ErrCancelled
	}
	return routes.ParsePrefix(cidr)
}

// ── Account / multi-user ──────────────────────────────────────────────────────

// watchAuthURL listens on the IPN bus for BrowseToURL notifications.
// When the daemon wants us to open a browser for login, it sends this.
// We store the URL, show a notification, and open the browser automatically.
func (a *App) watchAuthURL() {
	for {
		err := a.ts.WatchAuthURL(a.ctx, func(authURL string) {
			a.mu.Lock()
			a.currentAuthURL = authURL
			a.mu.Unlock()

			log.Printf("account: auth URL received")
			openBrowser(authURL)

			a.notifier.Send(
				"Tailscale — login required",
				"A browser window has been opened to complete login.",
			)
		})
		if a.ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Printf("account: auth URL watcher error (%v), restarting", err)
			select {
			case <-a.ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
		}
	}
}

// refreshAccounts reads the profile list from the daemon and rebuilds the
// Account submenu. Silently no-ops if multi-account is not supported.
func (a *App) refreshAccounts(ctx context.Context) {
	profiles, err := a.am.Profiles(ctx)
	if err != nil {
		// Not an error worth surfacing — daemon may not support profiles yet.
		log.Printf("account: list profiles: %v", err)
		return
	}
	a.rebuildAccountSubmenu(profiles)
}

// rebuildAccountSubmenu replaces the profile list inside the Account submenu.
//
// Layout:
//   Account (alice@example.com)
//     ✓ alice@example.com       ← active, click = no-op / show info
//       bob@corp.example         ← inactive, click = switch
//     ─────────────────
//     Add account…
//     Log out
func (a *App) rebuildAccountSubmenu(profiles []account.Profile) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Hide old profile items.
	for _, ai := range a.accountItems {
		ai.item.Hide()
	}
	a.accountItems = a.accountItems[:0]

	// Add one sub-item per profile, before Add account / Log out.
	for _, p := range profiles {
		checkmark := "  "
		if p.Active {
			checkmark = "✓ "
		}
		label := checkmark + p.Name
		tooltip := "Switch to " + p.Name
		if p.Active {
			tooltip = "Currently active account"
		}
		item := a.mAccount.AddSubMenuItem(label, tooltip)
		if p.Active {
			item.Disable() // can't switch to the account already active
		}

		ai := &accountItem{profile: p, item: item}
		a.accountItems = append(a.accountItems, ai)

		if !p.Active {
			go func(prof account.Profile, mi *systray.MenuItem) {
				for {
					select {
					case <-a.ctx.Done():
						return
					case _, ok := <-mi.ClickedCh:
						if !ok {
							return
						}
						go a.doSwitchAccount(prof)
					}
				}
			}(p, item)
		}
	}

	// Update parent label to show active account name.
	activeLabel := "Account"
	for _, p := range profiles {
		if p.Active {
			activeLabel = "Account (" + truncate(p.Name, 28) + ")"
			break
		}
	}
	a.mAccount.SetTitle(activeLabel)
}

// ── Account actions ───────────────────────────────────────────────────────────

// doSwitchAccount switches the active Tailscale profile.
func (a *App) doSwitchAccount(prof account.Profile) {
	ctx, cancel := context.WithTimeout(a.ctx, 8*time.Second)
	defer cancel()

	if err := a.am.Switch(ctx, prof.ID); err != nil {
		log.Printf("switch account: %v", err)
		a.notifier.Send("Account switch failed", err.Error())
		return
	}
	log.Printf("account: switched to %s", prof.Name)
	a.notifier.Send("Tailscale", "Switched to "+prof.Name)
	// The IPN state change will trigger applyState and a peer refresh.
}

// doAddAccount creates a new profile and starts the login flow.
func (a *App) doAddAccount() {
	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
	defer cancel()

	if err := a.am.AddAndLogin(ctx); err != nil {
		log.Printf("add account: %v", err)
		a.notifier.Send("Add account failed", err.Error())
		return
	}
	// The daemon will emit NeedsLogin → applyState will call
	// StartLoginInteractive → BrowseToURL will appear on the IPN bus →
	// watchAuthURL will open the browser. Nothing more to do here.
	log.Printf("account: add & login initiated")
}

// doLogout logs out the current account.
func (a *App) doLogout() {
	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
	defer cancel()

	if err := a.am.Logout(ctx); err != nil {
		log.Printf("logout: %v", err)
		a.notifier.Send("Logout failed", err.Error())
		return
	}
	log.Printf("account: logged out")
	a.notifier.Send("Tailscale", "Logged out")
}

// doLoginNow opens the browser for login when the user clicks "Log in…".
func (a *App) doLoginNow() {
	a.mu.Lock()
	authURL := a.currentAuthURL
	a.mu.Unlock()

	if authURL != "" {
		openBrowser(authURL)
		return
	}
	// No URL cached — ask the daemon for one.
	ctx, cancel := context.WithTimeout(a.ctx, 8*time.Second)
	defer cancel()
	if err := a.ts.StartLoginInteractive(ctx); err != nil {
		log.Printf("login interactive: %v", err)
		a.notifier.Send("Login failed", err.Error())
		return
	}
	// BrowseToURL will arrive on the IPN bus → watchAuthURL handles it.
}

// truncate shortens s to at most n runes, appending "…" if cut.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}

// ── Taildrop ──────────────────────────────────────────────────────────────────

func (a *App) runTaildrop() {
	a.tdrop.Watch(a.ctx, func(ev taildrop.FileEvent) {
		if ev.Err != nil {
			log.Printf("taildrop receive error: %v", ev.Err)
			// Error notification — no action buttons needed.
			a.notifier.SendWithActions(
				a.ctx,
				notify.FileReceiveErrorNotification(ev.Name, ev.Err),
				func(_ string) {},
			)
			return
		}

		from := ev.From
		if from == "" {
			from = "a peer"
		}
		log.Printf("taildrop: received %s from %q → %s", ev.Name, from, ev.Path)

		// Send an action notification with "Open file" and "Show in folder".
		notif := notify.FileReceivedNotification(ev.Name, from, ev.Path)
		savedPath := ev.Path // capture for closure
		savedDir := a.tdrop.ReceiveDir()

		a.notifier.SendWithActions(a.ctx, notif, func(action string) {
			switch action {
			case "open-file":
				openPath(savedPath)
			case "open-folder":
				openFileManager(savedDir)
			}
		})
	})
}

// ── Menu event handling ───────────────────────────────────────────────────────

func (a *App) handleMenuEvents() {
	for {
		select {
		case <-a.ctx.Done():
			return

		case <-a.mAddRoute.ClickedCh:
			go a.doAddRoute()

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

		case <-a.mAddAccount.ClickedCh:
			go a.doAddAccount()

		case <-a.mLogout.ClickedCh:
			go a.doLogout()

		case <-a.mLoginNow.ClickedCh:
			go a.doLoginNow()

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

func (a *App) doSetExitNode(id tailcfg.StableNodeID, hostname string) {
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

// doSendFile is the "Send file…" top-level tray action.
// It asks the user to pick a file first, then a peer.
func (a *App) doSendFile() {
	// Step 1: file picker
	paths, err := picker.PickFiles()
	if err != nil {
		a.handlePickerError("file", err)
		return
	}
	if len(paths) == 0 {
		return // cancelled
	}

	// Step 2: peer picker — build list of online peers
	onlinePeers, err := a.onlinePeers()
	if err != nil {
		a.notifier.Send("Taildrop", fmt.Sprintf("Could not fetch peers: %v", err))
		return
	}
	if len(onlinePeers) == 0 {
		a.notifier.Send("Taildrop", "No peers are online")
		return
	}

	target, err := picker.PickPeer(onlinePeers)
	if err != nil {
		a.handlePickerError("peer", err)
		return
	}

	// Step 3: send all chosen files
	for _, path := range paths {
		go a.sendOneFile(path, target)
	}
}

// doSendFileToPeer is triggered from a specific peer's "Send file…" sub-item.
// The peer is already known so only a file picker is shown.
func (a *App) doSendFileToPeer(target *ipnstate.PeerStatus) {
	paths, err := picker.PickFiles()
	if err != nil {
		a.handlePickerError("file", err)
		return
	}
	if len(paths) == 0 {
		return // cancelled
	}
	for _, path := range paths {
		go a.sendOneFile(path, target)
	}
}

// sendOneFile sends a single file to target and shows a progress bar + notification.
func (a *App) sendOneFile(path string, target *ipnstate.PeerStatus) {
	log.Printf("taildrop: %s → %s", path, target.HostName)

	ch, err := a.tdrop.SendFile(a.ctx, target.ID, path)
	if err != nil {
		a.notifier.Send("Taildrop", fmt.Sprintf("Send failed: %v", err))
		return
	}

	// Open a zenity progress bar (no-op if zenity unavailable).
	var prog *picker.ProgressDialog
	for p := range ch {
		if prog == nil && p.Total > 0 {
			prog = picker.NewProgressDialog(p.Name, p.Total)
		}
		if prog != nil {
			prog.Update(p.Sent)
		}
		if p.Done {
			prog.Close()
			a.notifier.Send(
				"Taildrop — sent",
				fmt.Sprintf("%s → %s", p.Name, target.HostName),
			)
			return
		}
		if p.Err != nil {
			prog.Close()
			a.notifier.SendUrgent("Taildrop error", p.Err.Error())
			log.Printf("taildrop send error: %v", p.Err)
			return
		}
	}
}

// SSHByPeerID is called by the status window when the user clicks "SSH"
// next to a specific peer. It resolves the StableNodeID to a PeerStatus
// and launches a terminal.
func (a *App) SSHByPeerID(targetID string) {
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	peers, err := a.ts.Peers(ctx)
	if err != nil {
		log.Printf("SSHByPeerID: fetch peers: %v", err)
		return
	}
	for _, p := range peers {
		if string(p.ID) == targetID {
			a.doSSH(p)
			return
		}
	}
	a.notifier.Send("SSH", "Peer not found or offline")
}

// SendFileToPeerID is called by the status window when the user clicks
// "Send file" next to a specific peer. It resolves the StableNodeID to a
// PeerStatus and opens the file picker.
func (a *App) SendFileToPeerID(targetID string) {
	online, err := a.onlinePeers()
	if err != nil {
		log.Printf("SendFileToPeerID: %v", err)
		return
	}
	for _, p := range online {
		if string(p.ID) == targetID {
			a.doSendFileToPeer(p)
			return
		}
	}
	a.notifier.Send("Taildrop", "Peer not found or offline")
}

// sshConfig builds an sshlaunch.Config from user preferences.
func (a *App) sshConfig() sshlaunch.Config {
	return sshlaunch.Config{
		TerminalCmd: a.cfg.TerminalCmd,
		SSHUser:     a.cfg.SSHUser,
	}
}

// doSSH launches a terminal with ssh connected to the given peer.
func (a *App) doSSH(p *ipnstate.PeerStatus) {
	cfg := a.sshConfig()
	if err := sshlaunch.Launch(p, cfg); err != nil {
		log.Printf("ssh launch: %v", err)
		a.notifier.SendUrgent(
			"SSH failed",
			fmt.Sprintf("Could not open terminal for %s:\n%v", p.HostName, err),
		)
		return
	}
	log.Printf("ssh: launched → %s (%s)", p.HostName, sshlaunch.Target(p))
}

// onlinePeers returns peers that are currently online, sorted by hostname.
func (a *App) onlinePeers() ([]*ipnstate.PeerStatus, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	peers, err := a.ts.Peers(ctx)
	if err != nil {
		return nil, err
	}
	var online []*ipnstate.PeerStatus
	for _, p := range peers {
		if p.Online {
			online = append(online, p)
		}
	}
	return online, nil
}

// handlePickerError maps picker errors to user-visible messages.
func (a *App) handlePickerError(stage string, err error) {
	switch {
	case errors.Is(err, picker.ErrCancelled):
		// User cancelled — silent.
	case errors.Is(err, picker.ErrZenityNotFound):
		a.notifier.SendUrgent(
			"Taildrop: zenity required",
			"Install zenity to use the file sender:\n  sudo apt install zenity",
		)
		log.Printf("taildrop %s picker: %v", stage, err)
	default:
		a.notifier.Send("Taildrop", fmt.Sprintf("%s picker: %v", stage, err))
		log.Printf("taildrop %s picker: %v", stage, err)
	}
}
