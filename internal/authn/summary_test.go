package authn

import (
	"bytes"
	"strings"
	"testing"

	auth "github.com/hollis-labs/libs/security/auth"
)

func TestSummarySeparatesAndLogsDenialReasons(t *testing.T) {
	s := NewSummary()
	audit := s.Audit()
	event := auth.Event{Tool: "tesseract_get", Authentication: auth.StateVerified, PrincipalID: "caller", WouldDeny: true}
	event.Reason = "namespace_not_permitted"
	audit(event)
	audit(event)
	event.Reason = "insufficient_scope"
	audit(event)

	rows, dropped := s.Snapshot()
	counts := map[string]int{}
	for _, row := range rows {
		counts[row.Reason] = row.Count
	}
	if len(rows) != 2 || counts["namespace_not_permitted"] != 2 || counts["insufficient_scope"] != 1 || dropped != 0 {
		t.Fatalf("denials must aggregate independently by reason: rows=%+v dropped=%d", rows, dropped)
	}

	var out bytes.Buffer
	s.flush(&out, false)
	for _, want := range []string{
		"2x mcp tesseract_get auth=verified who=caller reason=namespace_not_permitted",
		"1x mcp tesseract_get auth=verified who=caller reason=insufficient_scope",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("summary output missing %q: %s", want, out.String())
		}
	}

	out.Reset()
	s.flush(&out, false)
	if out.Len() != 0 {
		t.Fatalf("unchanged reason counts must not emit another summary: %s", out.String())
	}
	audit(event)
	s.flush(&out, false)
	if !strings.Contains(out.String(), "2x mcp tesseract_get auth=verified who=caller reason=insufficient_scope") {
		t.Fatalf("changed reason count must emit the updated summary: %s", out.String())
	}
}
