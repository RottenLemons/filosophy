package whatsapp

import (
	"log"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types/events"
)

// OnNewMessage is called after a message is successfully inserted.
// The engine integration (subtask 5) sets this to trigger re-indexing.
var OnNewMessage func(chatJID string)

// handleEvent is registered as the whatsmeow event handler.
func (c *Client) handleEvent(evt interface{}) {
	switch v := evt.(type) {

	case *events.Message:
		c.handleMessage(v)

	case *events.HistorySync:
		c.handleHistorySync(v)

	case *events.Connected:
		log.Println("[WhatsApp] Connected")
		c.setStatus(StatusConnected)

	case *events.Disconnected:
		log.Println("[WhatsApp] Disconnected")
		c.setStatus(StatusDisconnected)

	case *events.LoggedOut:
		log.Println("[WhatsApp] Logged out")
		c.setStatus(StatusLoggedOut)
	}
}

// ── Real-time message ─────────────────────────────────────────────────────────

func (c *Client) handleMessage(evt *events.Message) {
	m := extractMessage(evt.Info.ID, evt.Info.Chat.String(), evt.Info.Sender.String(),
		evt.Info.PushName, evt.Info.Timestamp, evt.Info.IsGroup, evt.Message)
	if m == nil {
		return
	}

	// Ensure the chat row exists.
	chatName := evt.Info.PushName
	if evt.Info.IsGroup {
		chatName = evt.Info.Chat.User
	}
	if err := c.store.UpsertChat(m.ChatJID, chatName, m.IsGroup); err != nil {
		log.Printf("[WhatsApp] UpsertChat error: %v", err)
		return
	}

	inserted, err := c.store.InsertMessage(*m)
	if err != nil {
		log.Printf("[WhatsApp] InsertMessage error: %v", err)
		return
	}
	if inserted && OnNewMessage != nil {
		OnNewMessage(m.ChatJID)
	}
}

// ── History sync ──────────────────────────────────────────────────────────────

func (c *Client) handleHistorySync(evt *events.HistorySync) {
	syncType := evt.Data.GetSyncType()

	// We care about the initial bootstrap and recent-message sync.
	// PUSH_NAME is metadata only, skip it.
	if syncType == waHistorySync.HistorySync_PUSH_NAME {
		return
	}

	log.Printf("[WhatsApp] HistorySync %s — %d conversation(s)",
		syncType, len(evt.Data.GetConversations()))

	for _, conv := range evt.Data.GetConversations() {
		chatJID := conv.GetID()
		chatName := conv.GetName()
		isGroup := strings.Contains(chatJID, "@g.us")

		if err := c.store.UpsertChat(chatJID, chatName, isGroup); err != nil {
			log.Printf("[WhatsApp] UpsertChat error for %s: %v", chatJID, err)
			continue
		}

		var batch []Message
		for _, hsMsg := range conv.GetMessages() {
			webMsg := hsMsg.GetMessage()
			if webMsg == nil {
				continue
			}
			key := webMsg.GetKey()
			msgID := key.GetID()
			if msgID == "" {
				continue
			}

			// Determine sender
			sender := key.GetParticipant()
			if sender == "" {
				if key.GetFromMe() {
					sender = "me"
				} else {
					sender = chatJID
				}
			}
			senderName := webMsg.GetPushName()

			ts := time.Unix(int64(webMsg.GetMessageTimestamp()), 0)

			m := extractMessage(msgID, chatJID, sender, senderName, ts, isGroup, webMsg.GetMessage())
			if m == nil {
				continue
			}
			batch = append(batch, *m)
		}

		if len(batch) == 0 {
			continue
		}

		n, err := c.store.BulkInsert(batch)
		if err != nil {
			log.Printf("[WhatsApp] BulkInsert error for %s: %v", chatJID, err)
			continue
		}
		if n > 0 && OnNewMessage != nil {
			OnNewMessage(chatJID)
		}
		log.Printf("[WhatsApp] HistorySync %s: inserted %d/%d messages", chatJID, n, len(batch))
	}
}

// ── Message text extraction ───────────────────────────────────────────────────

// extractMessage pulls text and media metadata out of a whatsmeow proto message.
// Returns nil if the message has no indexable content (reactions, stickers, etc.).
func extractMessage(
	msgID, chatJID, sender, senderName string,
	ts time.Time,
	isGroup bool,
	msg interface{ GetConversation() string },
) *Message {
	if msg == nil {
		return nil
	}

	type fullMsg interface {
		GetConversation() string
		GetExtendedTextMessage() interface{ GetText() string }
		GetImageMessage() interface {
			GetCaption() string
		}
		GetVideoMessage() interface {
			GetCaption() string
		}
		GetDocumentMessage() interface {
			GetCaption() string
			GetFileName() string
		}
	}

	text := strings.TrimSpace(msg.GetConversation())
	mediaType := ""
	mediaName := ""

	if fm, ok := msg.(fullMsg); ok {
		if text == "" {
			if etm := fm.GetExtendedTextMessage(); etm != nil {
				text = strings.TrimSpace(etm.GetText())
			}
		}
		if text == "" {
			if im := fm.GetImageMessage(); im != nil {
				text = strings.TrimSpace(im.GetCaption())
				mediaType = "image"
			}
		}
		if text == "" {
			if vm := fm.GetVideoMessage(); vm != nil {
				text = strings.TrimSpace(vm.GetCaption())
				mediaType = "video"
			}
		}
		if dm := fm.GetDocumentMessage(); dm != nil {
			if text == "" {
				text = strings.TrimSpace(dm.GetCaption())
			}
			mediaType = "document"
			mediaName = dm.GetFileName()
		}
	}

	// Skip messages with nothing indexable.
	if text == "" && mediaType == "" && mediaName == "" {
		return nil
	}

	return &Message{
		MessageID:  msgID,
		ChatJID:    chatJID,
		Sender:     sender,
		SenderName: senderName,
		Text:       text,
		Timestamp:  ts,
		IsGroup:    isGroup,
		MediaType:  mediaType,
		MediaName:  mediaName,
	}
}
