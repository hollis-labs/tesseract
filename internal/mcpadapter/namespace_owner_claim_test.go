package mcpadapter

import (
	"context"
	"reflect"
	"testing"

	"github.com/hollis-labs/tesseract/internal/contextstore"
)

func TestNamespaceRegisterIfUnset(t *testing.T) {
	for _, source := range []string{"absent", "inferred", "inferred-backfill", "explicit", "explicit-empty"} {
		t.Run(source, func(t *testing.T) {
			s := newTestStore(t)
			a := New(s, writeToken(t, s, []string{"namespace.admin"}, []string{"*"}))
			ctx := context.Background()
			ns := "project/claim/memory/notes"
			var policy map[string]any
			if source != "absent" {
				if source != "explicit-empty" {
					policy = map[string]any{"source": source, "retain": "unchanged"}
				}
				if err := s.UpsertNamespacePolicy(ctx, contextstore.NamespacePolicyEntry{Namespace: ns, OwnerType: "project", OwnerID: "original", Policy: policy}); err != nil {
					t.Fatal(err)
				}
				read := parseResult(t, a.namespaceShowResult(ctx, ns))
				if read["owner_explicit"] != (source == "explicit" || source == "explicit-empty") {
					t.Fatalf("read: %v", read)
				}
			}
			req := map[string]any{"namespace": ns, "owner_type": "app", "owner_id": "candidate", "if_unset": true}
			res, err := a.handleNamespaceRegister(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			body := parseResult(t, res)
			explicit := source == "explicit" || source == "explicit-empty"
			wantOwner, wantType := "candidate", "app"
			if explicit {
				wantOwner, wantType = "original", "project"
			}
			if body["changed"] != !explicit || body["owner_id"] != wantOwner || body["owner_type"] != wantType || body["owner_explicit"] != true {
				t.Fatalf("claim: %v", body)
			}
			actual, err := s.GetNamespacePolicy(ctx, ns)
			if err != nil {
				t.Fatal(err)
			}
			if explicit && !reflect.DeepEqual(actual.Policy, policy) {
				t.Fatalf("policy changed: %v", actual.Policy)
			}
			read := parseResult(t, a.namespaceShowResult(ctx, ns))
			if read["owner_id"] != wantOwner || read["owner_explicit"] != true {
				t.Fatalf("read after claim: %v", read)
			}
			// Repeating a claim is a no-op even if the caller supplies a different owner.
			req["owner_id"] = "other"
			res, err = a.handleNamespaceRegister(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			body = parseResult(t, res)
			if body["changed"] != false || body["owner_id"] != wantOwner {
				t.Fatalf("repeat: %v", body)
			}
			// Omission preserves the historical overwrite behavior.
			delete(req, "if_unset")
			res, err = a.handleNamespaceRegister(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			body = parseResult(t, res)
			if body["owner_id"] != "other" {
				t.Fatalf("default overwrite: %v", body)
			}
			actual, err = s.GetNamespacePolicy(ctx, ns)
			if err != nil || actual.OwnerID != "other" {
				t.Fatalf("persisted overwrite: %+v %v", actual, err)
			}
		})
	}
}

func TestNamespaceOwnerClaimRegisteredScope(t *testing.T) {
	s := newTestStore(t)
	args := map[string]any{"namespace": "app/claim", "owner_type": "app", "owner_id": "claim", "if_unset": true}
	readOnly := New(s, writeToken(t, s, []string{"packet"}, []string{"*"}))
	wantErrorCode(t, mustCallRegistered(t, readOnly, "context_namespace_register", args), "insufficient_scope")
	admin := New(s, writeToken(t, s, []string{"namespace.admin"}, []string{"*"}))
	body := mustCallRegistered(t, admin, "context_namespace_register", args)
	if body["changed"] != true || body["owner_id"] != "claim" {
		t.Fatalf("registered claim: %v", body)
	}
	body = mustCallRegistered(t, admin, "context_registry_list", map[string]any{"kind": "namespaces", "name": "app/claim"})
	if body["owner_explicit"] != true {
		t.Fatalf("registered owner read: %v", body)
	}
}
