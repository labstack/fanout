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
	"github.com/labstack/fanout/internal/service"
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
	svc          *service.Service
}

// NewWSHandler creates a WebSocket handler.
func NewWSHandler(orchestrator *Orchestrator, svc *service.Service) *WSHandler {
	return &WSHandler{orchestrator: orchestrator, svc: svc}
}

// Handle upgrades to WebSocket and manages the chat session.
func (h *WSHandler) Handle(c echo.Context) error {
	ws, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		return err
	}
	defer ws.Close()
	ws.SetReadLimit(64 * 1024) // 64KB max message

	session := &chatSession{
		ws:           ws,
		orchestrator: h.orchestrator,
		svc:          h.svc,
		messages:     []Message{},
	}

	return session.run()
}

type chatSession struct {
	ws           *websocket.Conn
	orchestrator *Orchestrator
	svc          *service.Service

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

		send := func(event ClientEvent) error {
			return s.send(event)
		}

		updated, tailCfg, err := s.orchestrator.Run(ctx, msgs, window, msg.Namespace, send)

		if err != nil && ctx.Err() == nil {
			slog.Error("orchestrator error", "err", err)
			// Ensure client gets a done event so UI doesn't stay stuck
			if sendErr := s.send(ClientEvent{Type: CEDone, ID: "error"}); sendErr != nil {
				slog.Warn("failed to send done-on-error to client", "err", sendErr)
			}
		}

		// Write back updated conversation (with assistant/tool messages added)
		s.mu.Lock()
		if updated != nil {
			s.messages = updated
		}
		s.trimConversation()
		s.mu.Unlock()

		// Start tailing if the orchestrator detected a tail tool call
		if tailCfg != nil && ctx.Err() == nil {
			s.runTail(ctx, tailCfg, send)
		}
	}()
}

// runTail polls for new log entries and streams them to the client.
// Stops on context cancellation, 2-minute timeout, or 30s of no new results.
func (s *chatSession) runTail(parent context.Context, cfg *TailConfig, send SendFunc) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()

	slog.Info("tail started", "service", cfg.Service, "namespace", cfg.Namespace, "since", cfg.Since)

	// Set Since to now if zero (initial batch had no logs)
	if cfg.Since.IsZero() {
		cfg.Since = time.Now().Add(-5 * time.Minute)
	}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	idleSince := time.Now()

	for {
		select {
		case <-ctx.Done():
			reason := "timeout"
			if parent.Err() != nil {
				reason = "cancel"
			}
			_ = s.send(ClientEvent{Type: CETailEnd, Content: reason})
			return
		case <-ticker.C:
			logs, err := s.svc.TailLogs(ctx, service.TailParams{
				Service:   cfg.Service,
				Pattern:   cfg.Pattern,
				Severity:  cfg.Severity,
				Namespace: cfg.Namespace,
				Since:     cfg.Since,
			})
			if err != nil {
				if ctx.Err() != nil {
					_ = s.send(ClientEvent{Type: CETailEnd, Content: "cancel"})
					return
				}
				slog.Warn("tail poll error", "err", err)
				continue
			}

			if len(logs) == 0 {
				if time.Since(idleSince) > 30*time.Second {
					slog.Info("tail stopped", "reason", "idle")
					_ = s.send(ClientEvent{Type: CETailEnd, Content: "idle"})
					return
				}
				continue
			}

			idleSince = time.Now()

			// Update Since to the latest log time
			if last := logs[len(logs)-1]; last.Time != "" {
				if t, err := time.Parse("2006-01-02T15:04:05Z", last.Time); err == nil {
					cfg.Since = t
				}
			}

			// Marshal and send entries
			type tailEntry struct {
				Time     string `json:"time"`
				Severity string `json:"severity"`
				Body     string `json:"body"`
				Service  string `json:"service"`
				TraceID  string `json:"trace_id,omitempty"`
			}
			entries := make([]tailEntry, len(logs))
			for i, l := range logs {
				entries[i] = tailEntry{
					Time:     l.Time,
					Severity: l.Severity,
					Body:     l.Body,
					Service:  l.Service,
					TraceID:  l.TraceID,
				}
			}
			data, err := json.Marshal(map[string]any{"entries": entries})
			if err != nil {
				slog.Warn("tail marshal error", "err", err)
				continue
			}
			if err := send(ClientEvent{Type: CETail, Content: string(data)}); err != nil {
				slog.Debug("tail send error, stopping", "err", err)
				return
			}
		}
	}
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
	if err := s.send(ClientEvent{Type: CEError, Error: msg}); err != nil {
		slog.Warn("failed to send error to client", "msg", msg, "err", err)
	}
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
