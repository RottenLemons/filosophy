package whatsapp

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/skip2/go-qrcode"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"
	_ "modernc.org/sqlite"
)

// waLogger forwards whatsmeow internal logs to Go's standard logger.
type waLogger struct{ prefix string }

func (w waLogger) Debugf(msg string, args ...any) { log.Printf("[WA:DBG:%s] "+msg, append([]any{w.prefix}, args...)...) }
func (w waLogger) Infof(msg string, args ...any)  { log.Printf("[WA:INF:%s] "+msg, append([]any{w.prefix}, args...)...) }
func (w waLogger) Warnf(msg string, args ...any)  { log.Printf("[WA:WRN:%s] "+msg, append([]any{w.prefix}, args...)...) }
func (w waLogger) Errorf(msg string, args ...any) { log.Printf("[WA:ERR:%s] "+msg, append([]any{w.prefix}, args...)...) }
func (w waLogger) Sub(module string) waLog.Logger { return waLogger{prefix: w.prefix + "/" + module} }

// Status represents the current connection state of the WhatsApp client.
type Status string

const (
	StatusDisconnected Status = "disconnected"
	StatusConnecting   Status = "connecting"
	StatusConnected    Status = "connected"
	StatusLoggedOut    Status = "logged_out"
)

// QRUpdate is sent to the frontend when a new QR code is available or
// the pairing state changes.
type QRUpdate struct {
	// Base64-encoded PNG of the QR code, or "" for non-code events.
	ImageBase64 string `json:"imageBase64"`
	// One of: "code", "success", "timeout", "error", "connected"
	Event string `json:"event"`
	Error string `json:"error,omitempty"`
}

// OnQRUpdate is called whenever a QR event occurs (new code, success, error).
type OnQRUpdate func(QRUpdate)

// OnStatusChange is called when the connection status changes.
type OnStatusChange func(Status)

// Client wraps a whatsmeow client with session persistence and reconnect logic.
type Client struct {
	mu        sync.Mutex
	wac       *whatsmeow.Client // nil until Connect is called
	sessionDB *sql.DB
	store     *Store // our local message DB
	dir       string // working directory for session DB
	status    Status
	lastQR    string // last QR base64 PNG, so frontend can poll it

	onQR     OnQRUpdate
	onStatus OnStatusChange

	cancelReconnect context.CancelFunc
	ctx             context.Context
}

// NewClient creates a new WhatsApp client. Call Connect() to pair or reconnect.
// dir is the directory where whatsapp-session.db and whatsapp.db are stored.
func NewClient(dir string, store *Store, onQR OnQRUpdate, onStatus OnStatusChange) *Client {
	return &Client{
		dir:      dir,
		store:    store,
		status:   StatusDisconnected,
		onQR:     onQR,
		onStatus: onStatus,
	}
}

// Status returns the current connection status.
func (c *Client) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

// LastQR returns the most recently generated QR code as a base64 PNG data URI.
// Returns "" if no QR code has been generated yet or pairing already succeeded.
func (c *Client) LastQR() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastQR
}

// IsConnected reports whether the client is currently connected and logged in.
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.wac != nil && c.wac.IsConnected() && c.wac.IsLoggedIn()
}

// AutoConnect reconnects a previously saved session at startup without showing
// a QR code. It is a no-op if there is no saved session. Runs asynchronously
// so it does not block the startup path.
func (c *Client) AutoConnect(appCtx context.Context) {
	go func() {
		if err := c.connect(appCtx, false); err != nil {
			log.Printf("[WhatsApp] Auto-connect failed: %v", err)
		}
	}()
}

// Connect initialises the whatsmeow client, restores an existing session if one
// exists, or starts QR pairing for a new session.
// It is safe to call multiple times — it no-ops if already connected.
func (c *Client) Connect(appCtx context.Context) error {
	return c.connect(appCtx, true)
}

