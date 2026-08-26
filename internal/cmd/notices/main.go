// Command notices generates the third-party notices shipped with Fanout.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

type component struct {
	id        string
	kind      string
	license   string
	documents []document
}

type document struct {
	name string
	text string
}

type goPackage struct {
	Standard bool      `json:"Standard"`
	Module   *goModule `json:"Module"`
}

type goModule struct {
	Path    string    `json:"Path"`
	Version string    `json:"Version"`
	Dir     string    `json:"Dir"`
	Main    bool      `json:"Main"`
	Replace *goModule `json:"Replace"`
}

type npmPackage struct {
	Name                 string            `json:"name"`
	Version              string            `json:"version"`
	License              any               `json:"license"`
	Dependencies         map[string]string `json:"dependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
	PeerDependencies     map[string]string `json:"peerDependencies"`
	PeerDependenciesMeta map[string]struct {
		Optional bool `json:"optional"`
	} `json:"peerDependenciesMeta"`
}

type noticeGroup struct {
	text    string
	sources []string
}

func main() {
	root := flag.String("root", ".", "repository root")
	output := flag.String("output", "THIRD_PARTY_NOTICES", "output path, relative to root")
	check := flag.Bool("check", false, "fail if the output is not current")
	flag.Parse()

	absRoot, err := filepath.Abs(*root)
	if err != nil {
		fatal(err)
	}

	components, err := collect(absRoot)
	if err != nil {
		fatal(err)
	}
	generated := render(components)
	outputPath := filepath.Join(absRoot, *output)

	if *check {
		current, err := os.ReadFile(outputPath)
		if err != nil {
			fatal(fmt.Errorf("read %s: %w", outputPath, err))
		}
		if !bytes.Equal(current, generated) {
			fatal(errors.New("THIRD_PARTY_NOTICES is stale; run `just notices` using the Go version declared in go.mod (a different toolchain can select a different dependency graph)"))
		}
		fmt.Println("THIRD_PARTY_NOTICES matches the release dependency graph")
		return
	}

	tmp, err := os.CreateTemp(filepath.Dir(outputPath), ".third-party-notices-*")
	if err != nil {
		fatal(err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(generated); err != nil {
		tmp.Close()
		fatal(err)
	}
	if err := tmp.Close(); err != nil {
		fatal(err)
	}
	if err := os.Rename(tmpName, outputPath); err != nil {
		fatal(err)
	}
	fmt.Printf("wrote %s (%d components)\n", outputPath, len(components))
}

func collect(root string) ([]component, error) {
	all := map[string]component{}
	if err := collectGo(root, all); err != nil {
		return nil, err
	}
	for _, workspace := range []string{"ui/host", "ui/apps"} {
		if err := collectNPM(root, filepath.Join(root, workspace), all); err != nil {
			return nil, err
		}
	}
	result := make([]component, 0, len(all))
	for _, item := range all {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].id < result[j].id })
	return result, nil
}

func collectGo(root string, all map[string]component) error {
	targets := [][2]string{{"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "amd64"}, {"darwin", "arm64"}}
	for _, target := range targets {
		cmd := exec.Command("go", "list", "-deps", "-json", "./cmd/fanout")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "CGO_ENABLED=1", "GOOS="+target[0], "GOARCH="+target[1])
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Start(); err != nil {
			return err
		}
		dec := json.NewDecoder(stdout)
		for {
			var pkg goPackage
			if err := dec.Decode(&pkg); err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				return fmt.Errorf("decode go list for %s/%s: %w", target[0], target[1], err)
			}
			module := pkg.Module
			if module == nil || module.Main || module.Path == "" {
				continue
			}
			if module.Replace != nil {
				module = module.Replace
			}
			version := module.Version
			if version == "" {
				version = "unversioned replacement"
			}
			id := "Go: " + module.Path + " " + version
			if _, exists := all[id]; exists {
				continue
			}
			docs, err := licenseDocuments(module.Dir)
			if err != nil {
				return fmt.Errorf("%s: %w", id, err)
			}
			if len(docs) == 0 {
				return fmt.Errorf("%s has no LICENSE, COPYING, NOTICE, or COPYRIGHT file", id)
			}
			all[id] = component{id: id, kind: "Go module", documents: docs}
		}
		if err := cmd.Wait(); err != nil {
			return fmt.Errorf("go list for %s/%s: %w: %s", target[0], target[1], err, strings.TrimSpace(stderr.String()))
		}
	}
	return nil
}

func collectNPM(root, workspace string, all map[string]component) error {
	manifestPath := filepath.Join(workspace, "package.json")
	manifest, err := readNPMManifest(manifestPath)
	if err != nil {
		return err
	}
	names := sortedKeys(manifest.Dependencies)
	type dependency struct {
		name, from string
		optional   bool
	}
	queue := make([]dependency, 0, len(names))
	for _, name := range names {
		queue = append(queue, dependency{name: name, from: workspace})
	}
	seenDirs := map[string]bool{}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		pkgDir, err := resolveNPM(next.name, next.from)
		if err != nil {
			if next.optional {
				continue
			}
			return fmt.Errorf("%s production dependency %s: %w", workspace, next.name, err)
		}
		realDir, err := filepath.EvalSymlinks(pkgDir)
		if err != nil {
			return err
		}
		if seenDirs[realDir] {
			continue
		}
		seenDirs[realDir] = true
		pkg, err := readNPMManifest(filepath.Join(realDir, "package.json"))
		if err != nil {
			return err
		}
		if pkg.Name == "" || pkg.Version == "" {
			return fmt.Errorf("%s has incomplete package metadata", realDir)
		}
		id := "npm: " + pkg.Name + " " + pkg.Version
		docs, err := licenseDocuments(realDir)
		if err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
		license := licenseString(pkg.License)
		supplemental, err := supplementalDocuments(root, id)
		if err != nil {
			return err
		}
		docs = append(docs, supplemental...)
		sort.Slice(docs, func(i, j int) bool { return docs[i].name < docs[j].name })
		if len(docs) == 0 && license == "" {
			return fmt.Errorf("%s has neither license metadata nor a license document", id)
		}
		if existing, exists := all[id]; exists {
			if existing.license != license || !sameDocuments(existing.documents, docs) {
				return fmt.Errorf("%s resolves to conflicting package contents", id)
			}
		} else {
			all[id] = component{id: id, kind: "npm production package", license: license, documents: docs}
		}

		for _, name := range sortedKeys(pkg.Dependencies) {
			queue = append(queue, dependency{name: name, from: realDir})
		}
		for _, name := range sortedKeys(pkg.OptionalDependencies) {
			queue = append(queue, dependency{name: name, from: realDir, optional: true})
		}
		for _, name := range sortedKeys(pkg.PeerDependencies) {
			queue = append(queue, dependency{
				name: name, from: realDir,
				optional: pkg.PeerDependenciesMeta[name].Optional,
			})
		}
	}
	return nil
}

func supplementalDocuments(root, id string) ([]document, error) {
	files := map[string][]string{
		"npm: @bufbuild/protobuf 2.12.1": {
			"LICENSE",
			"third_party/notices/bufbuild-protobuf-NOTICE.txt",
			"third_party/notices/bufbuild-protobuf-BSD.txt",
		},
		"npm: @protobuf-ts/protoc 2.11.1":    {"LICENSE"},
		"npm: react-remove-scroll-bar 2.3.8": {"third_party/notices/react-remove-scroll-bar-MIT.txt"},
	}[id]
	var docs []document
	for _, name := range files {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			return nil, fmt.Errorf("supplemental license for %s: %w", id, err)
		}
		docs = append(docs, document{
			name: "supplemental/" + filepath.Base(name),
			text: strings.TrimSpace(strings.ReplaceAll(string(data), "\r\n", "\n")) + "\n",
		})
	}
	return docs, nil
}

func readNPMManifest(path string) (npmPackage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return npmPackage{}, err
	}
	var pkg npmPackage
	if err := json.Unmarshal(data, &pkg); err != nil {
		return npmPackage{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return pkg, nil
}

func resolveNPM(name, from string) (string, error) {
	for dir := from; ; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, "node_modules", filepath.FromSlash(name))
		if info, err := os.Stat(filepath.Join(candidate, "package.json")); err == nil && !info.IsDir() {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return "", errors.New("not installed; run `just ui-deps`")
}

func licenseDocuments(dir string) ([]document, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var docs []document
	for _, entry := range entries {
		if entry.IsDir() || !isLicenseDocument(entry.Name()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		text := strings.ReplaceAll(string(data), "\r\n", "\n")
		text = strings.TrimSpace(text) + "\n"
		docs = append(docs, document{name: entry.Name(), text: text})
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].name < docs[j].name })
	return docs, nil
}

func isLicenseDocument(name string) bool {
	upper := strings.ToUpper(name)
	for _, prefix := range []string{"LICENSE", "LICENCE", "COPYING", "NOTICE", "COPYRIGHT"} {
		if upper == prefix || strings.HasPrefix(upper, prefix+".") || strings.HasPrefix(upper, prefix+"-") {
			return true
		}
	}
	return false
}

func licenseString(value any) string {
	switch value := value.(type) {
	case string:
		return value
	case map[string]any:
		if kind, ok := value["type"].(string); ok {
			return kind
		}
	case []any:
		var values []string
		for _, item := range value {
			if got := licenseString(item); got != "" {
				values = append(values, got)
			}
		}
		return strings.Join(values, " OR ")
	}
	return ""
}

func sameDocuments(a, b []document) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func render(components []component) []byte {
	var out strings.Builder
	out.WriteString("FANOUT THIRD-PARTY NOTICES\n\n")
	out.WriteString("This file is generated from the Go packages linked into the four supported\n")
	out.WriteString("release targets and from the production dependency graphs of the two embedded\n")
	out.WriteString("browser applications. Development-only dependencies are excluded. Regenerate it\n")
	out.WriteString("with `just notices`; CI verifies it with `just notices-check`.\n\n")
	out.WriteString("The following components are provided under their own terms. Fanout's Apache-2.0\n")
	out.WriteString("license does not replace or alter those terms.\n\n")
	out.WriteString("COMPONENT INVENTORY\n")
	out.WriteString("===================\n\n")

	groups := map[string]*noticeGroup{}
	for _, item := range components {
		fmt.Fprintf(&out, "- %s", item.id)
		if item.license != "" {
			fmt.Fprintf(&out, " (declared license: %s)", item.license)
		}
		if len(item.documents) == 0 {
			out.WriteString("; no license file was present in the installed package")
		}
		out.WriteByte('\n')
		for _, doc := range item.documents {
			sum := sha256.Sum256([]byte(doc.text))
			key := hex.EncodeToString(sum[:])
			group := groups[key]
			if group == nil {
				group = &noticeGroup{text: doc.text}
				groups[key] = group
			}
			group.sources = append(group.sources, item.id+" / "+doc.name)
		}
	}

	out.WriteString("\nLICENSE AND NOTICE TEXTS\n")
	out.WriteString("========================\n")
	ordered := make([]*noticeGroup, 0, len(groups))
	for _, group := range groups {
		sort.Strings(group.sources)
		ordered = append(ordered, group)
	}
	sort.Slice(ordered, func(i, j int) bool {
		return strings.Join(ordered[i].sources, "\n") < strings.Join(ordered[j].sources, "\n")
	})
	for _, group := range ordered {
		out.WriteString("\n--- Applies to -------------------------------------------------------------\n")
		for _, source := range group.sources {
			fmt.Fprintf(&out, "- %s\n", source)
		}
		out.WriteString("----------------------------------------------------------------------------\n\n")
		out.WriteString(group.text)
	}
	return []byte(out.String())
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "notices:", err)
	os.Exit(1)
}
