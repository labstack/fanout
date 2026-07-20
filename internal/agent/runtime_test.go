package agent

import (
	"bytes"
	"context"
	"strings"
	"testing"

	agtypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/encoding/sse"
)

type textProvider struct{}

func (textProvider) Stream(_ context.Context, _ StreamParams, callback func(StreamEvent) error) error {
	if err := callback(StreamEvent{Delta: "Telemetry "}); err != nil {
		return err
	}
	if err := callback(StreamEvent{Delta: "looks healthy."}); err != nil {
		return err
	}
	return callback(StreamEvent{StopReason: "end_turn"})
}

func TestRuntimeEmitsStandardAGUISequence(t *testing.T) {
	runtime := NewRuntime(textProvider{}, &ToolRegistry{}, nil)
	var output bytes.Buffer
	emitter := &eventEmitter{ctx: context.Background(), writer: &output, sse: sse.NewSSEWriter()}
	messages := []agtypes.Message{{ID: "user-1", Role: agtypes.RoleUser, Content: "status?"}}
	if err := runtime.execute(context.Background(), "thread-1", "run-1", &messages, emitter); err != nil {
		t.Fatal(err)
	}
	stream := output.String()
	for _, eventType := range []string{"RUN_STARTED", "TEXT_MESSAGE_START", "TEXT_MESSAGE_CONTENT", "TEXT_MESSAGE_END", "RUN_FINISHED"} {
		if !strings.Contains(stream, `"type":"`+eventType+`"`) {
			t.Errorf("stream missing %s: %s", eventType, stream)
		}
	}
	if len(messages) != 2 || messages[1].Content != "Telemetry looks healthy." {
		t.Fatalf("messages = %#v", messages)
	}
}
