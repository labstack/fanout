// Package toolerror defines product errors inside MCP and agent tool results.
package toolerror

import "encoding/json"

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Result struct {
	Error *Error `json:"error"`
}

func New(code, message string) Result { return Result{Error: &Error{Code: code, Message: message}} }

func JSON(code, message string) string {
	raw, _ := json.Marshal(New(code, message))
	return string(raw)
}

// Normalize preserves structured result fields and gives unstructured failures
// the default tool code. Callers use it only for failed tool executions.
func Normalize(content string) map[string]any {
	var payload map[string]any
	if json.Unmarshal([]byte(content), &payload) != nil || payload == nil {
		payload = map[string]any{}
	}
	code, message := "tool_failed", ""
	switch value := payload["error"].(type) {
	case map[string]any:
		if text, _ := value["code"].(string); text != "" {
			code = text
		}
		message, _ = value["message"].(string)
		if code == value["code"] && message != "" {
			return payload
		}
	case string:
		message = value
	}
	if _, has := payload["error"]; message == "" && !has {
		message = content
	}
	if message == "" {
		message = "Tool execution failed."
	}
	payload["error"] = &Error{Code: code, Message: message}
	return payload
}
