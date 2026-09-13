package workspace

import (
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/hollis-labs/tesseract/internal/memory"
)

const maxJSONBytes = 1 << 20

var schemaHashRE = regexp.MustCompile(`^[a-f0-9]{64}$`)

func validateCreate(in CreateInput) error {
	if in.WorkstreamID != nil {
		if err := memory.ValidateWorkstreamID(*in.WorkstreamID); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidInput, err)
		}
	}
	if err := memory.ValidateWorkspaceNamespace(in.Namespace); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	if in.Summary == "" {
		return fmt.Errorf("%w: summary is required", ErrInvalidInput)
	}
	if in.Author.AgentID == "" {
		return fmt.Errorf("%w: author.agent_id is required", ErrInvalidInput)
	}
	if in.SessionID == "" {
		return fmt.Errorf("%w: session_id is required", ErrInvalidInput)
	}
	if err := validateJSONObject("data", in.Data); err != nil {
		return err
	}
	if err := validateSchemaClaim(in.Data, in.DataSchemaHash); err != nil {
		return err
	}
	return validateJSONObject("consumer_state", in.ConsumerState)
}

func validateEdit(in EditInput) (map[ClearField]bool, error) {
	if in.ItemID == "" {
		return nil, fmt.Errorf("%w: item_id is required", ErrInvalidInput)
	}
	if in.VersionToken == "" {
		return nil, fmt.Errorf("%w: version_token is required", ErrInvalidInput)
	}
	if in.Author.AgentID == "" {
		return nil, fmt.Errorf("%w: author.agent_id is required", ErrInvalidInput)
	}
	if in.SessionID == "" {
		return nil, fmt.Errorf("%w: session_id is required", ErrInvalidInput)
	}

	clears := make(map[ClearField]bool, len(in.ClearFields))
	for _, field := range in.ClearFields {
		switch field {
		case ClearKey, ClearBody, ClearData, ClearTags, ClearConsumerState, ClearWorkstreamID:
		default:
			return nil, fmt.Errorf("%w: field %q cannot be cleared", ErrInvalidInput, field)
		}
		if clears[field] {
			return nil, fmt.Errorf("%w: clear field %q is repeated", ErrInvalidInput, field)
		}
		clears[field] = true
	}

	if in.Summary != nil && *in.Summary == "" {
		return nil, fmt.Errorf("%w: summary cannot be empty", ErrInvalidInput)
	}
	if in.Key != nil && *in.Key == "" {
		return nil, fmt.Errorf("%w: empty key is omitted; use clear_fields to remove it", ErrInvalidInput)
	}
	if in.Body != nil && *in.Body == "" {
		return nil, fmt.Errorf("%w: empty body is omitted; use clear_fields to remove it", ErrInvalidInput)
	}
	if in.Data != nil {
		if len(*in.Data) == 0 {
			return nil, fmt.Errorf("%w: empty data is omitted; use clear_fields to remove it", ErrInvalidInput)
		}
		if err := validateJSONObject("data", *in.Data); err != nil {
			return nil, err
		}
	}
	if in.ConsumerState != nil {
		if err := validatePresentJSONObject("consumer_state", *in.ConsumerState); err != nil {
			return nil, err
		}
	}
	if in.DataSchemaHash != nil {
		if in.Data == nil {
			return nil, fmt.Errorf("%w: data_schema_hash cannot be changed without data", ErrInvalidInput)
		}
		if err := validateSchemaClaim(*in.Data, *in.DataSchemaHash); err != nil {
			return nil, err
		}
	}
	if in.WorkstreamID != nil {
		if err := memory.ValidateWorkstreamID(*in.WorkstreamID); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidInput, err)
		}
	}

	for field, supplied := range map[ClearField]bool{
		ClearKey:           in.Key != nil,
		ClearBody:          in.Body != nil,
		ClearData:          in.Data != nil || in.DataSchemaHash != nil,
		ClearTags:          in.Tags != nil,
		ClearConsumerState: in.ConsumerState != nil,
		ClearWorkstreamID:  in.WorkstreamID != nil,
	} {
		if supplied && clears[field] {
			return nil, fmt.Errorf("%w: field %q cannot be supplied and cleared", ErrInvalidInput, field)
		}
	}

	if in.Key == nil && in.Summary == nil && in.Body == nil && in.Data == nil &&
		in.DataSchemaHash == nil && in.Tags == nil && in.ConsumerState == nil && in.WorkstreamID == nil && len(clears) == 0 {
		return nil, fmt.Errorf("%w: edit has no editable fields", ErrInvalidInput)
	}
	return clears, nil
}

func validateJSONObject(field string, raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	if len(raw) > maxJSONBytes {
		return fmt.Errorf("%w: %s is %d bytes, over the %d byte ceiling", ErrInvalidInput, field, len(raw), maxJSONBytes)
	}
	if !json.Valid(raw) {
		return fmt.Errorf("%w: %s must be well-formed JSON", ErrInvalidInput, field)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return fmt.Errorf("%w: %s must be a JSON object, not an array or scalar", ErrInvalidInput, field)
	}
	if object == nil {
		return fmt.Errorf("%w: %s must be a JSON object; omit it or send {}", ErrInvalidInput, field)
	}
	return nil
}

func validatePresentJSONObject(field string, raw json.RawMessage) error {
	if len(raw) == 0 {
		return fmt.Errorf("%w: empty %s is omitted; use clear_fields to remove it", ErrInvalidInput, field)
	}
	return validateJSONObject(field, raw)
}

func validateSchemaClaim(data json.RawMessage, hash string) error {
	if hash == "" {
		return nil
	}
	if len(data) == 0 {
		return fmt.Errorf("%w: data_schema_hash requires data", ErrInvalidInput)
	}
	if !schemaHashRE.MatchString(hash) {
		return fmt.Errorf("%w: data_schema_hash must be a lowercase hex sha256 digest", ErrInvalidInput)
	}
	return nil
}
