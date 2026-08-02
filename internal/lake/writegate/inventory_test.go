package writegate

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"testing"
)

// TestCatalogWritePathInventory is the executable inventory for every shipped
// DuckLake catalog-writing path. If a path is added, removed, or relabeled, this
// test forces the bounded operation inventory to change with it.
func TestCatalogWritePathInventory(t *testing.T) {
	t.Parallel()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve inventory test path")
	}
	dir := filepath.Dir(thisFile)
	files := map[string]map[string]WriteOperation{
		filepath.Join(dir, "..", "writer.go"): {
			"insertSpans":   WriteIngestSpans,
			"insertLogs":    WriteIngestLogs,
			"insertMetrics": WriteIngestMetrics,
		},
		filepath.Join(dir, "..", "..", "query", "duck.go"): {
			"skipRollupToLatest":    WriteRollupSkip,
			"refreshServiceRollup":  WriteRollupService,
			"refreshEndpointRollup": WriteRollupEndpoint,
			"refreshEdgeRollup":     WriteRollupEdge,
			"runMerge":              WriteMerge,
			"runMaintenance":        WriteMaintenance,
		},
	}

	actualOperations := make(map[WriteOperation]struct{})
	for path, want := range files {
		got := writeOperationsByFunction(t, path)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s write operation inventory = %#v, want %#v", path, got, want)
		}
		for _, operation := range got {
			actualOperations[operation] = struct{}{}
		}
	}

	gotOperations := make([]string, 0, len(actualOperations))
	for operation := range actualOperations {
		gotOperations = append(gotOperations, string(operation))
	}
	wantOperations := make([]string, 0, len(allWriteOperations))
	for _, operation := range allWriteOperations {
		wantOperations = append(wantOperations, string(operation))
	}
	sort.Strings(gotOperations)
	sort.Strings(wantOperations)
	if !reflect.DeepEqual(gotOperations, wantOperations) {
		t.Errorf("instrumented operations = %v, bounded operations = %v", gotOperations, wantOperations)
	}
}

func writeOperationsByFunction(t *testing.T, path string) map[string]WriteOperation {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	known := make(map[string]WriteOperation, len(allWriteOperations))
	for _, operation := range allWriteOperations {
		known[constantName(operation)] = operation
	}
	got := make(map[string]WriteOperation)
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			packageName, ok := selector.X.(*ast.Ident)
			if !ok || packageName.Name != "writegate" {
				return true
			}
			if operation, ok := known[selector.Sel.Name]; ok {
				if previous, exists := got[function.Name.Name]; exists && previous != operation {
					t.Errorf("%s uses multiple write operations: %q and %q", function.Name.Name, previous, operation)
				}
				got[function.Name.Name] = operation
			}
			return true
		})
	}
	return got
}

func constantName(operation WriteOperation) string {
	switch operation {
	case WriteIngestSpans:
		return "WriteIngestSpans"
	case WriteIngestLogs:
		return "WriteIngestLogs"
	case WriteIngestMetrics:
		return "WriteIngestMetrics"
	case WriteRollupSkip:
		return "WriteRollupSkip"
	case WriteRollupService:
		return "WriteRollupService"
	case WriteRollupEndpoint:
		return "WriteRollupEndpoint"
	case WriteRollupEdge:
		return "WriteRollupEdge"
	case WriteMerge:
		return "WriteMerge"
	case WriteMaintenance:
		return "WriteMaintenance"
	default:
		panic("unknown write operation: " + string(operation))
	}
}
