package panel

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func lookupFixed(values map[string][]any, lists map[string]bool) func(string) ([]any, bool, error) {
	return func(name string) ([]any, bool, error) {
		v, ok := values[name]
		if !ok {
			return nil, false, fmt.Errorf("unknown variable $%s", name)
		}
		return v, lists[name], nil
	}
}

func TestBindParams(t *testing.T) {
	query, describe, args, err := bindParams(
		"service = $service AND body LIKE '$notavar' AND \"$col\" = 1 AND route IN ($routes)",
		lookupFixed(map[string][]any{"service": {"o'brien"}, "routes": {"/a", "/b"}}, map[string]bool{"routes": true}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if query != `service = ? AND body LIKE '$notavar' AND "$col" = 1 AND route IN (?, ?)` {
		t.Fatal(query)
	}
	if describe != `service = NULL AND body LIKE '$notavar' AND "$col" = 1 AND route IN (NULL, NULL)` {
		t.Fatal(describe)
	}
	if !reflect.DeepEqual(args, []any{"o'brien", "/a", "/b"}) {
		t.Fatal(args)
	}
	for _, bad := range []string{"x = $1", "x = ?"} {
		if _, _, _, err := bindParams(bad, lookupFixed(nil, nil)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestExpandMacros(t *testing.T) {
	out, err := expandMacros("SELECT $__bucket(s.start_time) AS t, count(*) FROM spans s WHERE $__window(s.start_time) GROUP BY 1", 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	want := "SELECT time_bucket(INTERVAL '300 seconds', s.start_time::TIMESTAMP_NS) AS t, count(*) FROM spans s WHERE s.start_time >= $__from::TIMESTAMP_NS::TIMESTAMPTZ_NS AND s.start_time < $__to::TIMESTAMP_NS::TIMESTAMPTZ_NS GROUP BY 1"
	if out != want {
		t.Fatalf("got  %s\nwant %s", out, want)
	}
	if _, err := expandMacros("SELECT count(*) FROM spans", time.Minute); err == nil || !strings.Contains(err.Error(), "$__window") {
		t.Fatalf("missing window macro err = %v", err)
	}
	if _, err := expandMacros("SELECT $__interval FROM spans WHERE $__window(start_time)", time.Minute); err == nil {
		t.Fatal("unknown macro accepted")
	}
}

func TestBindParamsExpandsListsInsideCasts(t *testing.T) {
	query, describe, args, err := bindParams("(x IN (CAST($r AS DOUBLE))) AND y = CAST($s AS VARCHAR)",
		lookupFixed(map[string][]any{"r": {"1", "2"}, "s": {"a"}}, map[string]bool{"r": true}))
	if err != nil {
		t.Fatal(err)
	}
	if query != "(x IN (CAST(? AS DOUBLE), CAST(? AS DOUBLE))) AND y = CAST(? AS VARCHAR)" || len(args) != 3 {
		t.Fatal(query, args)
	}
	if describe != "(x IN (CAST(NULL AS DOUBLE), CAST(NULL AS DOUBLE))) AND y = CAST(NULL AS VARCHAR)" {
		t.Fatal(describe)
	}
}
