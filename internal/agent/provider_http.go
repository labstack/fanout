package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

func NewProvider(kind, apiKey, model, baseURL string) (Provider, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", "anthropic":
		if model == "" {
			model = "claude-sonnet-5-5"
		}
		if baseURL == "" {
			baseURL = "https://api.anthropic.com"
		}
		return &anthropicProvider{apiKey: apiKey, model: model, baseURL: strings.TrimRight(baseURL, "/"), client: modelHTTPClient()}, nil
	case "openai":
		if model == "" {
			model = "gpt-6.1-sol"
		}
		if baseURL == "" {
			baseURL = "https://api.openai.com"
		}
		return &openAIProvider{apiKey: apiKey, model: model, baseURL: strings.TrimRight(baseURL, "/"), client: modelHTTPClient()}, nil
	default:
		return nil, fmt.Errorf("unsupported AI provider %q", kind)
	}
}

func modelHTTPClient() *http.Client { return &http.Client{Timeout: 10 * time.Minute} }

// openAIProvider expects a reasoning model (default: gpt-6.1-sol). Stateless
// Responses requests include reasoning.encrypted_content so complete output,
// including encrypted reasoning and assistant phase, can be replayed within a run.
type openAIProvider struct {
	apiKey, model, baseURL string
	client                 *http.Client
}

func (p *openAIProvider) Stream(ctx context.Context, params StreamParams, cb func(StreamEvent) error) error {
	input := make([]any, 0, len(params.Messages))
	// A function call is replayable only after its result exists. Incomplete
	// responses and aborted tool loops must never leave orphan calls in input.
	results := make(map[string]bool)
	for _, message := range params.Messages {
		if message.Role == RoleTool && message.ToolResult != nil {
			results[message.ToolResult.ToolCallID] = true
		}
	}
	for _, message := range params.Messages {
		switch message.Role {
		case RoleUser:
			input = append(input, map[string]any{"role": "user", "content": []map[string]any{{"type": "input_text", "text": message.Content}}})
		case RoleAssistant:
			if len(message.ProviderItems) > 0 {
				for _, raw := range message.ProviderItems {
					var item struct {
						Type             string `json:"type"`
						CallID           string `json:"call_id"`
						EncryptedContent string `json:"encrypted_content"`
					}
					if err := json.Unmarshal(raw, &item); err != nil {
						return fmt.Errorf("decode OpenAI replay item: %w", err)
					}
					if item.Type == "function_call" && !results[item.CallID] || item.Type == "reasoning" && item.EncryptedContent == "" {
						continue
					}
					input = append(input, raw)
				}
				continue
			}
			if message.Content != "" {
				input = append(input, map[string]any{"role": "assistant", "content": []map[string]any{{"type": "output_text", "text": message.Content}}})
			}
			for _, call := range message.ToolCalls {
				if !results[call.ID] {
					continue
				}
				input = append(input, map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Name, "arguments": call.Input})
			}
		case RoleTool:
			if message.ToolResult != nil {
				input = append(input, map[string]any{"type": "function_call_output", "call_id": message.ToolResult.ToolCallID, "output": message.ToolResult.Content})
			}
		}
	}
	body := map[string]any{
		"model": p.model, "stream": true, "store": false,
		"include": []string{"reasoning.encrypted_content"}, "instructions": params.System,
		"input": input, "max_output_tokens": params.MaxTokens,
	}
	if len(params.Tools) > 0 {
		tools := make([]map[string]any, len(params.Tools))
		for i, tool := range params.Tools {
			tools[i] = map[string]any{"type": "function", "name": tool.Name, "description": tool.Description, "parameters": tool.InputSchema, "strict": false}
		}
		body["tools"] = tools
	}
	response, err := postModel(ctx, p.client, p.baseURL+"/v1/responses", p.apiKey, "openai", body)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusBadRequest && strings.Contains(strings.ToLower(apiErr.Body), "encrypted") {
			return fmt.Errorf("OpenAI configuration error: ai.model must select a reasoning model supporting reasoning.encrypted_content (default gpt-6.1-sol): %w", err)
		}
		return err
	}
	defer response.Close()
	return parseOpenAI(response, cb)
}

