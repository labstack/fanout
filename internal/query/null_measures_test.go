package query

import "testing"

// A measure of a field no matching span carries returns rows of nulls: the
// panel has no data, and the diagnosis says the measure is what is empty.
func TestNullMeasuresAreEmpty(t *testing.T) {
	e := newFixtureExecutor(t)
	d := Dashboard{Name: "Nulls", Time: Time{Range: "1h"}, Panels: []Panel{{ID: "p", Title: "p95", Viz: "bar",
		Query: &Spec{From: "spans", Measures: []string{"p95(attributes['nosuchfield_xyz'])"}, By: []string{"service"}}}}}
	got, err := e.Run(t.Context(), RunRequest{Dashboard: d})
	if err != nil {
		t.Fatal(err)
	}
	want := "The matching spans have no values for this measure in this time range."
	if r := got[0]; r.Status != StatusEmpty || r.Diagnosis != want {
		t.Fatalf("result %+v, want empty with %q", r, want)
	}
}
