package wikilink_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/hollis-labs/tesseract/internal/wikilink"
)

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want []wikilink.Link
	}{
		{"empty", "", nil},
		{"no links", "plain prose with no brackets", nil},
		{"single bracket is not a link", "an [array] index", nil},
		{
			"bare target",
			"see [[tesseract_event_is_a_domain]] for why",
			[]wikilink.Link{{Target: "tesseract_event_is_a_domain", Kind: wikilink.KindWiki, Position: 0}},
		},
		{
			"target and label",
			"see [[handle|label]]",
			[]wikilink.Link{{Target: "handle", Label: "label", Kind: wikilink.KindWiki, Position: 0}},
		},
		{
			"whitespace around target and label is trimmed",
			"[[  spaced_key  |  Spaced Label  ]]",
			[]wikilink.Link{{Target: "spaced_key", Label: "Spaced Label", Kind: wikilink.KindWiki, Position: 0}},
		},
		{
			"several links keep document order",
			"[[a]] then [[b]] then [[c]]",
			[]wikilink.Link{
				{Target: "a", Kind: wikilink.KindWiki, Position: 0},
				{Target: "b", Kind: wikilink.KindWiki, Position: 1},
				{Target: "c", Kind: wikilink.KindWiki, Position: 2},
			},
		},
		{
			// Position is what distinguishes them, so collapsing would lose data.
			"repeats are not collapsed",
			"[[same]] and [[same]]",
			[]wikilink.Link{
				{Target: "same", Kind: wikilink.KindWiki, Position: 0},
				{Target: "same", Kind: wikilink.KindWiki, Position: 1},
			},
		},
		{
			"dotted legacy keys parse as ordinary targets",
			"[[decisions.tether.platform_reshape.cross_app_design_lock]]",
			[]wikilink.Link{{
				Target:   "decisions.tether.platform_reshape.cross_app_design_lock",
				Kind:     wikilink.KindWiki,
				Position: 0,
			}},
		},
		{"empty target is not a link", "[[]] and [[   ]]", nil},
		{"empty target with label is not a link", "[[|label]]", nil},
		{
			"unterminated opener is dropped, not greedy",
			"[[never_closed and a lot of following prose",
			nil,
		},
		{
			"an unterminated opener does not swallow a later real link",
			"[[never_closed then [[real_key]]",
			[]wikilink.Link{{Target: "real_key", Kind: wikilink.KindWiki, Position: 0}},
		},
		{
			"newline inside brackets is not a link",
			"[[first\nsecond]]",
			nil,
		},
		{
			// The `{` case: kept deliberately. It stays unresolved on its own.
			"prompt-shaped target is retained as written",
			`[[UI_PROMPT:{"kind":"yes_no"}]]`,
			[]wikilink.Link{{Target: `UI_PROMPT:{"kind":"yes_no"}`, Kind: wikilink.KindWiki, Position: 0}},
		},
		{
			"link at the very start and very end",
			"[[a]] middle [[b]]",
			[]wikilink.Link{
				{Target: "a", Kind: wikilink.KindWiki, Position: 0},
				{Target: "b", Kind: wikilink.KindWiki, Position: 1},
			},
		},
		{
			"triple bracket",
			"[[[weird]]]",
			[]wikilink.Link{{Target: "[weird", Kind: wikilink.KindWiki, Position: 0}},
		},
		{
			"only the first pipe splits",
			"[[key|a|b]]",
			[]wikilink.Link{{Target: "key", Label: "a|b", Kind: wikilink.KindWiki, Position: 0}},
		},
		{
			"empty label after pipe",
			"[[key|]]",
			[]wikilink.Link{{Target: "key", Label: "", Kind: wikilink.KindWiki, Position: 0}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := wikilink.Parse(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Parse(%q)\n got: %+v\nwant: %+v", tc.in, got, tc.want)
			}
		})
	}
}

// Position must count occurrences, not survive gaps left by rejected spans.
// A rejected `[[]]` between two real links would otherwise leave a hole, and a
// consumer ordering by position would see 0, 2 and read it as a missing row.
func TestParse_PositionIsDenseAcrossRejectedSpans(t *testing.T) {
	got := wikilink.Parse("[[a]] [[]] [[b]]")
	want := []wikilink.Link{
		{Target: "a", Kind: wikilink.KindWiki, Position: 0},
		{Target: "b", Kind: wikilink.KindWiki, Position: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// The scan must not be quadratic on pathological input: a body of unterminated
// openers is a plausible thing to paste into a memory, and this runs on every
// write.
func TestParse_ManyUnterminatedOpenersTerminates(t *testing.T) {
	got := wikilink.Parse(strings.Repeat("[[", 50_000))
	if got != nil {
		t.Errorf("expected no links from unterminated openers, got %d", len(got))
	}
}

func TestParse_RealCorpusShape(t *testing.T) {
	// Verbatim from tesseract_vnext_phase_order's body — the shape this
	// actually has to handle.
	body := "Related: [[tesseract_vnext_target_architecture]], [[tesseract_event_is_a_domain]], " +
		"[[tesseract_type_registry_promoted]], [[tesseract_domain_extension_blast_radius]], " +
		"[[tesseract_novelty_gating_lead]]."

	got := wikilink.Parse(body)
	if len(got) != 5 {
		t.Fatalf("expected 5 links, got %d: %+v", len(got), got)
	}
	if got[0].Target != "tesseract_vnext_target_architecture" {
		t.Errorf("first target = %q", got[0].Target)
	}
	if got[4].Target != "tesseract_novelty_gating_lead" {
		t.Errorf("last target = %q", got[4].Target)
	}
	for i, l := range got {
		if l.Position != i {
			t.Errorf("link %d has position %d", i, l.Position)
		}
		if l.Kind != wikilink.KindWiki {
			t.Errorf("link %d has kind %q", i, l.Kind)
		}
	}
}