func parseOpenAI(reader io.Reader, cb func(StreamEvent) error) error {
	type outputItem struct {
		Type             string `json:"type"`
		Status           string `json:"status"`
		CallID           string `json:"call_id"`
		Name             string `json:"name"`
		Arguments        string `json:"arguments"`
		EncryptedContent string `json:"encrypted_content"`
	}
	type accumulator struct {
		call       ToolCall
		unfinished bool
	}
	calls := map[int]*accumulator{}
	items := map[int]json.RawMessage{}
	callAt := func(index int) *accumulator {
		if calls[index] == nil {
			calls[index] = &accumulator{}
		}
		return calls[index]
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var header struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(data), &header); err != nil {
			return fmt.Errorf("decode OpenAI stream type: %w", err)
		}
		switch header.Type {
		case "response.output_text.delta", "response.refusal.delta", "response.output_item.added", "response.output_item.done", "response.function_call_arguments.delta", "response.function_call_arguments.done", "response.completed", "response.incomplete", "response.failed", "error":
		default:
			continue
		}
		var event struct {
			Type        string          `json:"type"`
			OutputIndex int             `json:"output_index"`
			Delta       string          `json:"delta"`
			Arguments   string          `json:"arguments"`
			Item        json.RawMessage `json:"item"`
			Message     string          `json:"message"`
			Error       *struct {
				Message string `json:"message"`
			} `json:"error"`
			Response struct {
				Model  string            `json:"model"`
				Output []json.RawMessage `json:"output"`
				Usage  *struct {
					InputTokens        int `json:"input_tokens"`
					OutputTokens       int `json:"output_tokens"`
					InputTokensDetails struct {
						CachedTokens int `json:"cached_tokens"`
					} `json:"input_tokens_details"`
					OutputTokensDetails struct {
						ReasoningTokens int `json:"reasoning_tokens"`
					} `json:"output_tokens_details"`
				} `json:"usage"`
				IncompleteDetails struct {
					Reason string `json:"reason"`
				} `json:"incomplete_details"`
				Error *struct {
					Message string `json:"message"`
				} `json:"error"`
			} `json:"response"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return fmt.Errorf("decode OpenAI stream: %w", err)
		}
		switch event.Type {
		case "response.output_text.delta", "response.refusal.delta":
			if event.Delta != "" {
				if err := cb(StreamEvent{Type: EventText, Delta: event.Delta}); err != nil {
					return err
				}
			}
		case "response.output_item.added", "response.output_item.done":
			var item outputItem
			if err := json.Unmarshal(event.Item, &item); err != nil {
				return fmt.Errorf("decode OpenAI output item: %w", err)
			}
			if event.Type == "response.output_item.done" {
				items[event.OutputIndex] = event.Item
			}
			if item.Type == "function_call" {
				call := callAt(event.OutputIndex)
				call.call.ID, call.call.Name = item.CallID, item.Name
				if event.Type == "response.output_item.done" {
					call.call.Input = item.Arguments
					call.unfinished = item.Status == "incomplete" || item.Status == "in_progress"
				} else if item.Arguments != "" {
					call.call.Input = item.Arguments
				}
			}
		case "response.function_call_arguments.delta":
			callAt(event.OutputIndex).call.Input += event.Delta
		case "response.function_call_arguments.done":
			call := callAt(event.OutputIndex)
			call.call.Input = event.Arguments
		case "response.completed", "response.incomplete":
			stop := StreamEvent{Type: EventStop, StopReason: "end_turn", Model: event.Response.Model}
			if event.Response.Usage != nil {
				usage := event.Response.Usage
				stop.Usage = &TokenUsage{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, ReasoningTokens: usage.OutputTokensDetails.ReasoningTokens, CacheReadTokens: usage.InputTokensDetails.CachedTokens}
				// Preserve reported counts even if decoding terminal output fails
				// or a later tool callback aborts this call.
				if err := cb(StreamEvent{Type: EventUsage, Usage: stop.Usage, Model: event.Response.Model}); err != nil {
					return err
				}
			}
			// The terminal output fills gaps when item-done events are absent;
			// retain item-done bytes verbatim when both are present.
			for index, raw := range event.Response.Output {
				if _, exists := items[index]; !exists {
					items[index] = raw
				}
				var item outputItem
				if err := json.Unmarshal(raw, &item); err != nil {
					return fmt.Errorf("decode OpenAI terminal output item: %w", err)
				}
				if item.Type == "function_call" {
					call := callAt(index)
					call.call = ToolCall{ID: item.CallID, Name: item.Name, Input: item.Arguments}
					call.unfinished = item.Status == "incomplete" || item.Status == "in_progress"
				}
			}
			indices := make([]int, 0, len(calls))
			for index, call := range calls {
				if event.Type == "response.completed" && !call.unfinished && call.call.ID != "" && call.call.Name != "" {
					indices = append(indices, index)
				}
			}
			sort.Ints(indices)
			emitted := make(map[int]bool, len(indices))
			for _, index := range indices {
				call := calls[index].call
				if call.Input == "" {
					call.Input = "{}"
				}
				if err := cb(StreamEvent{Type: EventToolUse, ToolCall: &call}); err != nil {
					return err
				}
				stop.StopReason = "tool_calls"
				emitted[index] = true
			}
			indices = indices[:0]
			for index := range items {
				indices = append(indices, index)
			}
			sort.Ints(indices)
			for _, index := range indices {
				var item outputItem
				if err := json.Unmarshal(items[index], &item); err != nil {
					return fmt.Errorf("decode OpenAI retained output item: %w", err)
				}
				if item.Type == "reasoning" && item.EncryptedContent == "" || item.Type == "function_call" && !emitted[index] {
					continue
				}
				stop.ProviderItems = append(stop.ProviderItems, items[index])
			}
			if event.Type == "response.incomplete" {
				stop.StopReason = event.Response.IncompleteDetails.Reason
				if stop.StopReason == "max_output_tokens" {
					stop.StopReason = "length"
				} else if stop.StopReason != "content_filter" {
					stop.StopReason = "incomplete"
				}
			}
			return cb(stop)
		case "response.failed", "error":
			message := "OpenAI stream error"
			if event.Response.Error != nil && event.Response.Error.Message != "" {
				message = event.Response.Error.Message
			} else if event.Error != nil && event.Error.Message != "" {
				message = event.Error.Message
			} else if event.Message != "" {
				message = event.Message
			}
			var usage *TokenUsage
			if u := event.Response.Usage; u != nil {
				usage = &TokenUsage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, ReasoningTokens: u.OutputTokensDetails.ReasoningTokens, CacheReadTokens: u.InputTokensDetails.CachedTokens}
			}
			return cb(StreamEvent{Type: EventError, Error: message, Usage: usage, Model: event.Response.Model})
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read OpenAI stream: %w", err)
	}
	return fmt.Errorf("OpenAI stream ended without a terminal response")
}

type anthropicProvider struct {
	apiKey, model, baseURL string
	client                 *http.Client
}

func (p *anthropicProvider) Stream(ctx context.Context, params StreamParams, cb func(StreamEvent) error) error {
	messages := make([]map[string]any, 0, len(params.Messages))
	for _, message := range params.Messages {
		switch message.Role {
		case RoleUser:
			messages = append(messages, map[string]any{"role": "user", "content": []map[string]any{{"type": "text", "text": message.Content}}})
		case RoleAssistant:
			content := make([]map[string]any, 0, len(message.ToolCalls)+1)
			if message.Content != "" {
				content = append(content, map[string]any{"type": "text", "text": message.Content})
			}
			for _, call := range message.ToolCalls {
				var input any
				if err := json.Unmarshal([]byte(call.Input), &input); err != nil {
					return fmt.Errorf("decode tool input: %w", err)
				}
				content = append(content, map[string]any{"type": "tool_use", "id": call.ID, "name": call.Name, "input": input})
			}
			messages = append(messages, map[string]any{"role": "assistant", "content": content})
		case RoleTool:
			if message.ToolResult != nil {
				messages = append(messages, map[string]any{"role": "user", "content": []map[string]any{{"type": "tool_result", "tool_use_id": message.ToolResult.ToolCallID, "content": message.ToolResult.Content, "is_error": message.ToolResult.IsError}}})
			}
		}
	}
	// Place at most three ephemeral breakpoints: the stable system/tools prefix
	// and the final user or tool-result content block in the growing history.
	cacheControl := map[string]any{"type": "ephemeral"}
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i]["role"] == "user" {
			content := messages[i]["content"].([]map[string]any)
			if len(content) > 0 {
				content[len(content)-1]["cache_control"] = cacheControl
			}
			break
		}
	}
	body := map[string]any{"model": p.model, "stream": true, "messages": messages, "max_tokens": params.MaxTokens}
	if params.System != "" {
		body["system"] = []map[string]any{{"type": "text", "text": params.System, "cache_control": cacheControl}}
	}
	if len(params.Tools) > 0 {
		tools := make([]map[string]any, len(params.Tools))
		for i, tool := range params.Tools {
			tools[i] = map[string]any{"name": tool.Name, "description": tool.Description, "input_schema": tool.InputSchema}
		}
		tools[len(tools)-1]["cache_control"] = cacheControl
		body["tools"] = tools
	}
	response, err := postModel(ctx, p.client, p.baseURL+"/v1/messages", p.apiKey, "anthropic", body)
	if err != nil {
		return err
	}
	defer response.Close()
	return parseAnthropic(response, cb)
}

func parseAnthropic(reader io.Reader, cb func(StreamEvent) error) error {
	var currentID, currentName string
	var args strings.Builder
	type anthropicUsage struct {
		InputTokens      *int `json:"input_tokens"`
		OutputTokens     *int `json:"output_tokens"`
		CacheReadTokens  *int `json:"cache_read_input_tokens"`
		CacheWriteTokens *int `json:"cache_creation_input_tokens"`
	}
	var usage *TokenUsage
	mergeUsage := func(update *anthropicUsage) {
		if update == nil {
			return
		}
		if usage == nil {
			usage = &TokenUsage{}
		}
		if update.InputTokens != nil {
			usage.InputTokens = *update.InputTokens
		}
		if update.OutputTokens != nil {
			usage.OutputTokens = *update.OutputTokens
		}
		if update.CacheReadTokens != nil {
			usage.CacheReadTokens = *update.CacheReadTokens
		}
		if update.CacheWriteTokens != nil {
			usage.CacheWriteTokens = *update.CacheWriteTokens
		}
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		var header struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(data), &header); err != nil {
			return fmt.Errorf("decode Anthropic stream type: %w", err)
		}
		switch header.Type {
		case "message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "error":
		default:
			continue
		}
		var event struct {
			Type         string                          `json:"type"`
			ContentBlock struct{ Type, ID, Name string } `json:"content_block"`
			Delta        struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
			Usage   *anthropicUsage `json:"usage"`
			Message struct {
				Model string          `json:"model"`
				Usage *anthropicUsage `json:"usage"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return fmt.Errorf("decode Anthropic stream: %w", err)
		}
		switch event.Type {
		case "message_start":
			if event.Message.Model != "" {
				if err := cb(StreamEvent{Type: EventUsage, Model: event.Message.Model}); err != nil {
					return err
				}
			}
			mergeUsage(event.Message.Usage)
			if usage != nil {
				copy := *usage
				if err := cb(StreamEvent{Type: EventUsage, Usage: &copy}); err != nil {
					return err
				}
			}
		case "content_block_start":
			if event.ContentBlock.Type == "tool_use" {
				currentID, currentName = event.ContentBlock.ID, event.ContentBlock.Name
				args.Reset()
			}
		case "content_block_delta":
			if event.Delta.Type == "text_delta" && event.Delta.Text != "" {
				if err := cb(StreamEvent{Type: EventText, Delta: event.Delta.Text}); err != nil {
					return err
				}
			}
			if event.Delta.Type == "input_json_delta" {
				args.WriteString(event.Delta.PartialJSON)
			}
		case "content_block_stop":
			if currentID != "" {
				raw := args.String()
				if raw == "" {
					raw = "{}"
				}
				if err := cb(StreamEvent{Type: EventToolUse, ToolCall: &ToolCall{ID: currentID, Name: currentName, Input: raw}}); err != nil {
					return err
				}
				currentID, currentName = "", ""
			}
		case "message_delta":
			mergeUsage(event.Usage)
			return cb(StreamEvent{Type: EventStop, StopReason: event.Delta.StopReason, Usage: usage})
		case "error":
			message := "Anthropic stream error"
			if event.Error != nil && event.Error.Message != "" {
				message = event.Error.Message
			}
			return cb(StreamEvent{Type: EventError, Error: message, Usage: usage})
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read Anthropic stream: %w", err)
	}
	return fmt.Errorf("anthropic stream ended without a stop reason")
}

func postModel(ctx context.Context, client *http.Client, url, apiKey, kind string, body any) (io.ReadCloser, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode model request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("create model request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if kind == "anthropic" {
		request.Header.Set("x-api-key", apiKey)
		request.Header.Set("anthropic-version", "2023-06-01")
	} else {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("model request: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		defer response.Body.Close()
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return nil, &APIError{StatusCode: response.StatusCode, Body: string(message)}
	}
	return response.Body, nil
}
