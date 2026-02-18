package ai

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true // No origin header (non-browser clients)
		}
		// Accept if origin matches the Host header
		host := r.Host
		return origin == "http://"+host || origin == "https://"+host
	},
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
}

// clientMessage is received from the browser.
type clientMessage struct {
	Type      string `json:"type"`      // message, cancel
	Content   string `json:"content"`   // user text
	Window    int    `json:"window"`    // time window in minutes
	Namespace string `json:"namespace"` // namespace filter
}

// WSHandler handles WebSocket connections for the chat interface.
type WSHandler struct {
	orchestrator *Orchestrator
}

// NewWSHandler creates a WebSocket handler.
func NewWSHandler(orchestrator *Orchestrator) *WSHandler {
	return &WSHandler{orchestrator: orchestrator}
}

// Handle upgrades to WebSocket and manages the chat session.
func (h *WSHandler) Handle(c echo.Context) error {
	ws, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		return err
	}
	defer ws.Close()

	session := &chatSession{
		ws:           ws,
		orchestrator: h.orchestrator,
		messages:     []Message{},
	}

	return session.run()
}

type chatSession struct {
	ws           *websocket.Conn
	orchestrator *Orchestrator

	mu       sync.Mutex // protects messages, cancel, done
	messages []Message
	cancel   context.CancelFunc
	done     chan struct{} // closed when current goroutine finishes

	writeMu sync.Mutex // protects WebSocket writes
}

func (s *chatSession) run() error {
	for {
		_, raw, err := s.ws.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				slog.Warn("websocket read error", "err", err)
			}
			// Cancel any in-flight request on disconnect
			s.mu.Lock()
			if s.cancel != nil {
				s.cancel()
			}
			s.mu.Unlock()
			return nil
		}

		var msg clientMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			s.sendError("invalid message format")
			continue
		}

		switch msg.Type {
		case "message":
			if strings.TrimSpace(msg.Content) == "" {
				s.sendError("empty message")
				continue
			}
			s.handleMessage(msg)
		case "cancel":
			s.mu.Lock()
			if s.cancel != nil {
				s.cancel()
			}
			s.mu.Unlock()
		default:
			slog.Debug("unknown client message type", "type", msg.Type)
		}
	}
}

func (s *chatSession) handleMessage(msg clientMessage) {
	s.mu.Lock()

	// Cancel any in-progress request and wait for it to finish
	if s.cancel != nil {
		s.cancel()
	}
	done := s.done
	s.mu.Unlock()

	// Wait for previous goroutine to finish (outside lock to avoid deadlock)
	if done != nil {
		<-done
	}

	s.mu.Lock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	s.cancel = cancel
	s.done = make(chan struct{})
	doneCh := s.done

	// Add user message to conversation
	s.messages = append(s.messages, UserMessage(msg.Content))

	// Copy messages for the goroutine (orchestrator may append to the slice)
	msgs := make([]Message, len(s.messages))
	copy(msgs, s.messages)

	s.mu.Unlock()

	window := msg.Window
	if window == 0 {
		window = 60
	}

	// Run orchestrator in goroutine so we can receive cancel
	go func() {
		defer close(doneCh)
		defer cancel()

		updated, err := s.orchestrator.Run(ctx, msgs, window, msg.Namespace, func(event ClientEvent) error {
			return s.send(event)
		})

		if err != nil && ctx.Err() == nil {
			slog.Error("orchestrator error", "err", err)
		}

		// Write back updated conversation (with assistant/tool messages added)
		s.mu.Lock()
		if updated != nil {
			s.messages = updated
		}
		s.trimConversation()
		s.mu.Unlock()
	}()
}

func (s *chatSession) send(event ClientEvent) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	data, err := json.Marshal(event)
	if err != nil {
		return err
	}

	return s.ws.WriteMessage(websocket.TextMessage, data)
}

func (s *chatSession) sendError(msg string) {
	_ = s.send(ClientEvent{Type: CEError, Error: msg})
}

// trimConversation keeps the conversation manageable.
// Trims to at most maxMessages, cutting at a RoleUser boundary
// to avoid orphaning tool_use/tool_result pairs.
// Must be called with s.mu held.
func (s *chatSession) trimConversation() {
	const maxMessages = 40

	if len(s.messages) <= maxMessages {
		return
	}

	// Start from the target cut point and scan forward to find the first
	// RoleUser message, which is always a safe boundary.
	start := len(s.messages) - maxMessages
	for start < len(s.messages) {
		if s.messages[start].Role == RoleUser {
			break
		}
		start++
	}

	// If we couldn't find a safe boundary, keep everything (shouldn't happen
	// in practice since conversations always start with a user message).
	if start >= len(s.messages) {
		return
	}

	s.messages = s.messages[start:]
}
