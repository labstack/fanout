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
	"sort"
	"strings"
	"time"
)

// OpenAIProvider streams responses from OpenAI-compatible Chat Completions API.
type OpenAIProvider struct {
	apiKey  string
	model   string
	baseURL string
	client  *http.Client
}

// NewOpenAIProvider creates a provider for OpenAI-compatible APIs.
func NewOpenAIProvider(apiKey, model, baseURL string) *OpenAIProvider {
	if model == "" {
		model = "gpt-4o"
	}
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	return &OpenAIProvider{
		apiKey:  apiKey,
		model:   model,
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: 10 * time.Minute},
	}
}

func (p *OpenAIProvider) Stream(ctx context.Context, params StreamParams, cb StreamCallback) error {
	body := p.buildRequest(params)

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/v1/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

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

func (p *OpenAIProvider) buildRequest(params StreamParams) map[string]any {
	messages := make([]map[string]any, 0, len(params.Messages)+1)

	// System message
	if len(params.SystemBlocks) > 0 {
		var sb strings.Builder
		for i, b := range params.SystemBlocks {
			if i > 0 {
				sb.WriteString("\n\n")
			}
			sb.WriteString(b.Text)
		}
		messages = append(messages, map[string]any{
			"role":    "system",
			"content": sb.String(),
		})
	} else if params.System != "" {
		messages = append(messages, map[string]any{
			"role":    "system",
			"content": params.System,
		})
	}

	for _, m := range params.Messages {
		switch m.Role {
		case RoleUser:
			messages = append(messages, map[string]any{
				"role":    "user",
				"content": m.Content,
			})
		case RoleAssistant:
			msg := map[string]any{
				"role": "assistant",
			}
			if m.Content != "" {
				msg["content"] = m.Content
			}
			if len(m.ToolCalls) > 0 {
				tcs := make([]map[string]any, len(m.ToolCalls))
				for i, tc := range m.ToolCalls {
					tcs[i] = map[string]any{
						"id":   tc.ID,
						"type": "function",
						"function": map[string]any{
							"name":      tc.Name,
							"arguments": tc.Input,
						},
					}
				}
				msg["tool_calls"] = tcs
			}
			messages = append(messages, msg)
		case RoleTool:
			if m.ToolResult != nil {
				messages = append(messages, map[string]any{
					"role":         "tool",
					"tool_call_id": m.ToolResult.ToolCallID,
					"content":      m.ToolResult.Content,
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

	if len(params.Tools) > 0 {
		tools := make([]map[string]any, len(params.Tools))
		for i, t := range params.Tools {
			tools[i] = map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  t.InputSchema,
				},
			}
		}
		body["tools"] = tools
	}

	return body
}

func (p *OpenAIProvider) parseSSE(r io.Reader, cb StreamCallback) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	// Accumulate tool call arguments per index
	type toolCallAcc struct {
		id   string
		name string
		args strings.Builder
	}
	toolCalls := make(map[int]*toolCallAcc)
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

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
				Type    string `json:"type"`
			} `json:"error"`
		}

		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			parseErrors++
			if parseErrors >= 5 {
				slog.Error("too many SSE parse failures", "err", err, "consecutive", parseErrors)
				return fmt.Errorf("SSE parse: %d consecutive failures: %w", parseErrors, err)
			}
			slog.Warn("failed to parse SSE event", "err", err)
			continue
		}
		parseErrors = 0 // reset on successful parse

		// Handle mid-stream error events
		if chunk.Error != nil {
			errMsg := chunk.Error.Message
			if errMsg == "" {
				errMsg = "unknown OpenAI stream error"
			}
			return cb(StreamEvent{Type: EventError, Error: errMsg})
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		choice := chunk.Choices[0]

		// Text content
		if choice.Delta.Content != "" {
			if err := cb(StreamEvent{Type: EventText, Delta: choice.Delta.Content}); err != nil {
				return err
			}
		}

		// Tool calls (streamed incrementally)
		for _, tc := range choice.Delta.ToolCalls {
			acc, exists := toolCalls[tc.Index]
			if !exists {
				acc = &toolCallAcc{}
				toolCalls[tc.Index] = acc
			}
			if tc.ID != "" {
				acc.id = tc.ID
			}
			if tc.Function.Name != "" {
				acc.name = tc.Function.Name
			}
			acc.args.WriteString(tc.Function.Arguments)
		}

		// Finish reason
		if choice.FinishReason != nil {
			switch *choice.FinishReason {
			case "tool_calls":
				// Emit all accumulated tool calls in index order
				indices := make([]int, 0, len(toolCalls))
				for idx := range toolCalls {
					indices = append(indices, idx)
				}
				sort.Ints(indices)
				for _, idx := range indices {
					acc := toolCalls[idx]
					inputStr := acc.args.String()
					if inputStr == "" {
						inputStr = "{}"
					}
					if err := cb(StreamEvent{
						Type: EventToolUse,
						ToolCall: &ToolCall{
							ID:    acc.id,
							Name:  acc.name,
							Input: inputStr,
						},
					}); err != nil {
						return err
					}
				}
				toolCalls = make(map[int]*toolCallAcc)
				gotStop = true
				if err := cb(StreamEvent{Type: EventStop, StopReason: "tool_use"}); err != nil {
					return err
				}
			case "stop":
				gotStop = true
				if err := cb(StreamEvent{Type: EventStop, StopReason: "end_turn"}); err != nil {
					return err
				}
			case "length":
				slog.Warn("openai response truncated (max_tokens reached)")
				gotStop = true
				if err := cb(StreamEvent{Type: EventStop, StopReason: "end_turn"}); err != nil {
					return err
				}
			case "content_filter":
				gotStop = true
				return cb(StreamEvent{Type: EventError, Error: "Response blocked by content filter"})
			default:
				slog.Warn("unrecognized finish_reason from OpenAI", "reason", *choice.FinishReason)
				gotStop = true
				if err := cb(StreamEvent{Type: EventStop, StopReason: "end_turn"}); err != nil {
					return err
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("openai SSE read: %w", err)
	}

	// If stream ended without a stop event, report as error rather than
	// fabricating a successful end_turn (which hides truncated responses).
	if !gotStop {
		slog.Error("openai SSE stream ended without finish_reason — response may be incomplete")
		return cb(StreamEvent{Type: EventError, Error: "Response incomplete — stream ended unexpectedly"})
	}

	return nil
}
