package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/hollis-labs/tesseract/domains"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/promotion"
	"github.com/hollis-labs/tesseract/internal/sqlitedsn"
	"github.com/hollis-labs/tesseract/internal/workspace"

	_ "modernc.org/sqlite"
)

type promotionPlan struct {
	SourceDomain       string `json:"source_domain"`
	SourceItemID       string `json:"source_item_id"`
	SourceVersionToken string `json:"source_version_token"`
	TargetDomain       string `json:"target_domain"`
	TargetNamespace    string `json:"target_namespace"`
	TargetKey          string `json:"target_key,omitempty"`
	TargetItemID       string `json:"target_item_id,omitempty"`
	Action             string `json:"action"`
}

func runPromoteRecord(ctx context.Context, _ string, args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("promote-record", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	dbPath := fs.String("db", "", "path to the SQLite store to operate on (required)")
	sourceDomain := fs.String("source-domain", "", "source domain: workspace, memory, knowledge, event")
	sourceItemID := fs.String("source-item-id", "", "source item ID")
	sourceVersionToken := fs.String("source-version-token", "", "source version token or revision ID")
	targetDomain := fs.String("target-domain", "", "target domain: workspace, memory, knowledge, event")
	targetNamespace := fs.String("target-namespace", "", "target namespace")
	targetKey := fs.String("target-key", "", "target key")
	targetItemID := fs.String("target-item-id", "", "existing target item ID to update")
	expectedTargetRevID := fs.String("expected-target-revision-id", "", "expected revision ID for existing target item")
	expectedTargetVerToken := fs.String("expected-target-version-token", "", "expected version token for existing target workspace item")
	actor := fs.String("actor", "cli", "actor requesting and applying the promotion")
	reason := fs.String("reason", "", "reason for promotion")
	authorAgentID := fs.String("target-author-agent-id", "cli", "author agent ID for target record")
	authorVersion := fs.String("target-author-version", "1", "author agent version for target record")
	sessionID := fs.String("target-session-id", "cli-session", "author session ID for target record")
	targetStatus := fs.String("target-status", "", "status for target record (e.g. canonical, draft)")
	targetTrigger := fs.String("target-trigger", "", "trigger for target record (e.g. promotion, manual)")
	targetDerivedFrom := fs.String("target-derived-from", "", "derived_from for target record (e.g. project, reference)")
	targetConfidence := fs.Float64("target-confidence", 0.9, "confidence score for target record")
	targetTTL := fs.Int64("target-ttl-seconds", 0, "time-to-live in seconds for target record")
	targetDataSchemaHash := fs.String("target-data-schema-hash", "", "data schema hash for target record")
	targetWorkstreamID := fs.String("target-workstream-id", "", "workstream ID for target record")
	targetKind := fs.String("target-kind", "", "facet kind for knowledge target")
	targetSource := fs.String("target-source", "", "facet source for knowledge target")
	targetPointerScheme := fs.String("target-pointer-scheme", "", "facet pointer scheme for knowledge target")
	targetPointerLocator := fs.String("target-pointer-locator", "", "facet pointer locator for knowledge target")
	apply := fs.Bool("apply", false, "apply the promotion instead of planning it")
	jsonOut := fs.Bool("json", false, "emit the receipt or plan as JSON")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	if *dbPath == "" {
		fmt.Fprintln(stderr, "error: --db is required")
		return 1
	}

	if strings.TrimSpace(*sourceItemID) == "" {
		fmt.Fprintln(stderr, "error: -source-item-id is required")
		return 1
	}
	if strings.TrimSpace(*sourceVersionToken) == "" {
		fmt.Fprintln(stderr, "error: -source-version-token is required")
		return 1
	}
	if strings.TrimSpace(*targetDomain) == "" {
		fmt.Fprintln(stderr, "error: -target-domain is required")
		return 1
	}
	if strings.TrimSpace(*targetNamespace) == "" && strings.TrimSpace(*targetItemID) == "" {
		fmt.Fprintln(stderr, "error: -target-namespace or -target-item-id is required")
		return 1
	}

	db, err := sql.Open("sqlite", sqlitedsn.DSN(*dbPath, "journal_mode(WAL)"))
	if err != nil {
		fmt.Fprintf(stderr, "error: open db: %v\n", err)
		return 1
	}
	defer func() { _ = db.Close() }()
	if pingErr := db.PingContext(ctx); pingErr != nil {
		fmt.Fprintf(stderr, "error: ping db: %v\n", pingErr)
		return 1
	}

	memStore := memory.NewStore(db, nil, "", 0, memory.NoopQueue{})
	wsStore := workspace.NewStore(db)
	promStore := promotion.NewStoreWithDB(db, memStore, wsStore)

	var wsID *string
	if *targetWorkstreamID != "" {
		wsID = targetWorkstreamID
	}

	var pointer *memory.Pointer
	if *targetPointerScheme != "" || *targetPointerLocator != "" {
		pointer = &memory.Pointer{
			Scheme:  *targetPointerScheme,
			Locator: *targetPointerLocator,
		}
	}

	target := promotion.Target{
		Domain:               domains.Domain(*targetDomain),
		Namespace:            *targetNamespace,
		Key:                  *targetKey,
		ItemID:               *targetItemID,
		ExpectedRevisionID:   *expectedTargetRevID,
		ExpectedVersionToken: *expectedTargetVerToken,
		Author: memory.Author{
			AgentID:      *authorAgentID,
			AgentVersion: *authorVersion,
		},
		SessionID:      *sessionID,
		Confidence:     *targetConfidence,
		TTLSeconds:     *targetTTL,
		DataSchemaHash: *targetDataSchemaHash,
		WorkstreamID:   wsID,
		Status:         memory.Status(*targetStatus),
		Trigger:        memory.Trigger(*targetTrigger),
		DerivedFrom:    memory.DerivedFrom(*targetDerivedFrom),
		Kind:           *targetKind,
		Source:         *targetSource,
		Pointer:        pointer,
	}

	reqIn := promotion.RequestInput{
		SourceDomain:       domains.Domain(*sourceDomain),
		SourceItemID:       *sourceItemID,
		SourceVersionToken: *sourceVersionToken,
		Actor:              *actor,
		Reason:             *reason,
		Target:             target,
	}

	if !*apply {
		plan := promotionPlan{
			SourceDomain:       *sourceDomain,
			SourceItemID:       *sourceItemID,
			SourceVersionToken: *sourceVersionToken,
			TargetDomain:       *targetDomain,
			TargetNamespace:    *targetNamespace,
			TargetKey:          *targetKey,
			TargetItemID:       *targetItemID,
			Action:             fmt.Sprintf("promote from %s to %s", *sourceDomain, *targetDomain),
		}
		if *jsonOut {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(plan)
		} else {
			fmt.Fprintf(stdout, "Promotion plan:\n")
			fmt.Fprintf(stdout, "  Source:    %s (item_id: %s, token: %s)\n", *sourceDomain, *sourceItemID, *sourceVersionToken)
			fmt.Fprintf(stdout, "  Target:    %s (namespace: %s, key: %s)\n", *targetDomain, *targetNamespace, *targetKey)
			fmt.Fprintln(stdout, "\n(dry-run; pass --apply to commit the promotion)")
		}
		return 0
	}

	reqReceipt, err := promStore.Request(ctx, reqIn)
	if err != nil {
		if errors.Is(err, promotion.ErrTargetKeyOccupied) {
			fmt.Fprintf(stderr, "error: target key occupied: %v\n", err)
			return 2
		}
		fmt.Fprintf(stderr, "error: request promotion: %v\n", err)
		return 1
	}

	_, err = promStore.Approve(ctx, promotion.ApproveInput{
		RequestID: reqReceipt.RequestID,
		Actor:     *actor,
		Notes:     "cli promote-record",
	})
	if err != nil {
		fmt.Fprintf(stderr, "error: approve promotion: %v\n", err)
		return 1
	}

	applyReceipt, err := promStore.Apply(ctx, promotion.ApplyInput{
		RequestID: reqReceipt.RequestID,
		Actor:     *actor,
	})
	if err != nil {
		if errors.Is(err, promotion.ErrTargetKeyOccupied) {
			fmt.Fprintf(stderr, "error: target key occupied: %v\n", err)
			return 2
		}
		fmt.Fprintf(stderr, "error: apply promotion: %v\n", err)
		return 1
	}

	if *jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(applyReceipt)
	} else {
		fmt.Fprintf(stdout, "Applied promotion:\n")
		fmt.Fprintf(stdout, "  Request ID:   %s\n", applyReceipt.RequestID)
		fmt.Fprintf(stdout, "  Source:       %s (item: %s)\n", applyReceipt.SourceDomain, applyReceipt.SourceItemID)
		targetToken := applyReceipt.TargetRevisionID
		if targetToken == "" {
			targetToken = applyReceipt.TargetVersionToken
		}
		fmt.Fprintf(stdout, "  Target:       %s (item: %s, token/rev: %s)\n", applyReceipt.TargetDomain, applyReceipt.TargetItemID, targetToken)
		fmt.Fprintf(stdout, "  Destination:  %s / %s\n", applyReceipt.TargetNamespace, applyReceipt.TargetKey)
	}
	return 0
}
