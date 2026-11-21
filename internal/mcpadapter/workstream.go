package mcpadapter

import (
	"github.com/hollis-labs/tesseract/internal/memory"
)

func workstreamWriteArg(req map[string]any) (*string, any) {
	raw, present := req["workstream_id"]
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

// workstreamReadArg distinguishes an omitted selector from a malformed one.
// GetString defaults every non-string value to "", which would otherwise turn
// null, booleans, numbers, objects, and arrays into an unfiltered read.
func workstreamReadArg(req map[string]any) (string, any) {
	raw, present := req["workstream_id"]
	if !present {
		return "", nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", toolError(codeValidationError, "workstream_id filter must be a string")
	}
	if err := memory.ValidateWorkstreamFilter(value); err != nil {
		return "", toolError(codeValidationError, err.Error())
	}
	return value, nil
}

// eventWorkstreamReadArg is event-list's additive scalar-or-list contract.
// Other read tools retain their scalar association argument.
func eventWorkstreamReadArg(req map[string]any) ([]string, any) {
	raw, present := req["workstream_id"]
	if !present {
		return nil, nil
	}
	if value, ok := raw.(string); ok {
		values, err := memory.ParseEventWorkstreamSelectors([]string{value})
		if err != nil {
			return nil, toolError(codeValidationError, err.Error())
		}
		return values, nil
	}
	values, _, err := parseStringArrayArg(req, "workstream_id")
	if err != nil || values == nil || len(values) == 0 {
		return nil, toolError(codeValidationError, "workstream_id must be a non-empty string or array of strings")
	}
	checked, checkErr := memory.NormalizeEventWorkstreamIDs(values)
	if checkErr != nil {
		return nil, toolError(codeValidationError, checkErr.Error())
	}
	return checked, nil
}
