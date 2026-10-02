package mcpadapter

import (
	"strings"

	gomcpserver "github.com/hollis-labs/go-mcp/server"
)

// propDef is one property of a tool's input schema, plus whether it belongs
// in the schema's `required` list. go-mcp's server.Tool.InputSchema is `any`
// (anything that JSON-marshals to valid JSON Schema) with no functional-option
// builder of its own, so this file replaces mark3labs' mcp.WithString /
// mcp.WithNumber / mcp.WithBoolean / mcp.Required / mcp.Description chain
// with the minimal shape every tool registration in this package actually
// uses: string, number and boolean properties, each with a description and
// an optional required flag. Nothing here is more general than that; add to
// it only when a tool actually needs a shape it doesn't cover.
type propDef struct {
	name     string
	schema   map[string]any
	required bool
}

// strProp declares a string property.
func strProp(name, desc string, required bool) propDef {
	return propDef{name: name, schema: map[string]any{"type": "string", "description": desc}, required: required}
}

// numProp declares a number property.
func numProp(name, desc string, required bool) propDef {
	return propDef{name: name, schema: map[string]any{"type": "number", "description": desc}, required: required}
}

// boolProp declares a boolean property. required stays a parameter for
// symmetry with strProp/numProp (every call site today passes false — no
// tool in this package has a required boolean argument — but the triplet's
// call sites are meant to read the same way).
func boolProp(name, desc string, required bool) propDef { //nolint:unparam // kept for signature symmetry with strProp/numProp
	return propDef{name: name, schema: map[string]any{"type": "boolean", "description": desc}, required: required}
}

// inputSchema builds a tool's whole input schema from its property defs,
// via go-mcp's own strict ObjectSchema helper (type object, additionalProperties
// false). Called with no defs, it produces the strict empty-object schema a
// no-argument tool declares.
func inputSchema(defs ...propDef) map[string]any {
	properties := make(map[string]any, len(defs))
	var required []string
	for _, d := range defs {
		properties[d.name] = d.schema
		if d.required {
			required = append(required, d.name)
		}
	}
	return gomcpserver.ObjectSchema(properties, required...)
}

// toolAnnotations is the zero-friendly subset of go-mcp's four required tool
// hints. Its zero value (all false) is what a tool gets when it declares none
// of them explicitly, matching the mark3labs tools that called none of the
// WithXHintAnnotation options: those advertised no hints at all on the wire,
// but go-mcp's contract requires every hint be stated explicitly, so "unset"
// now reads as false rather than absent — an intended consequence of adopting
// go-mcp's required-annotation contract, not a behavior this package chose.
type toolAnnotations struct {
	ReadOnlyHint    bool
	DestructiveHint bool
	IdempotentHint  bool
	OpenWorldHint   bool
}

// gomcpTool builds a go-mcp server.Tool from the pieces every registration in
// this package supplies, replacing mark3labs' mcp.NewTool(...) call.
func gomcpTool(name, description string, schema map[string]any, ann toolAnnotations, handler gomcpserver.ToolHandler) gomcpserver.Tool {
	words := strings.Split(name, "_")
	for i, word := range words {
		if word != "" {
			words[i] = strings.ToUpper(word[:1]) + word[1:]
		}
	}
	return gomcpserver.Tool{
		Title:           strings.Join(words, " "),
		Name:            name,
		Description:     description,
		InputSchema:     schema,
		ReadOnlyHint:    ann.ReadOnlyHint,
		DestructiveHint: ann.DestructiveHint,
		IdempotentHint:  ann.IdempotentHint,
		OpenWorldHint:   ann.OpenWorldHint,
		Handler:         handler,
	}
}
