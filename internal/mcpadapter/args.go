package mcpadapter

import (
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

// parseStringArrayArg extracts a JSON-array-of-strings argument from an MCP
// request, accepting BOTH a native JSON array (sent by clients that don't
// stringify, including the mux MCP proxy) and a JSON-encoded string (sent by
// clients that honor the WithString schema declaration).
//
// Returns (values, present, error). `present` is true whenever the key exists
// in the request arguments; this lets callers distinguish "unset" from
// "deliberately empty list" if needed.
//
// Schemas in this package declare these arrays as `mcp.WithString` for
// historical reasons. Without this helper the native-array path silently
// drops the value because `req.GetString` returns "" for non-string args
// (mark3labs/mcp-go v0.47: tools.go GetString casts via val.(string)).
func parseStringArrayArg(req mcp.CallToolRequest, key string) ([]string, bool, error) {
	args := req.GetArguments()
	raw, ok := args[key]
	if !ok || raw == nil {
		return nil, false, nil
	}
	switch v := raw.(type) {
	case []string:
		return v, true, nil
	case []any:
		out := make([]string, 0, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, true, fmt.Errorf("item %d is not a string", i)
			}
			out = append(out, s)
		}
		return out, true, nil
	case string:
		if v == "" {
			return nil, false, nil
		}
		var out []string
		if err := json.Unmarshal([]byte(v), &out); err != nil {
			return nil, true, fmt.Errorf("must be a JSON array of strings: %w", err)
		}
		return out, true, nil
	}
	return nil, true, fmt.Errorf("must be a string or JSON array of strings")
}
