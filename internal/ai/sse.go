package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v5"
)

const (
	// sessionTimeout is how long a session lives without activity.
	sessionTimeout = 30 * time.Minute
	// sessionCleanupInterval is how often we sweep expired sessions.
	sessionCleanupInterval = 5 * time.Minute
)

// chatRequest is the JSON body for POST /api/chat.
type chatRequest struct {
	Content   string `json:"content"`
	Window    int    `json:"window"`
	Namespace string `json:"namespace"`
}

// sseSession holds conversation state for one client session.
type sseSession struct {
	mu       sync.Mutex // protects messages, cancel, done
	messages []Message
	cancel   context.CancelFunc
	done     chan struct{} // closed when current goroutine finishes
	lastSeen time.Time
}

// SSEHandler handles SSE streaming for the chat interface.
type SSEHandler struct {
	orchestrator *Orchestrator
	appCtx       context.Context
	sessions     sync.Map // map[string]*sseSession
}

// NewSSEHandler creates an SSE handler.
// The appCtx should be the application-level context that is cancelled on shutdown.
func NewSSEHandler(appCtx context.Context, orchestrator *Orchestrator) *SSEHandler {
	h := &SSEHandler{appCtx: appCtx, orchestrator: orchestrator}
	go h.cleanupLoop(appCtx)
	return h
}

// cleanupLoop periodically removes expired sessions.
func (h *SSEHandler) cleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(sessionCleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			h.sessions.Range(func(key, value any) bool {
				sess := value.(*sseSession)
				sess.mu.Lock()
				expired := now.Sub(sess.lastSeen) > sessionTimeout
				sess.mu.Unlock()
				if expired {
					h.sessions.Delete(key)
					slog.Debug("expired SSE session", "id", key)
				}
				return true
			})
		}
	}
}

// getOrCreateSession returns an existing session or creates a new one.
func (h *SSEHandler) getOrCreateSession(id string) *sseSession {
	if v, ok := h.sessions.Load(id); ok {
		sess := v.(*sseSession)
		sess.mu.Lock()
		sess.lastSeen = time.Now()
		sess.mu.Unlock()
		return sess
	}
	sess := &sseSession{
		messages: []Message{},
		lastSeen: time.Now(),
	}
	actual, _ := h.sessions.LoadOrStore(id, sess)
	return actual.(*sseSession)
}

// sessionID extracts the session ID from the request.
// Uses X-Session-ID header, falling back to a cookie.
func sessionID(c *echo.Context) string {
	if id := c.Request().Header.Get("X-Session-ID"); id != "" {
		return id
	}
	if cookie, err := c.Request().Cookie("fanout_session"); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	return ""
}

// StreamChat handles POST /api/chat — streams the AI response as SSE.
func (h *SSEHandler) StreamChat(c *echo.Context) error {
	if h.orchestrator == nil {
		return echo.NewHTTPError(503, "AI chat not configured")
	}

	var req chatRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(400, "invalid request body")
	}
	if strings.TrimSpace(req.Content) == "" {
		return echo.NewHTTPError(400, "empty message")
	}

	sid := sessionID(c)
	if sid == "" {
		return echo.NewHTTPError(400, "missing session ID (set X-Session-ID header)")
	}

	sess := h.getOrCreateSession(sid)

	// Cancel any in-progress request and wait for it to finish
	sess.mu.Lock()
	if sess.cancel != nil {
		sess.cancel()
	}
	done := sess.done
	sess.mu.Unlock()

	// Wait for previous goroutine to finish (outside lock to avoid deadlock).
	if done != nil {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			slog.Warn("timed out waiting for previous SSE request goroutine")
		}
	}

	sess.mu.Lock()

	ctx, cancel := context.WithTimeout(h.appCtx, 5*time.Minute)
	sess.cancel = cancel
	sess.done = make(chan struct{})
	doneCh := sess.done

	// Add user message to conversation
	sess.messages = append(sess.messages, UserMessage(req.Content))

	// Copy messages for the orchestrator (it may append to the slice)
	msgs := make([]Message, len(sess.messages))
	copy(msgs, sess.messages)

	sess.mu.Unlock()

	window := req.Window
	if window == 0 {
		window = 60
	}

	// Set SSE headers
	w := c.Response()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	// Detect client disconnect via the request context
	clientCtx := c.Request().Context()
	streamCtx, streamCancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-clientCtx.Done():
			streamCancel()
		case <-streamCtx.Done():
		}
	}()

	// writeMu serializes SSE writes (single goroutine, but be safe)
	var writeMu sync.Mutex
	send := func(event ClientEvent) error {
		if streamCtx.Err() != nil {
			return streamCtx.Err()
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		writeSSE(w, string(event.Type), event)
		return nil
	}

	// Run synchronously — SSE streams inline (not in a goroutine)
	// But we still need to handle the "done" channel for session lifecycle.
	updated, err := h.orchestrator.Run(streamCtx, msgs, window, req.Namespace, send)

	if err != nil && streamCtx.Err() == nil {
		slog.Error("orchestrator error", "err", err)
		// Ensure client gets a done event so UI doesn't stay stuck
		writeMu.Lock()
		writeSSE(w, string(CEDone), ClientEvent{Type: CEDone, ID: "error"})
		writeMu.Unlock()
	}

	// Signal completion
	streamCancel()
	cancel()
	close(doneCh)

	// Write back updated conversation (with assistant/tool messages added)
	sess.mu.Lock()
	if updated != nil {
		sess.messages = updated
	}
	trimConversation(&sess.messages)
	sess.mu.Unlock()

	return nil
}

// CancelChat handles POST /api/chat/cancel — cancels the in-flight request.
func (h *SSEHandler) CancelChat(c *echo.Context) error {
	sid := sessionID(c)
	if sid == "" {
		return c.NoContent(204)
	}

	if v, ok := h.sessions.Load(sid); ok {
		sess := v.(*sseSession)
		sess.mu.Lock()
		if sess.cancel != nil {
			sess.cancel()
		}
		sess.mu.Unlock()
	}

	return c.NoContent(204)
}

// ClearChat handles POST /api/chat/clear — resets conversation history.
func (h *SSEHandler) ClearChat(c *echo.Context) error {
	sid := sessionID(c)
	if sid == "" {
		return c.NoContent(204)
	}

	if v, ok := h.sessions.Load(sid); ok {
		sess := v.(*sseSession)
		sess.mu.Lock()
		if sess.cancel != nil {
			sess.cancel()
		}
		sess.messages = sess.messages[:0]
		sess.mu.Unlock()
	}

	return c.NoContent(204)
}

// writeSSE writes a single SSE event and flushes.
func writeSSE(w http.ResponseWriter, event string, data any) {
	b, err := json.Marshal(data)
	if err != nil {
		slog.Error("SSE marshal failed", "event", event, "err", err)
		b = []byte(`{"error":"internal serialization error"}`)
		event = "error"
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// trimConversation keeps the conversation manageable.
// Trims to at most maxMessages, cutting at a RoleUser boundary
// to avoid orphaning tool_use/tool_result pairs.
func trimConversation(messages *[]Message) {
	const maxMessages = 40

	if len(*messages) <= maxMessages {
		return
	}

	// Start from the target cut point and scan forward to find the first
	// RoleUser message, which is always a safe boundary.
	start := len(*messages) - maxMessages
	for start < len(*messages) {
		if (*messages)[start].Role == RoleUser {
			break
		}
		start++
	}

	// If we couldn't find a safe boundary, keep everything (shouldn't happen
	// in practice since conversations always start with a user message).
	if start >= len(*messages) {
		return
	}

	*messages = (*messages)[start:]
}
