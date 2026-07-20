package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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
			model = "claude-sonnet-4-6"
		}
		if baseURL == "" {
			baseURL = "https://api.anthropic.com"
		}
		return &anthropicProvider{apiKey: apiKey, model: model, baseURL: strings.TrimRight(baseURL, "/"), client: modelHTTPClient()}, nil
	case "openai":
		if model == "" {
			model = "gpt-5.4"
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

type openAIProvider struct {
	apiKey, model, baseURL string
	client                 *http.Client
}

func (p *openAIProvider) Stream(ctx context.Context, params StreamParams, cb func(StreamEvent) error) error {
	messages := make([]map[string]any, 0, len(params.Messages)+1)
	if params.System != "" {
		messages = append(messages, map[string]any{"role": "system", "content": params.System})
	}
	for _, message := range params.Messages {
		switch message.Role {
		case RoleUser:
			messages = append(messages, map[string]any{"role": "user", "content": message.Content})
		case RoleAssistant:
			out := map[string]any{"role": "assistant", "content": message.Content}
			if len(message.ToolCalls) > 0 {
				calls := make([]map[string]any, len(message.ToolCalls))
				for i, call := range message.ToolCalls {
					calls[i] = map[string]any{"id": call.ID, "type": "function", "function": map[string]any{"name": call.Name, "arguments": call.Input}}
				}
				out["tool_calls"] = calls
			}
			messages = append(messages, out)
		case RoleTool:
			if message.ToolResult != nil {
				messages = append(messages, map[string]any{"role": "tool", "tool_call_id": message.ToolResult.ToolCallID, "content": message.ToolResult.Content})
			}
		}
	}
	body := map[string]any{"model": p.model, "stream": true, "messages": messages, "max_completion_tokens": params.MaxTokens}
	if len(params.Tools) > 0 {
		tools := make([]map[string]any, len(params.Tools))
		for i, tool := range params.Tools {
			tools[i] = map[string]any{"type": "function", "function": map[string]any{"name": tool.Name, "description": tool.Description, "parameters": tool.InputSchema}}
		}
		body["tools"] = tools
	}
	response, err := postModel(ctx, p.client, p.baseURL+"/v1/chat/completions", p.apiKey, "openai", body)
	if err != nil {
		return err
	}
	defer response.Close()
	return parseOpenAI(response, cb)
}

func parseOpenAI(reader io.Reader, cb func(StreamEvent) error) error {
	type accumulator struct {
		id, name string
		args     strings.Builder
	}
	calls := map[int]*accumulator{}
	stopped := false
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
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
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return fmt.Errorf("decode OpenAI stream: %w", err)
		}
		if chunk.Error != nil {
			return cb(StreamEvent{Error: chunk.Error.Message})
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]
		if choice.Delta.Content != "" {
			if err := cb(StreamEvent{Delta: choice.Delta.Content}); err != nil {
				return err
			}
		}
		for _, incoming := range choice.Delta.ToolCalls {
			call := calls[incoming.Index]
			if call == nil {
				call = &accumulator{}
				calls[incoming.Index] = call
			}
			if incoming.ID != "" {
				call.id = incoming.ID
			}
			if incoming.Function.Name != "" {
				call.name = incoming.Function.Name
			}
			call.args.WriteString(incoming.Function.Arguments)
		}
		if choice.FinishReason != nil {
			if *choice.FinishReason == "tool_calls" {
				indices := make([]int, 0, len(calls))
				for index := range calls {
					indices = append(indices, index)
				}
				sort.Ints(indices)
				for _, index := range indices {
					call := calls[index]
					args := call.args.String()
					if args == "" {
						args = "{}"
					}
					if err := cb(StreamEvent{ToolCall: &ToolCall{ID: call.id, Name: call.name, Input: args}}); err != nil {
						return err
					}
				}
			}
			stopped = true
			if err := cb(StreamEvent{StopReason: *choice.FinishReason}); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read OpenAI stream: %w", err)
	}
	if !stopped {
		return fmt.Errorf("OpenAI stream ended without a finish reason")
	}
	return nil
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
			messages = append(messages, map[string]any{"role": "user", "content": message.Content})
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
	body := map[string]any{"model": p.model, "stream": true, "messages": messages, "max_tokens": params.MaxTokens}
	if params.System != "" {
		body["system"] = params.System
	}
	if len(params.Tools) > 0 {
		tools := make([]map[string]any, len(params.Tools))
		for i, tool := range params.Tools {
			tools[i] = map[string]any{"name": tool.Name, "description": tool.Description, "input_schema": tool.InputSchema}
		}
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
	stopped := false
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event struct {
			Type         string                                               `json:"type"`
			ContentBlock struct{ Type, ID, Name string }                      `json:"content_block"`
			Delta        struct{ Type, Text, PartialJSON, StopReason string } `json:"delta"`
			Error        *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			return fmt.Errorf("decode Anthropic stream: %w", err)
		}
		switch event.Type {
		case "content_block_start":
			if event.ContentBlock.Type == "tool_use" {
				currentID, currentName = event.ContentBlock.ID, event.ContentBlock.Name
				args.Reset()
			}
		case "content_block_delta":
			if event.Delta.Type == "text_delta" && event.Delta.Text != "" {
				if err := cb(StreamEvent{Delta: event.Delta.Text}); err != nil {
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
				if err := cb(StreamEvent{ToolCall: &ToolCall{ID: currentID, Name: currentName, Input: raw}}); err != nil {
					return err
				}
				currentID, currentName = "", ""
			}
		case "message_delta":
			stopped = true
			if err := cb(StreamEvent{StopReason: event.Delta.StopReason}); err != nil {
				return err
			}
		case "error":
			message := "Anthropic stream error"
			if event.Error != nil && event.Error.Message != "" {
				message = event.Error.Message
			}
			return cb(StreamEvent{Error: message})
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read Anthropic stream: %w", err)
	}
	if !stopped {
		return fmt.Errorf("anthropic stream ended without a stop reason")
	}
	return nil
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
