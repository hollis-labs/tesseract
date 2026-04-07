package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/hollis-labs/vanta-conduit/internal/contextapi"
	"github.com/hollis-labs/vanta-conduit/internal/contextpolicy"
	"github.com/hollis-labs/vanta-conduit/internal/contextstore"
)

func TestNamespacePolicyPersistsAcrossServerRestart(t *testing.T) {
	root := t.TempDir()

	s1, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: root})
	if err != nil {
		t.Fatalf("open s1: %v", err)
	}
	srv1 := contextapi.NewServer(s1, contextpolicy.New())

	reg := perform(t, srv1, http.MethodPost, "/v1/namespaces/register", map[string]any{
		"namespace":  "app/editor/session",
		"owner_type": "app",
		"owner_id":   "editor",
		"policy": map[string]any{
			"required_keys": []string{"title", "summary"},
		},
	})
	if reg.Code != http.StatusOK {
		t.Fatalf("register status=%d body=%s", reg.Code, reg.Body.String())
	}
	_ = s1.Close()

	s2, err := contextstore.Open(context.Background(), contextstore.Config{RootDir: root})
	if err != nil {
		t.Fatalf("open s2: %v", err)
	}
	defer s2.Close()
	srv2 := contextapi.NewServer(s2, contextpolicy.New())

	get := perform(t, srv2, http.MethodGet, "/v1/namespaces/get?namespace=app/editor/session", nil)
	if get.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", get.Code, get.Body.String())
	}
	var payload struct {
		OwnerID string         `json:"owner_id"`
		Policy  map[string]any `json:"policy"`
	}
	if err := json.Unmarshal(get.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.OwnerID != "editor" {
		t.Fatalf("unexpected owner id: %s", payload.OwnerID)
	}
	if _, ok := payload.Policy["required_keys"]; !ok {
		t.Fatalf("expected required_keys policy")
	}
}
