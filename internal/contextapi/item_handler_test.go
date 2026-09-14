package contextapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/event"
	"github.com/hollis-labs/tesseract/internal/memory"
)

func getItemRoute(t *testing.T, srv *Server, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	return rr
}

func TestItemRoutesReadKeylessEventCurrentAndHistory(t *testing.T) {
	srv := newEventTestServer(t)
	written, err := srv.EventStore.Write(context.Background(), event.WriteInput{
		Namespace: "user/chrispian/event/reasoning",
		Summary:   "keyless event",
		Author:    memory.Author{AgentID: "test"},
		SessionID: "session:item-route",
	})
	if err != nil {
		t.Fatal(err)
	}
	before, err := srv.MemoryStore.GetState(context.Background(), written.ItemID)
	if err != nil {
		t.Fatal(err)
	}

	currentResponse := getItemRoute(t, srv, "/v1/items/"+written.ItemID, nil)
	if currentResponse.Code != http.StatusOK {
		t.Fatalf("current status = %d; body=%s", currentResponse.Code, currentResponse.Body.String())
	}
	var current memory.Revision
	if decodeErr := json.Unmarshal(currentResponse.Body.Bytes(), &current); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if current.ItemID != written.ItemID || current.MemoryID != written.ItemID || current.MemoryKey != "" || current.Domain != domains.Event {
		t.Fatalf("current identity/domain = %#v", current)
	}

	historyResponse := getItemRoute(t, srv, "/v1/items/"+written.ItemID+"/history", nil)
	if historyResponse.Code != http.StatusOK {
		t.Fatalf("history status = %d; body=%s", historyResponse.Code, historyResponse.Body.String())
	}
	var history []memory.Revision
	if decodeErr := json.Unmarshal(historyResponse.Body.Bytes(), &history); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if len(history) != 1 || history[0].ItemID != written.ItemID || history[0].RevisionID != written.RevisionID {
		t.Fatalf("history = %#v", history)
	}

	after, err := srv.MemoryStore.GetState(context.Background(), written.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if after.AccessCount != before.AccessCount {
		t.Fatalf("event item read reinforced access_count: before=%d after=%d", before.AccessCount, after.AccessCount)
	}
}

func TestItemCurrentReinforcesMemoryButHistoryDoesNot(t *testing.T) {
	srv := newMemoryTestServer(t)
	written, err := srv.MemoryStore.WriteRevision(context.Background(), memory.WriteInput{
		Domain:      domains.Memory,
		Actor:       "user",
		Namespace:   "user/chrispian/memory/notes",
		Author:      memory.Author{AgentID: "test"},
		Trigger:     memory.TriggerExplicit,
		SessionID:   "session:item-route",
		DerivedFrom: memory.DerivedFromUser,
		Confidence:  0.9,
		Status:      memory.StatusCanonical,
		Summary:     "keyless memory",
	})
	if err != nil {
		t.Fatal(err)
	}

	before, _ := srv.MemoryStore.GetState(context.Background(), written.ItemID)
	if rr := getItemRoute(t, srv, "/v1/items/"+written.ItemID+"/history", nil); rr.Code != http.StatusOK {
		t.Fatalf("history status = %d; body=%s", rr.Code, rr.Body.String())
	}
	afterHistory, _ := srv.MemoryStore.GetState(context.Background(), written.ItemID)
	if afterHistory.AccessCount != before.AccessCount {
		t.Fatalf("history reinforced access_count: before=%d after=%d", before.AccessCount, afterHistory.AccessCount)
	}
	if rr := getItemRoute(t, srv, "/v1/items/"+written.ItemID, nil); rr.Code != http.StatusOK {
		t.Fatalf("current status = %d; body=%s", rr.Code, rr.Body.String())
	}
	afterCurrent, _ := srv.MemoryStore.GetState(context.Background(), written.ItemID)
	if afterCurrent.AccessCount != before.AccessCount+1 {
		t.Fatalf("current access_count = %d, want %d", afterCurrent.AccessCount, before.AccessCount+1)
	}
}

func TestItemReadAppliesResolvedNamespacePolicyBeforeReinforcement(t *testing.T) {
	srv := newMemoryTestServer(t)
	written, err := srv.MemoryStore.WriteRevision(context.Background(), memory.WriteInput{
		Domain:      domains.Memory,
		Actor:       "user",
		Namespace:   "user/chrispian/memory/notes",
		Author:      memory.Author{AgentID: "test"},
		Trigger:     memory.TriggerExplicit,
		SessionID:   "session:item-route",
		DerivedFrom: memory.DerivedFromUser,
		Confidence:  0.9,
		Status:      memory.StatusCanonical,
		Summary:     "private",
	})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := srv.MemoryStore.GetState(context.Background(), written.ItemID)

	srv.ManagedAuth = true
	token := issueTokenWithScopes(t, srv, "other-namespace", []string{"memory:read"}, []string{"app/other/*"})
	rr := getItemRoute(t, srv, "/v1/items/"+written.ItemID, map[string]string{"Authorization": "Bearer " + token})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
	var envelope map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope["code"] != "namespace_not_permitted" {
		t.Fatalf("code = %v", envelope["code"])
	}
	after, _ := srv.MemoryStore.GetState(context.Background(), written.ItemID)
	if after.AccessCount != before.AccessCount {
		t.Fatalf("denied read reinforced access_count: before=%d after=%d", before.AccessCount, after.AccessCount)
	}
}

func TestItemRoutesReturnNotFoundForUnknownID(t *testing.T) {
	srv := newMemoryTestServer(t)
	for _, suffix := range []string{"", "/history"} {
		rr := getItemRoute(t, srv, "/v1/items/01NEVEREXISTED"+suffix, nil)
		if rr.Code != http.StatusNotFound {
			t.Errorf("%q status = %d, want 404; body=%s", suffix, rr.Code, rr.Body.String())
		}
	}
}
