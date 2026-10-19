package mcp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// Legacy sessions expire after 10 idle minutes; each user/OAuth-client pair
	// may keep eight live sessions. Modern requests never enter this accounting.
	legacySessionTimeout = 10 * time.Minute
	legacySessionLimit   = 8
)

// ReadHTTPMessage preserves the body for the SDK and excludes JSON-RPC batches.
func ReadHTTPMessage(r *http.Request) (jsonrpc.Message, error) {
	if r.Body == nil {
		return nil, io.EOF
	}
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, mcp.DefaultMaxRequestBodyBytes))
	if err != nil {
		return nil, err
	}
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(body))
	return jsonrpc.DecodeMessage(body)
}

func WriteHTTPMessageError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		status = http.StatusRequestEntityTooLarge
	}
	http.Error(w, http.StatusText(status), status)
}

func limitLegacySessions(server *mcp.Server) mcp.Middleware {
	type principal struct{ user, client string }
	var mu sync.Mutex
	sessions := make(map[*mcp.ServerSession]principal)
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != "initialize" {
				return next(ctx, method, req)
			}
			session, ok := req.GetSession().(*mcp.ServerSession)
			extra := req.GetExtra()
			if !ok || session == nil || session.ID() == "" || extra == nil || extra.TokenInfo == nil {
				return next(ctx, method, req)
			}
			info := extra.TokenInfo
			client, _ := info.Extra["client_id"].(string)
			key := principal{info.UserID, client}
			mu.Lock()
			defer mu.Unlock()
			live := make(map[*mcp.ServerSession]bool)
			count := 0
			for session := range server.Sessions() {
				live[session] = true
				if owner, exists := sessions[session]; exists && owner == key {
					count++
				}
			}
			for session := range sessions {
				if !live[session] {
					delete(sessions, session)
				}
			}
			if count >= legacySessionLimit {
				return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidRequest, Message: "legacy session limit exceeded"}
			}
			result, err := next(ctx, method, req)
			if err == nil {
				sessions[session] = key
			}
			return result, err
		}
	}
}
