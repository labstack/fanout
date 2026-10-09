package toolerror

import (
	"encoding/json"
	"testing"
)

func TestNormalizeKeepsPartialErrorObjectsUnwrapped(t *testing.T) {
	for _, tc := range []struct{ name, content, code, message string }{
		{"complete", `{"error":{"code":"answer_only","message":"x"}}`, "answer_only", "x"},
		{"missing code", `{"error":{"message":"x"}}`, "tool_failed", "x"},
		{"missing message", `{"error":{"code":"interrupted"}}`, "interrupted", "Tool execution failed."},
		{"string error", `{"error":"x"}`, "tool_failed", "x"},
		{"plain text", "x", "tool_failed", "x"},
		{"empty", "", "tool_failed", "Tool execution failed."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(Normalize(tc.content))
			if err != nil {
				t.Fatal(err)
			}
			var got Result
			if err := json.Unmarshal(raw, &got); err != nil || got.Error == nil || got.Error.Code != tc.code || got.Error.Message != tc.message {
				t.Fatalf("%s %v", raw, err)
			}
		})
	}
}
