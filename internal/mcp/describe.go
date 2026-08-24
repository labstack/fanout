package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/labstack/fanout/internal/dashboard"
)

// This file exists so the MCP reference can be generated from the server itself
// rather than from a description of it.
//
// It does not read the registration calls. It starts a real server over an
// in-memory transport and issues `tools/list` — the same request a connecting
// agent makes — then publishes the answer. A generator that parsed
// registerTools would be describing the shape of the code; this asks the server
// the question its clients ask, so a tool renamed, re-annotated, or given a new
// input cannot be documented as it used to be.
//
// Nothing here decides anything. If one of these disagrees with the server, the
// accessor is wrong, because the server is what agents talk to.

// ToolDoc is what a reader needs before pointing an agent at a tool: what it is
// called, what it does, whether it changes anything, and what it accepts.
type ToolDoc struct {
	Name        string
	Title       string
	Description string

	// ReadOnly is the server's own readOnlyHint.
	ReadOnly bool
	// Destructive is meaningful only when ReadOnly is false. The MCP spec
	// defaults it to true when the server sends no hint, which is the safe
	// reading and the one published.
	Destructive bool
	// Idempotent is meaningful only when ReadOnly is false.
	Idempotent bool
	// OpenWorld reports whether the tool may reach beyond this instance. The
	// spec defaults it to true when absent.
	OpenWorld bool

	Inputs []ToolInput
}

// ToolInput is one top-level parameter. Nested object properties are not
// flattened — a JSON Schema is the precise artefact and a table is not the place
// to restate one, so a nested input is published with its own type and
// description and a client reads the schema for the rest.
type ToolInput struct {
	Name        string
	Type        string
	Description string
	Required    bool
}

// DescribeTools reports every tool this server exposes, as the server itself
// reports them over MCP.
//
// The server is constructed with no query backend and an empty dashboard
// service because registration touches neither: the tool set is fixed at
// construction and no handler runs here. A nil dashboard service would register
// no dashboard tools at all, so one is supplied — otherwise this would quietly
// document a smaller surface than an instance serves.
func DescribeTools(ctx context.Context) ([]ToolDoc, error) {
	// Bounded so a handshake that never completes fails the build instead of
	// hanging it. The transport is in-memory and this should take microseconds;
	// the timeout is a backstop, not a budget.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	server := New(nil, dashboard.New(nil), "docgen")

	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	type connection struct {
		session *mcp.ServerSession
		err     error
	}
	// Buffered, so the goroutine cannot block if this returns before reading it.
	connected := make(chan connection, 1)
	go func() {
		session, err := server.MCP().Connect(ctx, serverTransport, nil)
		connected <- connection{session: session, err: err}
	}()

	client := mcp.NewClient(&mcp.Implementation{Name: "fanout-docgen", Version: "docgen"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		// The goroutine may still have produced a session. Closing it here is the
		// same leak the deferred close below exists to prevent — this path is just
		// the one that returns before reaching it.
		if served := <-connected; served.session != nil {
			_ = served.session.Close()
		}
		return nil, fmt.Errorf("connecting to the MCP server: %w", err)
	}
	defer func() { _ = session.Close() }()

	served := <-connected
	if served.session != nil {
		// Closed explicitly rather than left to the client's close: this is called
		// repeatedly by tests, and a server session per call would accumulate.
		defer func() { _ = served.session.Close() }()
	}
	if served.err != nil {
		return nil, fmt.Errorf("serving the in-memory MCP transport: %w", served.err)
	}

	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("listing tools: %w", err)
	}
	if len(listed.Tools) == 0 {
		// An empty reference reads as "this instance exposes no tools", which is
		// a stronger and wronger claim than a missing page.
		return nil, fmt.Errorf("the MCP server reported no tools; has registration changed?")
	}

	docs := make([]ToolDoc, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		doc := ToolDoc{
			Name:        tool.Name,
			Title:       tool.Title,
			Description: tool.Description,
			// Absent hints default to true per the MCP spec. Reading them as
			// false would publish a mutating tool as safe.
			Destructive: true,
			OpenWorld:   true,
		}
		if a := tool.Annotations; a != nil {
			doc.ReadOnly = a.ReadOnlyHint
			doc.Idempotent = a.IdempotentHint
			if a.DestructiveHint != nil {
				doc.Destructive = *a.DestructiveHint
			}
			if a.OpenWorldHint != nil {
				doc.OpenWorld = *a.OpenWorldHint
			}
		}
		if doc.Description == "" {
			return nil, fmt.Errorf(
				"tool %s has no description; a calling model chooses tools by description, "+
					"so an empty one is a bug rather than a blank cell",
				tool.Name,
			)
		}
		if doc.Title == "" {
			// The reference renders the title as `**%s** — description`, so an
			// empty one publishes a heading line starting with a bare `****`.
			return nil, fmt.Errorf(
				"tool %s has no title; the reference renders one for every tool and an "+
					"empty one is published as stray emphasis",
				tool.Name,
			)
		}

		inputs, err := toolInputs(tool.Name, tool.InputSchema)
		if err != nil {
			return nil, err
		}
		doc.Inputs = inputs

		docs = append(docs, doc)
	}

	sort.Slice(docs, func(i, j int) bool { return docs[i].Name < docs[j].Name })
	return docs, nil
}

