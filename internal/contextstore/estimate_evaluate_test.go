package contextstore

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

func seedStoreWithRecords(t *testing.T, count int) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(context.Background(), Config{RootDir: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	for i := 0; i < count; i++ {
		payload := json.RawMessage([]byte(`{"n":` + fmt.Sprintf("%d", i) + `}`))
		if _, err := s.AppendRecord(context.Background(), AppendInput{
			Namespace: "app/estimate/session",
			Key:       fmt.Sprintf("rec-%04d", i),
			Actor:     "app:test",
			Payload:   payload,
		}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	return s
}

func TestEstimate_ReturnsAggregateCounts(t *testing.T) {
	s := seedStoreWithRecords(t, 5)
	result, err := s.Estimate(context.Background(), Selector{
		Namespaces: []string{"app/estimate/session"},
	})
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if result.RecordCount != 5 {
		t.Errorf("RecordCount = %d, want 5", result.RecordCount)
	}
	if result.TotalBytes == 0 {
		t.Error("TotalBytes = 0, want > 0")
	}
	wantTokens := (result.TotalBytes + 3) / 4
	if result.TotalTokensEstimate != wantTokens {
		t.Errorf("TotalTokensEstimate = %d, want %d (bytes/4 round up)", result.TotalTokensEstimate, wantTokens)
	}
}

func TestEstimate_NotCappedByDefaultSelectLimit(t *testing.T) {
	// DefaultSelectLimit is 200 and Select is hard-capped at MaxSelectLimit
	// (500). Estimate must count the full matching set regardless — that's
	// the difference between "what would Select return" and "how big is
	// this selector?" Seeding more than DefaultSelectLimit catches any
	// regression where Estimate accidentally inherits Select's cap.
	const seed = DefaultSelectLimit + 50
	s := seedStoreWithRecords(t, seed)
	result, err := s.Estimate(context.Background(), Selector{
		Namespaces: []string{"app/estimate/session"},
	})
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if result.RecordCount != seed {
		t.Errorf("RecordCount = %d, want %d (Estimate must not inherit Select limit cap)", result.RecordCount, seed)
	}
}

func TestEvaluate_AppliesDefaults(t *testing.T) {
	s := seedStoreWithRecords(t, 3)
	result, err := s.Evaluate(context.Background(), Selector{
		Namespaces: []string{"app/estimate/session"},
	}, false, 0)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if result.MatchedCount != 3 {
		t.Errorf("MatchedCount = %d, want 3", result.MatchedCount)
	}
	if len(result.Items) != 3 {
		t.Errorf("len(Items) = %d, want 3", len(result.Items))
	}
	for _, item := range result.Items {
		if item.Payload != nil {
			t.Errorf("Payload not stripped when includePayload=false: %s", string(item.Payload))
		}
	}
	wantSort := []string{"namespace", "key", "revision"}
	if len(result.SortKeys) != len(wantSort) {
		t.Fatalf("SortKeys = %v, want %v", result.SortKeys, wantSort)
	}
	for i, k := range wantSort {
		if result.SortKeys[i] != k {
			t.Errorf("SortKeys[%d] = %q, want %q", i, result.SortKeys[i], k)
		}
	}
	if result.NormalizedScope != "head" {
		t.Errorf("NormalizedScope = %q, want head", result.NormalizedScope)
	}
}

func TestEvaluate_IncludePayloadTrueKeepsPayload(t *testing.T) {
	s := seedStoreWithRecords(t, 2)
	result, err := s.Evaluate(context.Background(), Selector{
		Namespaces: []string{"app/estimate/session"},
	}, true, 0)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	for _, item := range result.Items {
		if item.Payload == nil {
			t.Errorf("Payload stripped when includePayload=true, record %s", item.Key)
		}
	}
}

func TestEvaluate_LimitOverride(t *testing.T) {
	s := seedStoreWithRecords(t, 8)
	result, err := s.Evaluate(context.Background(), Selector{
		Namespaces: []string{"app/estimate/session"},
	}, false, 3)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if result.MatchedCount != 3 {
		t.Errorf("MatchedCount = %d, want 3 (clamped by limit)", result.MatchedCount)
	}
	if !result.Truncated {
		t.Error("Truncated = false, want true when results == limit")
	}
}

func TestNormalizedScope(t *testing.T) {
	cases := map[string]string{
		"":      "head",
		"head":  "head",
		"all":   "all",
		"ALL":   "all",
		" all ": "all",
		"other": "head",
	}
	for in, want := range cases {
		if got := NormalizedScope(in); got != want {
			t.Errorf("NormalizedScope(%q) = %q, want %q", in, got, want)
		}
	}
}
