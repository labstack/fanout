package observability

import (
	"strings"
	"testing"
)

func TestCompletedEndpointQueryBindsEventWindowDirectly(t *testing.T) {
	if !strings.Contains(completedEndpointsQuery, "s.start_time>=$1::TIMESTAMP_NS::TIMESTAMPTZ_NS") {
		t.Fatal("missing direct event-time predicate")
	}
}
