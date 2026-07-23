// internal/taildrop/taildrop.go
//
// Taildrop file send/receive via the tailscaled local API.
//
// Receiving:
//   Two complementary mechanisms run in parallel:
//   1. IPN bus watcher  — reacts immediately when tailscaled signals that
//      files are waiting (ipn.Notify.FilesWaiting == true).  Zero latency.
//   2. Polling fallback — drains any files that arrived while the watcher
//      was restarting (5-second interval).
//
// Sending:
//   Wraps local.Client.PushFile with a progress channel.
package taildrop

import (
	"context"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tailscale.com/client/local"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"

	"github.com/yourname/tailscale-gui/internal/client"
)

const defaultReceiveDir = "~/Downloads/Taildrop"

// Manager handles Taildrop send and receive.
type Manager struct {
	ts         *client.Client
	receiveDir string // fully expanded path
}

// New creates a Manager. receiveDir may be "" to use the default.
func New(ts *client.Client, receiveDir string) *Manager {
	if receiveDir == "" {
		receiveDir = defaultReceiveDir
	}
	if strings.HasPrefix(receiveDir, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			receiveDir = filepath.Join(home, receiveDir[2:])
		}
	}
	return &Manager{ts: ts, receiveDir: receiveDir}
}

// ReceiveDir returns the directory where received files are saved.
func (m *Manager) ReceiveDir() string { return m.receiveDir }

// ── Receiving ─────────────────────────────────────────────────────────────────

// FileEvent is emitted for each file received or each receive error.
type FileEvent struct {
	Name string // sanitised filename
	Path string // full path on disk (empty on error)
	Size int64
	From string // sender hostname (may be empty if unknown)
	Err  error
}

// Watch starts the receive loop and calls onFile for every received file.
// Blocks until ctx is cancelled. Call from a goroutine.
func (m *Manager) Watch(ctx context.Context, onFile func(FileEvent)) {
	if err := os.MkdirAll(m.receiveDir, 0o755); err != nil {
		log.Printf("taildrop: cannot create receive dir %s: %v", m.receiveDir, err)
		return
	}

	// trigger is closed/replaced each time we want an immediate drain.
	trigger := make(chan struct{}, 1)
	nudge := func() {
		select {
		case trigger <- struct{}{}:
		default:
		}
	}

	// IPN bus watcher — fires trigger whenever FilesWaiting becomes true.
	go m.watchIPNBus(ctx, nudge)

	// Drain loop — responds to both triggers and the 5-second fallback ticker.
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	// Build a peer map for resolving sender hostnames.
	peers := m.buildPeerMap(ctx)
	peerRefresh := time.NewTicker(60 * time.Second)
	defer peerRefresh.Stop()

	// Drain immediately on start in case files arrived before we launched.
	nudge()

	for {
		select {
		case <-ctx.Done():
			return
		case <-peerRefresh.C:
			peers = m.buildPeerMap(ctx)
		case <-ticker.C:
			m.drainWaitingFiles(ctx, peers, onFile)
		case <-trigger:
			m.drainWaitingFiles(ctx, peers, onFile)
		}
	}
}

// watchIPNBus subscribes to the IPN notification bus and nudges the drain loop
// whenever FilesWaiting is signalled. Automatically reconnects on errors.
func (m *Manager) watchIPNBus(ctx context.Context, nudge func()) {
	for {
		if err := m.watchIPNBusOnce(ctx, nudge); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("taildrop: IPN bus error (%v), reconnecting in 3s", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
		}
	}
}

func (m *Manager) watchIPNBusOnce(ctx context.Context, nudge func()) error {
	watcher, err := m.ts.LocalClient().WatchIPNBus(ctx, 0)
	if err != nil {
		return err
	}
	defer watcher.Close()

	for {
		n, err := watcher.Next()
		if err != nil {
			return err
		}
		if n.FilesWaiting != nil {
			log.Printf("taildrop: FilesWaiting signal received")
			nudge()
		}
	}
}

// buildPeerMap returns a map from Tailscale IP string → hostname for all
// currently known peers. Used to resolve who sent a file.
func (m *Manager) buildPeerMap(ctx context.Context) map[string]string {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	st, err := m.ts.Status(ctx)
	if err != nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(st.Peer))
	for _, p := range st.Peer {
		for _, ip := range p.TailscaleIPs {
			out[ip.String()] = hostLabel(p)
		}
	}
	return out
}

// hostLabel returns the most human-readable name for a peer.
func hostLabel(p *ipnstate.PeerStatus) string {
	if p.DNSName != "" {
		// Strip trailing dot and domain suffix — keep just the hostname part.
		parts := strings.SplitN(strings.TrimSuffix(p.DNSName, "."), ".", 2)
		return parts[0]
	}
	return p.HostName
}

