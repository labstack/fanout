package lintdocs

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Inserting a declaration between a doc comment and the thing it documents is
// invisible to gofmt, go vet and the compiler, and it silently reassigns the
// comment: the new declaration inherits a block describing something else, and
// the original is left undocumented. It happened three times in this tree, twice
// in one afternoon, every time from a scripted edit that matched on the
// declaration line and inserted above it.
//
// Requiring every doc comment to start with its own declaration's name would
// flag hundreds that simply do not follow that convention, and a guard that is
// mostly false positives gets muted. This checks the exact signature of the bug
// instead: a doc comment whose first word names the declaration immediately
// BELOW the one it is attached to. That is what such an insertion produces, and
// nobody writes it on purpose.
func TestDocCommentsDocumentWhatTheySitOn(t *testing.T) {
	root := "../../.."
	fset := token.NewFileSet()

	// Every declaration in source order, documented or not. Tracking only the
	// documented ones would compare against the next *documented* declaration
	// and skip straight past the undocumented victim -- which is precisely the
	// declaration this is looking for.
	type decl struct {
		file string
		line int
		name string
		doc  string
	}
	var decls []decl

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "node_modules", "vendor", "dist", "site", "experiments":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		parsed, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil // not this guard's job to report unparseable files
		}
		add := func(name string, doc *ast.CommentGroup, pos token.Pos) {
			text := ""
			if doc != nil {
				text = doc.Text()
			}
			decls = append(decls, decl{file: path, line: fset.Position(pos).Line, name: name, doc: text})
		}
		for _, d := range parsed.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				add(d.Name.Name, d.Doc, d.Pos())
			case *ast.GenDecl:
				// A grouped block is one declaration: its doc describes the
				// block, which conventionally means its first name.
				for si, spec := range d.Specs {
					var name string
					switch spec := spec.(type) {
					case *ast.TypeSpec:
						name = spec.Name.Name
					case *ast.ValueSpec:
						if len(spec.Names) > 0 {
							name = spec.Names[0].Name
						}
					}
					if name == "" {
						continue
					}
					if si == 0 {
						add(name, d.Doc, d.Pos())
					} else {
						add(name, nil, spec.Pos())
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(decls) < 500 {
		t.Fatalf("only %d declarations found; this guard is not looking at the right tree", len(decls))
	}

	documented, flagged := 0, 0
	for i, d := range decls {
		if d.doc == "" {
			continue
		}
		documented++
		if i+1 >= len(decls) || decls[i+1].file != d.file {
			continue
		}
		first, _, _ := strings.Cut(strings.TrimSpace(d.doc), " ")
		first = strings.TrimRight(first, ".,:")
		if first == d.name || first != decls[i+1].name {
			continue
		}
		flagged++
		t.Errorf("%s:%d: the doc comment on %s describes %s, which is declared immediately below it\n"+
			"a declaration was inserted between that comment and what it documents",
			d.file, d.line, d.name, first)
	}
	t.Logf("checked %d declarations, %d documented, %d flagged", len(decls), documented, flagged)
}