func (c *Client) connect(appCtx context.Context, allowPairing bool) error {
	c.mu.Lock()
	if c.wac != nil && c.wac.IsConnected() {
		c.mu.Unlock()
		log.Println("[WhatsApp] Connect: already connected, no-op")
		return nil
	}
	if c.wac != nil {
		c.wac.Disconnect()
		c.wac = nil
	}
	if c.sessionDB != nil {
		_ = c.sessionDB.Close()
		c.sessionDB = nil
	}
	c.mu.Unlock()

	c.setStatus(StatusConnecting)
	log.Println("[WhatsApp] Connect: starting session DB setup")

	// Open the session DB with modernc sqlite, then hand it to whatsmeow's sqlstore.
	// We avoid sqlstore.New() because it expects a "sqlite3" driver (mattn);
	// our project uses modernc.org/sqlite which registers as "sqlite".
	sessionPath := filepath.Join(c.dir, "whatsapp-session.db")
	log.Printf("[WhatsApp] Connect: opening session db at %s", sessionPath)
	sessionDB, err := sql.Open("sqlite", sessionPath)
	if err != nil {
		c.setStatus(StatusDisconnected)
		return fmt.Errorf("whatsapp: open session db: %w", err)
	}
	sessionDB.SetMaxOpenConns(1)

	// Verify the DB is actually reachable (sql.Open is lazy)
	if err := sessionDB.Ping(); err != nil {
		sessionDB.Close()
		c.setStatus(StatusDisconnected)
		return fmt.Errorf("whatsapp: ping session db: %w", err)
	}

	// modernc.org/sqlite ignores DSN pragma params; execute them explicitly.
	if _, err := sessionDB.ExecContext(appCtx, "PRAGMA foreign_keys = ON"); err != nil {
		sessionDB.Close()
		c.setStatus(StatusDisconnected)
		return fmt.Errorf("whatsapp: enable foreign keys: %w", err)
	}
	log.Println("[WhatsApp] Connect: session db open, running migrations")

	container := sqlstore.NewWithDB(sessionDB, "sqlite3", waLogger{prefix: "store"})
	if err := container.Upgrade(appCtx); err != nil {
		sessionDB.Close()
		c.setStatus(StatusDisconnected)
		return fmt.Errorf("whatsapp: upgrade session store: %w", err)
	}
	log.Println("[WhatsApp] Connect: migrations done, getting device")

	device, err := container.GetFirstDevice(appCtx)
	if err != nil {
		sessionDB.Close()
		c.setStatus(StatusDisconnected)
		return fmt.Errorf("whatsapp: get device: %w", err)
	}
	log.Printf("[WhatsApp] Connect: got device (ID=%v)", device.ID)

	wac := whatsmeow.NewClient(device, waLogger{prefix: "client"})
	c.mu.Lock()
	c.wac = wac
	c.sessionDB = sessionDB
	c.mu.Unlock()

	// Register event handler (defined in events.go)
	wac.AddEventHandler(c.handleEvent)

	if device.ID == nil {
		if !allowPairing {
			// Auto-connect at startup: no saved session, nothing to do.
			log.Println("[WhatsApp] AutoConnect: no saved session, skipping")
			c.setStatus(StatusDisconnected)
			return nil
		}
		log.Println("[WhatsApp] Connect: no saved session — starting QR pairing")
		return c.doPairing(appCtx)
	}

	log.Println("[WhatsApp] Connect: saved session found — reconnecting")
	if err := wac.Connect(); err != nil {
		c.setStatus(StatusDisconnected)
		return fmt.Errorf("whatsapp: reconnect: %w", err)
	}
	c.setStatus(StatusConnected)
	c.startReconnectWatcher(appCtx)
	log.Println("[WhatsApp] Connect: reconnected successfully")
	return nil
}

// Disconnect cleanly closes the WhatsApp connection.
func (c *Client) Disconnect() {
	c.mu.Lock()
	cancel := c.cancelReconnect
	wac := c.wac
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if wac != nil {
		wac.Disconnect()
	}
	c.setStatus(StatusDisconnected)
}

// Logout logs out of WhatsApp and deletes the local session.
func (c *Client) Logout(ctx context.Context) error {
	c.mu.Lock()
	wac := c.wac
	c.mu.Unlock()

	if wac == nil {
		return nil
	}
	err := wac.Logout(ctx)
	c.setStatus(StatusLoggedOut)
	return err
}

