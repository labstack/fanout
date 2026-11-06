package query

import (
	"testing"
	"time"
)

func TestTimeOverrideWindow(t *testing.T) {
	e := newFixtureExecutor(t)
	d := shopDashboard()
	d.Panels = d.Panels[:1]
	d.Panels[0].Time = &TimeOverride{Range: "15m"}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].FromMS != fixtureStart.Add(45*time.Minute).UnixMilli() || got[0].ToMS != fixtureStart.Add(time.Hour).UnixMilli() {
		t.Fatalf("window: %+v", got[0])
	}
	d.Panels[0].Time = &TimeOverride{Shift: "1h"}
	got, err = e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].FromMS != fixtureStart.Add(-time.Hour).UnixMilli() || got[0].ToMS != fixtureStart.UnixMilli() {
		t.Fatalf("shift: %+v", got[0])
	}
}
