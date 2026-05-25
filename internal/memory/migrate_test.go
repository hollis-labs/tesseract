package memory

import (
	"reflect"
	"strings"
	"testing"
)

func TestMapRow_TypeStripAndProjectExtraction(t *testing.T) {
	projectSet := map[string]struct{}{
		"tesseract": {},
		"nanite":    {},
		"agent_ops": {},
	}
	cases := []struct {
		name      string
		oldNS     string
		oldKey    string
		oldTags   []string
		wantNS    string
		wantKey   string
		wantTags  []string
		wantNote  string // substring of Reason
	}{
		{
			name:     "decisions with project and ticket",
			oldNS:    "user/chrispian/memory",
			oldKey:   "decisions.nanite.cw_20260420_0006.foo",
			wantNS:   "user/chrispian/memory/decisions",
			wantKey:  "foo",
			wantTags: []string{"project:nanite", "ticket:cw_20260420_0006"},
			wantNote: "stripped",
		},
		{
			name:     "decisions with project only",
			oldNS:    "user/chrispian/memory",
			oldKey:   "decisions.tesseract.reinforce_on_read.deliberate",
			wantNS:   "user/chrispian/memory/decisions",
			wantKey:  "reinforce_on_read.deliberate",
			wantTags: []string{"project:tesseract"},
		},
		{
			name:     "singular reference -> references; project not lifted when not in set",
			oldNS:    "user/chrispian/memory",
			oldKey:   "reference.atlas.weekly_review",
			wantNS:   "user/chrispian/memory/references",
			wantKey:  "atlas.weekly_review", // atlas not in project set, so not lifted
			wantTags: nil,
			wantNote: "normalized-type-prefix-reference-to-references",
		},
		{
			name:     "unknown prefix -> notes bucket; key preserved",
			oldNS:    "user/chrispian/memory",
			oldKey:   "audit.something.foo",
			wantNS:   "user/chrispian/memory/notes",
			wantKey:  "audit.something.foo",
			wantTags: nil,
			wantNote: "no-known-type-prefix",
		},
		{
			name:     "long single-segment key (no dots) -> notes bucket",
			oldNS:    "user/chrispian/memory",
			oldKey:   "user_profile_multi_llm_portfolio",
			wantNS:   "user/chrispian/memory/notes",
			wantKey:  "user_profile_multi_llm_portfolio",
			wantTags: nil,
		},
		{
			name:     "decisions with agent_ops project",
			oldNS:    "user/chrispian/memory",
			oldKey:   "decisions.agent_ops.framing_vs_agent_os",
			wantNS:   "user/chrispian/memory/decisions",
			wantKey:  "framing_vs_agent_os",
			wantTags: []string{"project:agent_ops"},
		},
		{
			name:     "ticket-only after type (no project)",
			oldNS:    "user/chrispian/memory",
			oldKey:   "followups.cw_20260101_0001.do_thing",
			wantNS:   "user/chrispian/memory/followups",
			wantKey:  "do_thing",
			wantTags: []string{"ticket:cw_20260101_0001"},
		},
		{
			name:     "project namespace gets type appended",
			oldNS:    "user/chrispian/project/nanite/memory",
			oldKey:   "decisions.cw_20260101_0001.fix",
			wantNS:   "user/chrispian/project/nanite/memory/decisions",
			wantKey:  "fix",
			wantTags: []string{"ticket:cw_20260101_0001"},
		},
		{
			name:     "session namespace gets type appended",
			oldNS:    "user/default/session/sess1/memory",
			oldKey:   "notes.something",
			wantNS:   "user/default/session/sess1/memory/notes",
			wantKey:  "something",
		},
		{
			name:     "existing tags are preserved + deduped",
			oldNS:    "user/chrispian/memory",
			oldKey:   "decisions.nanite.foo",
			oldTags:  []string{"important", "project:nanite"}, // project:nanite already present
			wantNS:   "user/chrispian/memory/decisions",
			wantKey:  "foo",
			wantTags: []string{"important", "project:nanite"},
		},
		{
			name:     "keyless row -> notes bucket, empty key",
			oldNS:    "user/chrispian/memory",
			oldKey:   "",
			wantNS:   "user/chrispian/memory/notes",
			wantKey:  "",
			wantNote: "empty-key",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := mapRow(tc.oldNS, tc.oldKey, tc.oldTags, projectSet)
			if row.NewNamespace != tc.wantNS {
				t.Errorf("NewNamespace = %q, want %q", row.NewNamespace, tc.wantNS)
			}
			if row.NewKey != tc.wantKey {
				t.Errorf("NewKey = %q, want %q", row.NewKey, tc.wantKey)
			}
			if !reflect.DeepEqual(row.NewTags, tc.wantTags) && !(row.NewTags == nil && len(tc.wantTags) == 0) {
				t.Errorf("NewTags = %v, want %v", row.NewTags, tc.wantTags)
			}
			if tc.wantNote != "" && !strings.Contains(row.Reason, tc.wantNote) {
				t.Errorf("Reason = %q, want substring %q", row.Reason, tc.wantNote)
			}
		})
	}
}

func TestStripTypePrefix(t *testing.T) {
	cases := []struct {
		key     string
		wantTyp string
		wantRes string
	}{
		{"decisions.foo.bar", "decisions", "foo.bar"},
		{"decision.foo", "decisions", "foo"},
		{"followup.bar", "followups", "bar"},
		{"reference.x", "references", "x"},
		{"unknown.thing", "notes", "unknown.thing"},
		{"singleword", "notes", "singleword"},
		{"", "notes", ""},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			typ, res, _ := stripTypePrefix(tc.key)
			if typ != tc.wantTyp || res != tc.wantRes {
				t.Errorf("stripTypePrefix(%q) = (%q, %q), want (%q, %q)",
					tc.key, typ, res, tc.wantTyp, tc.wantRes)
			}
		})
	}
}