// ResetSession forgets the local WhatsApp session so the next Connect starts QR pairing.
func (c *Client) ResetSession() error {
	c.mu.Lock()
	cancel := c.cancelReconnect
	wac := c.wac
	sessionDB := c.sessionDB
	c.cancelReconnect = nil
	c.wac = nil
	c.sessionDB = nil
	c.lastQR = ""
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if wac != nil {
		wac.Disconnect()
	}
	if sessionDB != nil {
		_ = sessionDB.Close()
	}

	sessionPath := filepath.Join(c.dir, "whatsapp-session.db")
	var removeErr error
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		path := sessionPath + suffix
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) && removeErr == nil {
			removeErr = err
		}
	}

	c.setStatus(StatusLoggedOut)
	if removeErr != nil {
		return fmt.Errorf("whatsapp: reset session: %w", removeErr)
	}
	return nil
}

// ── QR Pairing ────────────────────────────────────────────────────────────────

func (c *Client) doPairing(ctx context.Context) error {
	qrCh, err := c.wac.GetQRChannel(ctx)
	if err != nil {
		c.setStatus(StatusDisconnected)
		return fmt.Errorf("whatsapp: get QR channel: %w", err)
	}

	if err := c.wac.Connect(); err != nil {
		c.setStatus(StatusDisconnected)
		return fmt.Errorf("whatsapp: connect for QR: %w", err)
	}

	go func() {
		for item := range qrCh {
			switch item.Event {
			case whatsmeow.QRChannelEventCode:
				img64, err := qrToBase64PNG(item.Code)
				if err != nil {
					log.Printf("[WhatsApp] QR encode error: %v", err)
					continue
				}
				c.mu.Lock()
				c.lastQR = img64
				c.mu.Unlock()
				if c.onQR != nil {
					c.onQR(QRUpdate{ImageBase64: img64, Event: "code"})
				}

			case whatsmeow.QRChannelSuccess.Event:
				c.mu.Lock()
				c.lastQR = ""
				c.mu.Unlock()
				c.setStatus(StatusConnected)
				if c.onQR != nil {
					c.onQR(QRUpdate{Event: "success"})
				}
				c.startReconnectWatcher(ctx)

			case whatsmeow.QRChannelTimeout.Event:
				c.mu.Lock()
				c.lastQR = ""
				c.mu.Unlock()
				c.setStatus(StatusDisconnected)
				if c.onQR != nil {
					c.onQR(QRUpdate{Event: "timeout"})
				}

			default:
				if c.onQR != nil {
					c.onQR(QRUpdate{Event: "error", Error: item.Event})
				}
			}
		}
	}()

	return nil
}

// ── Reconnect watcher ─────────────────────────────────────────────────────────

// startReconnectWatcher runs in the background and reconnects after drops.
func (c *Client) startReconnectWatcher(parentCtx context.Context) {
	ctx, cancel := context.WithCancel(parentCtx)
	c.mu.Lock()
	if c.cancelReconnect != nil {
		c.cancelReconnect() // stop any previous watcher
	}
	c.cancelReconnect = cancel
	c.mu.Unlock()

	go func() {
		backoff := 5 * time.Second
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Second):
			}

			c.mu.Lock()
			wac := c.wac
			c.mu.Unlock()

			if wac == nil {
				return
			}
			if wac.IsConnected() {
				backoff = 5 * time.Second // reset on healthy check
				continue
			}

			log.Printf("[WhatsApp] Disconnected — reconnecting in %v", backoff)
			c.setStatus(StatusConnecting)
			time.Sleep(backoff)

			if err := wac.Connect(); err != nil {
				log.Printf("[WhatsApp] Reconnect error: %v", err)
				backoff = min(backoff*2, 5*time.Minute)
			} else {
				log.Println("[WhatsApp] Reconnected successfully")
				c.setStatus(StatusConnected)
				backoff = 5 * time.Second
			}
		}
	}()
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func (c *Client) setStatus(s Status) {
	c.mu.Lock()
	c.status = s
	cb := c.onStatus
	c.mu.Unlock()
	if cb != nil {
		cb(s)
	}
}

// qrToBase64PNG encodes a QR code string to a base64 PNG image.
func qrToBase64PNG(code string) (string, error) {
	qr, err := qrcode.New(code, qrcode.Medium)
	if err != nil {
		return "", err
	}
	img := qr.Image(256)
	var buf []byte
	bufWriter := &byteWriter{buf: &buf}
	if err := png.Encode(bufWriter, img); err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf), nil
}

// byteWriter implements io.Writer backed by a *[]byte.
type byteWriter struct{ buf *[]byte }

func (w *byteWriter) Write(p []byte) (int, error) {
	*w.buf = append(*w.buf, p...)
	return len(p), nil
}

func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
