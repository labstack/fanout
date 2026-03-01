package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// AnthropicProvider streams responses from the Anthropic Messages API.
type AnthropicProvider struct {
	apiKey  string
	model   string
	baseURL string
	client  *http.Client
}

// NewAnthropicProvider creates a provider for the Anthropic Messages API.
func NewAnthropicProvider(apiKey, model, baseURL string) *AnthropicProvider {
	if model == "" {
		model = "claude-haiku-4-5"
	}
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	return &AnthropicProvider{
		apiKey:  apiKey,
		model:   model,
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: 10 * time.Minute},
	}
}

func (p *AnthropicProvider) Stream(ctx context.Context, params StreamParams, cb StreamCallback) error {
	body := p.buildRequest(params)

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/v1/messages", bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("anthropic-beta", "prompt-caching-2024-07-31")

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &APIError{StatusCode: resp.StatusCode, Body: string(b)}
	}

	return p.parseSSE(resp.Body, cb)
}

func (p *AnthropicProvider) buildRequest(params StreamParams) map[string]any {
	messages := make([]map[string]any, 0, len(params.Messages))

	for _, m := range params.Messages {
		switch m.Role {
		case RoleUser:
			messages = append(messages, map[string]any{
				"role":    "user",
				"content": m.Content,
			})
		case RoleAssistant:
			if len(m.ToolCalls) > 0 {
				// Assistant message with tool uses
				content := make([]map[string]any, 0)
				if m.Content != "" {
					content = append(content, map[string]any{
						"type": "text",
						"text": m.Content,
					})
				}
				for _, tc := range m.ToolCalls {
					var input any
					if err := json.Unmarshal([]byte(tc.Input), &input); err != nil {
						slog.Error("corrupt tool call input, skipping tool_use block",
							"tool", tc.Name, "id", tc.ID, "input_len", len(tc.Input), "err", err)
						content = append(content, map[string]any{
							"type": "text",
							"text": fmt.Sprintf("[tool_use %s skipped: corrupt input]", tc.Name),
						})
						continue
					}
					content = append(content, map[string]any{
						"type":  "tool_use",
						"id":    tc.ID,
						"name":  tc.Name,
						"input": input,
					})
				}
				messages = append(messages, map[string]any{
					"role":    "assistant",
					"content": content,
				})
			} else {
				messages = append(messages, map[string]any{
					"role":    "assistant",
					"content": m.Content,
				})
			}
		case RoleTool:
			if m.ToolResult != nil {
				messages = append(messages, map[string]any{
					"role": "user",
					"content": []map[string]any{{
						"type":        "tool_result",
						"tool_use_id": m.ToolResult.ToolCallID,
						"content":     m.ToolResult.Content,
						"is_error":    m.ToolResult.IsError,
					}},
				})
			}
		}
	}

	body := map[string]any{
		"model":      p.model,
		"max_tokens": params.MaxTokens,
		"stream":     true,
		"messages":   messages,
	}

	if len(params.SystemBlocks) > 0 {
		blocks := make([]map[string]any, len(params.SystemBlocks))
		for i, b := range params.SystemBlocks {
			block := map[string]any{
				"type": "text",
				"text": b.Text,
			}
			if b.CacheControl != "" {
				block["cache_control"] = map[string]string{"type": b.CacheControl}
			}
			blocks[i] = block
		}
		body["system"] = blocks
	} else if params.System != "" {
		body["system"] = params.System
	}

	if len(params.Tools) > 0 {
		tools := make([]map[string]any, len(params.Tools))
		for i, t := range params.Tools {
			tool := map[string]any{
				"name":         t.Name,
				"description":  t.Description,
				"input_schema": t.InputSchema,
			}
			// Mark last tool with cache_control so the entire tools block is cached
			if i == len(params.Tools)-1 {
				tool["cache_control"] = map[string]string{"type": "ephemeral"}
			}
			tools[i] = tool
		}
		body["tools"] = tools
		if params.ToolChoice != nil {
			body["tool_choice"] = map[string]string{"type": "tool", "name": params.ToolChoice.Name}
		}
	}

	return body
}

func (p *AnthropicProvider) parseSSE(r io.Reader, cb StreamCallback) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB buffer

	// State for accumulating tool use input
	var currentToolID, currentToolName string
	var toolInputBuf strings.Builder
	var gotStop bool
	var parseErrors int

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := line[6:]
		if data == "[DONE]" {
			break
		}

		var event struct {
			Type  string `json:"type"`
			Index int    `json:"index"`
			// content_block_start
			ContentBlock struct {
				Type  string `json:"type"`
				ID    string `json:"id"`
				Name  string `json:"name"`
				Input any    `json:"input"`
			} `json:"content_block"`
			// content_block_delta
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
		}

		if err := json.Unmarshal([]byte(data), &event); err != nil {
			parseErrors++
			if parseErrors >= 5 {
				slog.Error("too many SSE parse failures", "err", err, "consecutive", parseErrors)
				return fmt.Errorf("SSE parse: %d consecutive failures: %w", parseErrors, err)
			}
			slog.Warn("failed to parse SSE event", "err", err)
			continue
		}
		parseErrors = 0 // reset on successful parse

		switch event.Type {
		case "content_block_start":
			if event.ContentBlock.Type == "tool_use" {
				currentToolID = event.ContentBlock.ID
				currentToolName = event.ContentBlock.Name
				toolInputBuf.Reset()
			}

		case "content_block_delta":
			switch event.Delta.Type {
			case "text_delta":
				if err := cb(StreamEvent{Type: EventText, Delta: event.Delta.Text}); err != nil {
					return err
				}
			case "input_json_delta":
				toolInputBuf.WriteString(event.Delta.PartialJSON)
			}

		case "content_block_stop":
			if currentToolID != "" {
				inputStr := toolInputBuf.String()
				if inputStr == "" {
					inputStr = "{}"
				}
				if err := cb(StreamEvent{
					Type: EventToolUse,
					ToolCall: &ToolCall{
						ID:    currentToolID,
						Name:  currentToolName,
						Input: inputStr,
					},
				}); err != nil {
					return err
				}
				currentToolID = ""
				currentToolName = ""
				toolInputBuf.Reset()
			}

		case "message_delta":
			// stop_reason is in the delta object
			gotStop = true
			if err := cb(StreamEvent{Type: EventStop, StopReason: event.Delta.StopReason}); err != nil {
				return err
			}

		case "message_stop":
			// Final event, stream complete

		case "error":
			var errEvent struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(data), &errEvent); err != nil {
				slog.Error("failed to parse Anthropic error event", "err", err, "raw", data)
				return cb(StreamEvent{Type: EventError, Error: "unparseable error from API"})
			}
			return cb(StreamEvent{Type: EventError, Error: errEvent.Error.Message})
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("anthropic SSE read: %w", err)
	}

	// If stream ended without a stop event, report as error rather than
	// fabricating a successful end_turn (which hides truncated responses).
	if !gotStop {
		slog.Error("anthropic SSE stream ended without message_delta — response may be incomplete")
		return cb(StreamEvent{Type: EventError, Error: "Response incomplete — stream ended unexpectedly"})
	}

	return nil
}
