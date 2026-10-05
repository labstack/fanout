package agent

import (
	"context"
	"errors"
	agtypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"strings"
	"testing"
	"time"
)

type deadlineProvider struct {
	t       *testing.T
	timeout time.Duration
}

func (p deadlineProvider) Stream(ctx context.Context, _ StreamParams, _ func(StreamEvent) error) error {
	d, ok := ctx.Deadline()
	if !ok || time.Until(d) > p.timeout {
		p.t.Errorf("missing run wall deadline: %v %v", d, ok)
		return context.DeadlineExceeded
	}
	<-ctx.Done()
	return ctx.Err()
}
func TestFinalFixAgentWallDeadline(t *testing.T) {
	r := NewRuntime(deadlineProvider{t: t, timeout: 5 * time.Minute}, &fakeTools{}, nil)
	if r.runTimeout != 5*time.Minute {
		t.Errorf("default=%v", r.runTimeout)
	}
	r.runTimeout = 10 * time.Millisecond
	r.provider = deadlineProvider{t: t, timeout: 10 * time.Millisecond}
	emitter, output := newTestEmitter()
	messages := []agtypes.Message{}
	_, err := r.execute(t.Context(), "thread", "run", &messages, emitter)
	if !errors.Is(err, errStepLimit) || !strings.Contains(output.String(), "5-minute time limit") {
		t.Fatalf("err=%v stream=%s", err, output.String())
	}
}
