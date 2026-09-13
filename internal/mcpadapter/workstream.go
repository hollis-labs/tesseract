package mcpadapter

import (
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/mark3labs/mcp-go/mcp"
)

func workstreamWriteArg(req mcp.CallToolRequest) (*string, *mcp.CallToolResult) {
	raw, present := req.GetArguments()["workstream_id"]
	if !present {
		return nil, nil
	}
	value, ok := raw.(string)
	if !ok {
		return nil, toolError(codeValidationError, "workstream_id must be a string")
	}
	if err := memory.ValidateWorkstreamID(value); err != nil {
		return nil, toolError(codeValidationError, err.Error())
	}
	return &value, nil
}
