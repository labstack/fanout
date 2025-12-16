// Package render provides a component-based rendering system for reports.
//
// Components are self-registering: each component file registers itself
// with the global registry in its init() function.
//
// To add a new component:
//  1. Create a new file comp_<name>.go
//  2. Implement the Component interface
//  3. Register in init(): Register(&myComponent{})
//
// The registry provides:
//   - Type-safe config validation via JSON Schema
//   - Dynamic CSS collection from all components
//   - Auto-generated tool descriptions
package render

// Legacy types for backward compatibility with existing code.
// These are superseded by the Component interface in registry.go.

// Tree renders hierarchical data (used by trace viewer)
type Tree struct {
	Title string
	Root  *Node
}

// Node is a tree node
type Node struct {
	Label    string
	Value    string
	Children []*Node
	Meta     map[string]string
}

// Compose combines multiple renderers
type Compose struct {
	Title    string
	Items    []Renderer
	Vertical bool
}

// Render implements Renderer for Tree
func (t *Tree) Render(format Format) Output {
	var out Output
	out.Title = t.Title
	if format == ASCII || format == Both {
		out.ASCII = t.renderASCII()
	}
	if format == HTML || format == Both {
		out.HTML = t.renderHTML()
	}
	return out
}

func (t *Tree) renderASCII() string {
	var sb stringBuilder
	if t.Title != "" {
		sb.WriteString(t.Title + "\n")
	}
	if t.Root != nil {
		renderNode(&sb, t.Root, "", true)
	}
	return trimSuffix(sb.String(), "\n")
}

func renderNode(sb *stringBuilder, n *Node, prefix string, last bool) {
	connector := "├── "
	if last {
		connector = "└── "
	}
	line := prefix + connector + n.Label
	if n.Value != "" {
		line += ": " + n.Value
	}
	if status, ok := n.Meta["status"]; ok {
		line += " [" + status + "]"
	}
	if dur, ok := n.Meta["duration"]; ok {
		line += " (" + dur + ")"
	}
	sb.WriteString(line + "\n")

	childPrefix := prefix
	if last {
		childPrefix += "    "
	} else {
		childPrefix += "│   "
	}
	for i, child := range n.Children {
		renderNode(sb, child, childPrefix, i == len(n.Children)-1)
	}
}

func (t *Tree) renderHTML() string {
	var sb stringBuilder
	sb.WriteString(`<sl-tree>`)
	if t.Root != nil {
		renderHTMLNode(&sb, t.Root)
	}
	sb.WriteString(`</sl-tree>`)
	return sb.String()
}

func renderHTMLNode(sb *stringBuilder, n *Node) {
	sb.WriteString(`<sl-tree-item>`)
	sb.WriteString(n.Label)
	if n.Value != "" {
		sb.WriteString(`: <span class="tree-value">` + n.Value + `</span>`)
	}
	if status, ok := n.Meta["status"]; ok {
		sb.WriteString(` <sl-badge variant="` + statusVariant(status) + `" size="small">` + status + `</sl-badge>`)
	}
	for _, child := range n.Children {
		renderHTMLNode(sb, child)
	}
	sb.WriteString(`</sl-tree-item>`)
}

// Render implements Renderer for Compose
func (c *Compose) Render(format Format) Output {
	var out Output
	out.Title = c.Title

	var asciiParts, htmlParts []string
	for _, item := range c.Items {
		r := item.Render(format)
		if r.ASCII != "" {
			asciiParts = append(asciiParts, r.ASCII)
		}
		if r.HTML != "" {
			htmlParts = append(htmlParts, r.HTML)
		}
	}

	if format == ASCII || format == Both {
		sep := "  "
		if c.Vertical {
			sep = "\n\n"
		}
		if c.Title != "" {
			out.ASCII = c.Title + "\n" + join(asciiParts, sep)
		} else {
			out.ASCII = join(asciiParts, sep)
		}
	}
	if format == HTML || format == Both {
		dir := "row"
		if c.Vertical {
			dir = "column"
		}
		out.HTML = `<div class="compose compose-` + dir + `">` + join(htmlParts, "") + `</div>`
	}
	return out
}

// stringBuilder is a simple string builder
type stringBuilder struct {
	s string
}

func (sb *stringBuilder) WriteString(s string) {
	sb.s += s
}

func (sb *stringBuilder) String() string {
	return sb.s
}

func trimSuffix(s, suffix string) string {
	if len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix {
		return s[:len(s)-len(suffix)]
	}
	return s
}

func join(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for i := 1; i < len(parts); i++ {
		result += sep + parts[i]
	}
	return result
}
