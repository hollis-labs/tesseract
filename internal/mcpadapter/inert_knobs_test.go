package mcpadapter

import (
	"context"
	"fmt"
	"testing"
)

func TestContextWriteRecordTypeContract(t *testing.T) {
	tests := []struct {
		name       string
		arguments  map[string]any
		wantRecord string
	}{
		{
			name: "explicit record type persists",
			arguments: map[string]any{
				"record_type": "project/state",
			},
			wantRecord: "project/state",
		},
		{
			name:       "omitted record type remains untyped",
			arguments:  map[string]any{},
			wantRecord: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore(t)
			a := New(s, writeToken(t, s, []string{"write"}, []string{"*"}))
			args := map[string]any{
				"namespace": "app/test/session/cw-0108",
				"key":       "state",
				"payload":   `{"phase":"test"}`,
			}
			for key, value := range tt.arguments {
				args[key] = value
			}

			wantNoError(t, mustCall(t, a.handleWrite, args))

			rec, err := s.Head(context.Background(), "app/test/session/cw-0108", "state")
			if err != nil {
				t.Fatalf("read written record: %v", err)
			}
			if rec.RecordType != tt.wantRecord {
				t.Errorf("record_type = %q, want %q", rec.RecordType, tt.wantRecord)
			}
		})
	}
}

func TestContextPlanBootProjectBumpsMaxItems(t *testing.T) {
	a := New(newTestStore(t), "")

	for _, tt := range []struct {
		name      string
		requested float64
		want      float64
	}{
		{name: "raises a low budget", requested: 25, want: 100},
		{name: "preserves a larger budget", requested: 125, want: 125},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := wantNoError(t, mustCall(t, a.handleContextPlan, map[string]any{
				"intent":    "boot_project",
				"max_items": tt.requested,
			}))
			plan := body["plan"].(map[string]any)
			budget := plan["budget"].(map[string]any)
			if got := budget["max_items"]; got != tt.want {
				t.Errorf("plan budget max_items = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestContextPlanBootProjectBumpControlsExecuteArm(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 2; i++ {
		writeRecord(t, s, "user/memory/cw-0108", fmt.Sprintf("item-%d", i), `{"phase":"test"}`)
	}
	a := New(s, "")

	body := wantNoError(t, mustCall(t, a.handleContextPlan, map[string]any{
		"execute":   true,
		"intent":    "boot_project",
		"max_items": float64(1),
	}))
	if got := len(parseItems(t, body)); got != 2 {
		t.Fatalf("executed plan returned %d items, want 2 after boot_project budget bump", got)
	}
	manifest := body["manifest"].(map[string]any)
	if got := manifest["truncated"]; got != false {
		t.Errorf("manifest truncated = %v, want false", got)
	}
}
