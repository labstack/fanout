package pitotql

import (
	"encoding/json"
	"testing"
)

func TestRecordedFailureReadsPreviousCodeAndWritesCurrentCode(t *testing.T) {
	var failure Failure
	if err := json.Unmarshal([]byte(`{"code":"invalid_fanql","message":"The query has problems."}`), &failure); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(failure)
	if err != nil || string(raw) != `{"code":"invalid_pitotql","message":"The query has problems."}` {
		t.Fatalf("failure = %s, %v", raw, err)
	}
}