// toolInputs reads a tool's top-level input properties out of its generated
// JSON Schema.
//
// The schema arrives as a map rather than a typed value: Tool.InputSchema is
// `any`, and a client receives it having been through JSON. So this reads it as
// what it is, and refuses anything it does not recognise rather than treating an
// unreadable schema as "no inputs" — a tool published as taking nothing when it
// requires an argument is worse than no page.
func toolInputs(toolName string, raw any) ([]ToolInput, error) {
	if raw == nil {
		return nil, nil
	}
	schema, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf(
			"tool %s: input schema is %T, not a JSON object; the reference cannot describe its parameters",
			toolName, raw,
		)
	}

	properties, ok := schema["properties"]
	if !ok {
		// A tool that genuinely takes nothing, e.g. dashboard_list.
		return nil, nil
	}
	fields, ok := properties.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("tool %s: schema properties are %T, not a JSON object", toolName, properties)
	}

	required := map[string]bool{}
	if list, ok := schema["required"].([]any); ok {
		for _, name := range list {
			if s, ok := name.(string); ok {
				required[s] = true
			}
		}
	}

	inputs := make([]ToolInput, 0, len(fields))
	for name, raw := range fields {
		property, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf(
				"tool %s: input %q has schema %T, not a JSON object, so it would publish as a bare name",
				toolName, name, raw,
			)
		}
		description, _ := property["description"].(string)
		inputs = append(inputs, ToolInput{
			Name:        name,
			Type:        schemaType(property),
			Description: description,
			Required:    required[name],
		})
	}

	// Required first, then alphabetical: someone wiring up a call needs the
	// mandatory inputs before the optional ones.
	sort.Slice(inputs, func(i, j int) bool {
		if inputs[i].Required != inputs[j].Required {
			return inputs[i].Required
		}
		return inputs[i].Name < inputs[j].Name
	})
	return inputs, nil
}

// schemaType renders a property's type for a table cell.
//
// JSON Schema allows a type to be a string or a list, and the SDK emits a list
// for a nullable field — a nullable array arrives as ["null","array"]. The null
// is dropped because every optional input is nullable and saying so in every row
// carries no information; what remains is rendered as a union rather than
// letting the first member silently win.
func schemaType(property map[string]any) string {
	switch t := property["type"].(type) {
	case string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, entry := range t {
			name, ok := entry.(string)
			if !ok || name == "null" {
				continue
			}
			out = append(out, name)
		}
		if len(out) > 0 {
			return strings.Join(out, " or ")
		}
	}
	return "any"
}
