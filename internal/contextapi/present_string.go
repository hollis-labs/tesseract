package contextapi

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// presentString distinguishes omission from an explicit empty string and
// rejects JSON null rather than silently treating it as omission.
type presentString struct {
	present bool
	value   string
}

func (v *presentString) UnmarshalJSON(raw []byte) error {
	v.present = true
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return fmt.Errorf("workstream_id must be a string; null is not omission")
	}
	return json.Unmarshal(raw, &v.value)
}

func (v presentString) Pointer() *string {
	if !v.present {
		return nil
	}
	value := v.value
	return &value
}
