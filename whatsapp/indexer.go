package whatsapp

// Indexer bridges the WhatsApp message store and the Filosophy semantic engine.
// Strategy: each chat is indexed as one "document" whose path is
//
//	whatsapp://chat/<jid>
//
// The document content is the concatenation of the most recent 200 messages.
// When a new message arrives, the chat's document is re-indexed.
// This means chats surface in the main Search() results alongside regular files.

import (
	"fmt"
	"log"
	"sync"
	"time"

	"filosophy/shared"
)

const (
	// whatsAppPathPrefix is the URI scheme used for all WhatsApp search results.
	whatsAppPathPrefix = "whatsapp://chat/"

	// maxMessagesPerDoc is how many recent messages get packed into one embedding.
	maxMessagesPerDoc = 200

	// reindexDebounce prevents hammering the engine during a history sync burst.
	reindexDebounce = 3 * time.Second
)

// Indexer watches for new messages and keeps the semantic engine up to date.
type Indexer struct {
	store  *Store
	engine *shared.Engine

	mu      sync.Mutex
	pending map[string]time.Time // chatJID → time of last queue
	quit    chan struct{}
	wakeup  chan struct{}

	// StatusCallback is called only during IndexAll to report global status.
	// Routine background syncs are silent.
	StatusCallback func(isIndexing bool, message string, progress int)
}

// NewIndexer creates an Indexer but does not start it.
func NewIndexer(store *Store, engine *shared.Engine) *Indexer {
	idx := &Indexer{
		store:   store,
		engine:  engine,
		pending: make(map[string]time.Time),
		quit:    make(chan struct{}),
		wakeup:  make(chan struct{}, 1),
	}
	// Wire the whatsapp package-level callback so the client can notify us.
	OnNewMessage = idx.Enqueue
	return idx
}

// Start launches the background re-indexing goroutine.
func (idx *Indexer) Start() {
	go idx.run()
}

// Stop shuts the background goroutine down.
func (idx *Indexer) Stop() {
	close(idx.quit)
}

// Enqueue marks a chat as needing re-indexing. Safe to call from any goroutine.
func (idx *Indexer) Enqueue(chatJID string) {
	idx.mu.Lock()
	idx.pending[chatJID] = time.Now()
	idx.mu.Unlock()
	select {
	case idx.wakeup <- struct{}{}:
	default:
	}
}

// IndexAll re-indexes every chat in the store. Call once after Connect() for a
// full initial pass over existing data.
func (idx *Indexer) IndexAll() {
	chats, err := idx.store.GetChats()
	if err != nil {
		log.Printf("[WA Indexer] GetChats error: %v", err)
		return
	}
	total := len(chats)
	log.Printf("[WA Indexer] Full re-index: %d chat(s)", total)
	for i, c := range chats {
		if idx.StatusCallback != nil {
			progress := int((float64(i) / float64(total)) * 100)
			idx.StatusCallback(true, fmt.Sprintf("Indexing WhatsApp: %d/%d", i+1, total), progress)
		}
		idx.indexChat(c.JID)
	}
	if idx.StatusCallback != nil {
		idx.StatusCallback(false, "WhatsApp indexing complete", 100)
	}
}

// ── Background loop ───────────────────────────────────────────────────────────

func (idx *Indexer) run() {
	ticker := time.NewTicker(reindexDebounce)
	defer ticker.Stop()

	for {
		select {
		case <-idx.quit:
			return
		case <-idx.wakeup:
		case <-ticker.C:
		}

		now := time.Now()
		idx.mu.Lock()
		var ready []string
		for jid, queued := range idx.pending {
			if now.Sub(queued) >= reindexDebounce {
				ready = append(ready, jid)
				delete(idx.pending, jid)
			}
		}
		idx.mu.Unlock()

		for _, jid := range ready {
			idx.indexChat(jid)
		}
	}
}

// ── Per-chat indexing ─────────────────────────────────────────────────────────

func (idx *Indexer) indexChat(chatJID string) {
	text, err := idx.store.ChatTextForIndex(chatJID, maxMessagesPerDoc)
	if err != nil {
		log.Printf("[WA Indexer] ChatTextForIndex %s: %v", chatJID, err)
		return
	}
	if text == "" {
		return
	}

	path := ChatPath(chatJID)

	// Use zero-value metadata — WhatsApp docs don't have file timestamps/sizes.
	err = idx.engine.IndexText(
		[]string{text},
		[]string{path},
		[]string{path},
		[]int64{0}, // hash
		[]int64{time.Now().Unix()},
		[]int64{int64(len(text))},
		[]int64{0}, // ctime
		[]int64{time.Now().Unix()},
	)
	if err != nil {
		log.Printf("[WA Indexer] IndexText %s: %v", chatJID, err)
		return
	}

	// Also keep the path FTS table up to date so keyword search works too.
	idx.engine.IndexPathsFTS([]string{path})
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// ChatPath returns the canonical search-result path for a WhatsApp chat.
func ChatPath(jid string) string {
	return whatsAppPathPrefix + jid
}

// IsWhatsAppPath reports whether a search result path refers to a WhatsApp chat.
func IsWhatsAppPath(path string) bool {
	return len(path) > len(whatsAppPathPrefix) &&
		path[:len(whatsAppPathPrefix)] == whatsAppPathPrefix
}

// JIDFromPath extracts the chat JID from a whatsapp:// path.
func JIDFromPath(path string) string {
	if !IsWhatsAppPath(path) {
		return ""
	}
	return path[len(whatsAppPathPrefix):]
}

// ChatDisplayName returns a human-readable label for a path, falling back to
// the JID if no chat name is stored.
func ChatDisplayName(store *Store, path string) string {
	jid := JIDFromPath(path)
	if jid == "" {
		return path
	}
	msgs, err := store.GetChatMessages(jid, 1)
	if err != nil || len(msgs) == 0 {
		return fmt.Sprintf("WhatsApp: %s", jid)
	}
	name := msgs[0].ChatName
	if name == "" {
		name = jid
	}
	return fmt.Sprintf("WhatsApp: %s", name)
}
