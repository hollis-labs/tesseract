package contextapi

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/tesseract/internal/memory"
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

// FilterValue preserves omission while rejecting every supplied value that
// cannot safely narrow a read. In particular, an explicit empty string cannot
// collapse to the same behavior as an omitted filter.
func (v presentString) FilterValue() (string, error) {
	if !v.present {
		return "", nil
	}
	if err := memory.ValidateWorkstreamFilter(v.value); err != nil {
		return "", err
	}
	return v.value, nil
}
