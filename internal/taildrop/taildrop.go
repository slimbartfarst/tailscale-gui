// internal/taildrop/taildrop.go
//
// Taildrop file send/receive via the tailscaled local API.
//
// Receiving: watches the IPN bus for incoming file notifications and saves
//            them to a configurable directory.
// Sending:   wraps local.Client.PushFile for outbound transfers.
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
	"github.com/yourname/tailscale-gui/internal/client"
)

const defaultReceiveDir = "~/Downloads/Taildrop"

// Manager handles Taildrop send and receive.
type Manager struct {
	ts         *client.Client
	receiveDir string // expanded path
}

// New creates a Manager. receiveDir may be "" to use the default.
func New(ts *client.Client, receiveDir string) *Manager {
	if receiveDir == "" {
		receiveDir = defaultReceiveDir
	}
	// Expand leading ~
	if strings.HasPrefix(receiveDir, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			receiveDir = filepath.Join(home, receiveDir[2:])
		}
	}
	return &Manager{ts: ts, receiveDir: receiveDir}
}

// ReceiveDir returns the directory where received files are saved.
func (m *Manager) ReceiveDir() string { return m.receiveDir }

// ── Receiving ─────────────────────────────────────────────────────────────────

// FileEvent is emitted for each file received.
type FileEvent struct {
	Name string // filename
	Path string // full path on disk
	Size int64
	From string // sender hostname
	Err  error
}

// Watch listens on the IPN bus for incoming Taildrop files and saves them to
// ReceiveDir. It calls onFile for each received file. Blocks until ctx done.
func (m *Manager) Watch(ctx context.Context, onFile func(FileEvent)) {
	if err := os.MkdirAll(m.receiveDir, 0o755); err != nil {
		log.Printf("taildrop: cannot create receive dir %s: %v", m.receiveDir, err)
		return
	}

	// Poll for waiting files every 5 s as a fallback, and also react to
	// IPN notifications. The local API exposes WaitingFiles() to list files
	// that have been received but not yet claimed.
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	// Also watch the IPN bus for file-notify events.
	go m.watchBus(ctx, ticker)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.drainWaitingFiles(ctx, onFile)
		}
	}
}

// watchBus listens for ipn.Notify.FilesWaiting and resets the ticker.
// (The ticker channel is used as a signal — we just poke it via a separate
// goroutine that sends to a shared channel in a real implementation.
// For this scaffold we simply call drainWaitingFiles on a short interval.)
func (m *Manager) watchBus(ctx context.Context, _ *time.Ticker) {
	err := m.ts.WatchState(ctx, func(sc client.StateChange) {
		// FilesWaiting is signalled through the IPN bus; in a full
		// implementation you'd check n.FilesWaiting here via WatchIPNBus
		// directly. The polling loop above covers the common case.
		_ = sc
	})
	if err != nil && ctx.Err() == nil {
		log.Printf("taildrop: bus watch error: %v", err)
	}
}

// drainWaitingFiles fetches and saves all files that tailscaled has received
// but not yet handed to us.
func (m *Manager) drainWaitingFiles(ctx context.Context, onFile func(FileEvent)) {
	lc := m.ts.LocalClient()

	files, err := lc.WaitingFiles(ctx)
	if err != nil {
		// Not connected yet — silent.
		return
	}
	for _, wf := range files {
		ev := m.saveFile(ctx, lc, wf.Name)
		if onFile != nil {
			onFile(ev)
		}
	}
}

// saveFile claims a single waiting file from tailscaled and writes it to disk.
func (m *Manager) saveFile(ctx context.Context, lc *local.Client, name string) FileEvent {
	// Sanitise the filename so it can't escape the receive dir.
	safeName := filepath.Base(filepath.Clean(name))
	if safeName == "." || safeName == ".." {
		return FileEvent{Name: name, Err: fmt.Errorf("invalid filename")}
	}

	dest := filepath.Join(m.receiveDir, safeName)

	rc, size, err := lc.GetWaitingFile(ctx, name)
	if err != nil {
		return FileEvent{Name: name, Err: fmt.Errorf("get: %w", err)}
	}
	defer rc.Close()

	f, err := os.Create(dest)
	if err != nil {
		return FileEvent{Name: name, Err: fmt.Errorf("create: %w", err)}
	}
	defer f.Close()

	if _, err := io.Copy(f, rc); err != nil {
		return FileEvent{Name: name, Err: fmt.Errorf("write: %w", err)}
	}

	// Delete the file from the daemon's waiting queue.
	if err := lc.DeleteWaitingFile(ctx, name); err != nil {
		log.Printf("taildrop: delete waiting file %q: %v", name, err)
	}

	log.Printf("taildrop: saved %s (%d bytes) → %s", name, size, dest)
	return FileEvent{Name: safeName, Path: dest, Size: size}
}

// ── Sending ───────────────────────────────────────────────────────────────────

// SendFile sends a file to a peer identified by its StableNodeID.
// Progress is reported via the returned channel (bytes sent so far).
// The channel is closed when the transfer completes or fails.
func (m *Manager) SendFile(ctx context.Context, targetID ipn.StableNodeID, filePath string) (<-chan SendProgress, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("stat: %w", err)
	}

	// Detect content type.
	ext := filepath.Ext(filePath)
	contentType := mime.TypeByExtension(ext)
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	progress := make(chan SendProgress, 16)
	total := info.Size()
	name := filepath.Base(filePath)

	go func() {
		defer f.Close()
		defer close(progress)

		pr := &progressReader{r: f, total: total, ch: progress, name: name}
		err := m.ts.LocalClient().PushFile(ctx, targetID, total,
			name, contentType, pr)
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
	ct := mime.TypeByExtension(ext)
	if ct != "" {
		return ct
	}
	// Read first 512 bytes to sniff.
	f, err := os.Open(path)
	if err != nil {
		return "application/octet-stream"
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	return http.DetectContentType(buf[:n])
}
