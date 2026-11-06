package query

import (
	"slices"
	"strings"
)

// SignalColumns returns a copy of every catalog column in display order.
func SignalColumns(signal string) ([]Field, bool) {
	s, ok := lookupSignal(signal)
	if !ok {
		return nil, false
	}
	return slices.Clone(s.fields), true
}

// LowCardinalityColumns returns columns whose common values the schema lists.
func LowCardinalityColumns(signal string) []string {
	s, ok := lookupSignal(signal)
	if !ok {
		return nil
	}
	return slices.Clone(s.lowCard)
}

// MeasureSpec describes a registered aggregate without exposing mutable tables.
type MeasureSpec struct {
	Name     string
	Args     int
	Numeric  bool
	Additive bool
	Unit     string
	Signals  []string
	Quantile float64
}

// MeasureSpecs lists registered measures, sorted by name.
func MeasureSpecs() []MeasureSpec {
	out := make([]MeasureSpec, 0, len(measureFuncs))
	for _, name := range measureNames() {
		s := measureFuncs[name]
		out = append(out, MeasureSpec{Name: name, Args: s.args, Numeric: s.numeric, Additive: s.additive, Unit: s.unit, Signals: slices.Clone(s.signals), Quantile: s.quantile})
	}
	return out
}

// BucketNames lists auto followed by the registered intervals in duration order.
func BucketNames() []string {
	out := make([]string, 0, len(buckets)+1)
	for name := range buckets {
		out = append(out, name)
	}
	slices.SortFunc(out, func(a, b string) int {
		if buckets[a] < buckets[b] {
			return -1
		}
		if buckets[a] > buckets[b] {
			return 1
		}
		return 0
	})
	return append([]string{"auto"}, out...)
}

// FilterFunctionNames lists named filter functions, excluding SQL operators.
func FilterFunctionNames() []string {
	out := make([]string, 0, len(filterFunctions))
	for name := range filterFunctions {
		if !strings.Contains(name, "~") {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}