// drainWaitingFiles fetches every file waiting in the daemon queue and saves
// them all, calling onFile for each.
func (m *Manager) drainWaitingFiles(
	ctx context.Context,
	peers map[string]string,
	onFile func(FileEvent),
) {
	lc := m.ts.LocalClient()
	files, err := lc.WaitingFiles(ctx)
	if err != nil {
		// Silent — daemon may not be ready yet.
		return
	}
	for _, wf := range files {
		// Try to resolve sender IP → hostname.
		from := ""
		if wf.Sender != "" {
			if name, ok := peers[wf.Sender]; ok {
				from = name
			} else {
				from = wf.Sender // fall back to raw IP
			}
		}
		ev := m.saveFile(ctx, lc, wf.Name, from)
		if onFile != nil {
			onFile(ev)
		}
	}
}

// saveFile claims a single waiting file from tailscaled and writes it to disk.
func (m *Manager) saveFile(
	ctx context.Context,
	lc *local.Client,
	name string,
	from string,
) FileEvent {
	// Sanitise filename — prevent path traversal.
	safeName := filepath.Base(filepath.Clean(name))
	if safeName == "." || safeName == ".." || safeName == "" {
		return FileEvent{Name: name, From: from, Err: fmt.Errorf("invalid filename")}
	}

	// If a file with that name already exists, append a counter.
	dest := m.uniqueDest(safeName)

	rc, size, err := lc.GetWaitingFile(ctx, name)
	if err != nil {
		return FileEvent{Name: safeName, From: from, Err: fmt.Errorf("get: %w", err)}
	}
	defer rc.Close()

	f, err := os.Create(dest)
	if err != nil {
		return FileEvent{Name: safeName, From: from, Err: fmt.Errorf("create: %w", err)}
	}
	defer f.Close()

	written, err := io.Copy(f, rc)
	if err != nil {
		os.Remove(dest) // clean up partial file
		return FileEvent{Name: safeName, From: from, Err: fmt.Errorf("write: %w", err)}
	}

	// Remove from daemon queue only after successful write.
	if err := lc.DeleteWaitingFile(ctx, name); err != nil {
		log.Printf("taildrop: delete waiting file %q: %v", name, err)
	}

	log.Printf("taildrop: received %s (%d bytes) from %q → %s", safeName, written, from, dest)
	return FileEvent{Name: filepath.Base(dest), Path: dest, Size: size, From: from}
}

// uniqueDest returns a path that doesn't already exist.
// If ~/Downloads/Taildrop/file.txt exists it returns ~/Downloads/Taildrop/file (1).txt, etc.
func (m *Manager) uniqueDest(name string) string {
	candidate := filepath.Join(m.receiveDir, name)
	if _, err := os.Stat(candidate); os.IsNotExist(err) {
		return candidate
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; i < 1000; i++ {
		candidate = filepath.Join(m.receiveDir, fmt.Sprintf("%s (%d)%s", base, i, ext))
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
	return filepath.Join(m.receiveDir, name) // give up, let os.Create overwrite
}

// ── Sending ───────────────────────────────────────────────────────────────────

// SendFile sends a file to a peer identified by its StableNodeID.
// Progress is reported via the returned channel; closed when done or failed.
func (m *Manager) SendFile(
	ctx context.Context,
	targetID ipn.StableNodeID,
	filePath string,
) (<-chan SendProgress, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("stat: %w", err)
	}

	ext := filepath.Ext(filePath)
	contentType := mime.TypeByExtension(ext)
	if contentType == "" {
		contentType = ContentTypeForFile(filePath)
	}

	progress := make(chan SendProgress, 16)
	total := info.Size()
	name := filepath.Base(filePath)

	go func() {
		defer f.Close()
		defer close(progress)

		pr := &progressReader{r: f, total: total, ch: progress, name: name}
		err := m.ts.LocalClient().PushFile(ctx, targetID, total, name, contentType, pr)
		if err != nil {
			progress <- SendProgress{Name: name, Total: total, Err: err}
		} else {
			progress <- SendProgress{Name: name, Sent: total, Total: total, Done: true}
		}
	}()

	return progress, nil
}

// SendProgress carries the state of an outbound Taildrop transfer.
type SendProgress struct {
	Name  string
	Sent  int64
	Total int64
	Done  bool
	Err   error
}

// progressReader wraps an io.Reader and emits progress events.
type progressReader struct {
	r     io.Reader
	sent  int64
	total int64
	ch    chan<- SendProgress
	last  time.Time
	name  string
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	pr.sent += int64(n)
	if time.Since(pr.last) > 250*time.Millisecond || err == io.EOF {
		pr.ch <- SendProgress{Name: pr.name, Sent: pr.sent, Total: pr.total}
		pr.last = time.Now()
	}
	return n, err
}

// ContentTypeForFile guesses the MIME type for a file path.
func ContentTypeForFile(path string) string {
	ext := filepath.Ext(path)
	if ct := mime.TypeByExtension(ext); ct != "" {
		return ct
	}
	f, err := os.Open(path)
	if err != nil {
		return "application/octet-stream"
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	return http.DetectContentType(buf[:n])
}
