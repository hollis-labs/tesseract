package contextapi

import (
	"context"
	"net/http"
	"slices"
	"testing"

	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	llmtypes "github.com/hollis-labs/go-llm-types"
)

// unusedSynthesisProvider stands in for a configured LLM provider so
// /v1/synthesis/ask gets past its 503 guard and reaches the decoder. Every
// method fails the test: a body refused at decode time must not cost a
// completion, and this route is the only one on this surface where a request
// accepted by mistake spends real money.
type unusedSynthesisProvider struct{ t *testing.T }

var _ llmcontracts.Provider = unusedSynthesisProvider{}

func (p unusedSynthesisProvider) StreamChat(context.Context, llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	p.t.Error("StreamChat called: /v1/synthesis/ask does not stream")
	return nil, nil
}

func (p unusedSynthesisProvider) Complete(context.Context, llmtypes.ChatRequest) (string, error) {
	p.t.Error("Complete called: a rejected body must never reach the provider")
	return "", nil
}

func (p unusedSynthesisProvider) Capabilities() llmtypes.ProviderCapabilities {
	return llmtypes.ProviderCapabilities{}
}

func newSynthesisTestServer(t *testing.T) *Server {
	t.Helper()
	srv := newMemoryTestServer(t)
	srv.SynthesisProvider = unusedSynthesisProvider{t: t}
	return srv
}

// TestSynthesisAsk_UnknownFieldRejectedBeforeTheProvider: this route names its
// input `question`, while every recall-shaped route beside it calls the same
// idea `query`. The lenient decoder turned that slip into an empty question
// and a "question is required" error that never mentioned the field the caller
// had sent.
func TestSynthesisAsk_UnknownFieldRejectedBeforeTheProvider(t *testing.T) {
	srv := newSynthesisTestServer(t)

	body := `{"query":"why is recall slow","namespaces":["user/chrispian/memory/notes"]}`
	env := mustRejectUnknownField(t, srv, "/v1/synthesis/ask", body, "query")
	if !slices.Contains(env.Details.AcceptedFields, "question") {
		t.Errorf("accepted_fields does not list question, the field they meant: %v",
			env.Details.AcceptedFields)
	}
}

// TestSynthesisAsk_CanonicalBodyStillDecodes is the regression half. The
// provider is never reached because the seeded corpus returns no results, so
// the handler takes its deterministic no-sources path — which is enough to
// prove the whole body decoded.
func TestSynthesisAsk_CanonicalBodyStillDecodes(t *testing.T) {
	srv := newSynthesisTestServer(t)

	body := `{
		"question":"why is recall slow",
		"namespaces":["user/chrispian/memory/notes"],
		"tags":["perf"],
		"limit":5,
		"domains":["memory","knowledge"],
		"statuses":["canonical"],
		"confidence_min":0.5,
		"model":"claude-sonnet-4"
	}`
	rr := postRawJSON(t, srv, "/v1/synthesis/ask", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}
