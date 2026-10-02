package mcpadapter

import (
	"context"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// toolFailure preserves the existing JSON refusal body while distinguishing it
// from successful data that happens to contain code/message fields.
type toolFailure map[string]any

// go-mcp mirrors handler values into StructuredContent without classifying
// application failures returned with a nil Go error. Set the protocol flag at
// the SDK boundary, after that conversion, for our explicit refusal type only.
// Ordinary Go errors already receive IsError from go-mcp and pass through.
func toolFailureMiddleware(next mcpsdk.MethodHandler) mcpsdk.MethodHandler {
	return func(ctx context.Context, method string, req mcpsdk.Request) (mcpsdk.Result, error) {
		result, err := next(ctx, method, req)
		if method == "tools/call" && err == nil {
			if call, ok := result.(*mcpsdk.CallToolResult); ok && call != nil {
				if _, failed := call.StructuredContent.(toolFailure); failed {
					call.IsError = true
				}
			}
		}
		return result, err
	}
}
