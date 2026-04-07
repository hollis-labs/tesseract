package contextcli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/hollis-labs/cortex/internal/contextstore"
	"github.com/hollis-labs/cortex/internal/contexttypes"
)

func (c *CLI) runTypedPut(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("typed-put", flag.ContinueOnError)
	ns := fs.String("namespace", "", "namespace")
	key := fs.String("key", "", "key")
	actor := fs.String("actor", "user", "actor")
	recordType := fs.String("type", "", "context type (e.g. task/spec)")
	status := fs.String("status", "draft", "status (draft|reviewed|canonical|deprecated)")
	ttl := fs.String("ttl", "", "TTL as RFC3339 timestamp or duration (e.g. 24h)")
	pointers := fs.String("pointers", "", "comma-separated pointer references")
	payload := fs.String("payload", "", "JSON payload")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}
	if *ns == "" || *key == "" || *payload == "" {
		return c.fail("namespace, key, and payload are required")
	}

	reg := contexttypes.NewRegistry()
	if err := reg.ValidateType(*recordType); err != nil {
		return c.fail(err.Error())
	}
	if err := reg.ValidateStatus(*recordType, *status); err != nil {
		return c.fail(err.Error())
	}

	// Parse TTL: if it's a duration, compute the expiry timestamp.
	ttlStr := *ttl
	if ttlStr != "" {
		if dur, err := time.ParseDuration(ttlStr); err == nil {
			ttlStr = time.Now().UTC().Add(dur).Format(time.RFC3339)
		}
	}
	// Apply default TTL from type registry if not specified.
	if ttlStr == "" && *recordType != "" {
		ct, ok := reg.GetType(*recordType)
		if ok {
			defaultTTL := ct.ParseDefaultTTL()
			if defaultTTL > 0 {
				ttlStr = time.Now().UTC().Add(defaultTTL).Format(time.RFC3339)
			}
		}
	}

	var pts []string
	if *pointers != "" {
		for _, p := range strings.Split(*pointers, ",") {
			if t := strings.TrimSpace(p); t != "" {
				pts = append(pts, t)
			}
		}
	}

	var raw json.RawMessage
	if err := json.Unmarshal([]byte(*payload), &raw); err != nil {
		return c.fail("payload must be valid JSON: " + err.Error())
	}

	rec, err := c.Store.AppendRecord(ctx, contextstore.AppendInput{
		Namespace:  *ns,
		Key:        *key,
		Actor:      *actor,
		Payload:    raw,
		RecordType: *recordType,
		Status:     *status,
		TTL:        ttlStr,
		Pointers:   pts,
	})
	if err != nil {
		return c.fail(err.Error())
	}

	result := map[string]any{
		"record_id":   rec.RecordID,
		"revision":    rec.Revision,
		"namespace":   rec.Namespace,
		"key":         rec.Key,
		"record_type": rec.RecordType,
		"status":      rec.Status,
		"ttl":         rec.TTL,
	}
	b, _ := json.MarshalIndent(result, "", "  ")
	fmt.Fprintln(c.Stdout, string(b))
	return 0
}

func (c *CLI) runStatusPromote(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("status-promote", flag.ContinueOnError)
	ns := fs.String("namespace", "", "namespace")
	key := fs.String("key", "", "key")
	actor := fs.String("actor", "user", "actor")
	toStatus := fs.String("to", "", "target status (default: next in chain)")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}
	if *ns == "" || *key == "" {
		return c.fail("namespace and key are required")
	}

	head, err := c.Store.Head(ctx, *ns, *key)
	if err != nil {
		return c.fail(err.Error())
	}

	reg := contexttypes.NewRegistry()
	oldStatus := head.Status
	if oldStatus == "" {
		oldStatus = "draft"
	}

	newStatus := *toStatus
	if newStatus == "" {
		newStatus = contexttypes.NextPromotionStatus(oldStatus)
		if newStatus == "" {
			return c.fail(fmt.Sprintf("cannot promote from status %q", oldStatus))
		}
	}

	if err := reg.ValidateTransition(head.RecordType, oldStatus, newStatus, *actor); err != nil {
		return c.fail(err.Error())
	}

	rec, err := c.Store.UpdateRecordStatus(ctx, *ns, *key, *actor, newStatus)
	if err != nil {
		return c.fail(err.Error())
	}

	result := map[string]any{
		"record_id":   rec.RecordID,
		"revision":    rec.Revision,
		"from_status": oldStatus,
		"to_status":   newStatus,
		"record_type": head.RecordType,
	}
	b, _ := json.MarshalIndent(result, "", "  ")
	fmt.Fprintln(c.Stdout, string(b))
	return 0
}

func (c *CLI) runStatusDeprecate(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("status-deprecate", flag.ContinueOnError)
	ns := fs.String("namespace", "", "namespace")
	key := fs.String("key", "", "key")
	actor := fs.String("actor", "user", "actor")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}
	if *ns == "" || *key == "" {
		return c.fail("namespace and key are required")
	}

	head, err := c.Store.Head(ctx, *ns, *key)
	if err != nil {
		return c.fail(err.Error())
	}

	oldStatus := head.Status
	if oldStatus == "" {
		oldStatus = "draft"
	}
	if oldStatus == "deprecated" {
		return c.fail("item is already deprecated")
	}

	rec, err := c.Store.UpdateRecordStatus(ctx, *ns, *key, *actor, "deprecated")
	if err != nil {
		return c.fail(err.Error())
	}

	result := map[string]any{
		"record_id":   rec.RecordID,
		"revision":    rec.Revision,
		"from_status": oldStatus,
		"to_status":   "deprecated",
		"record_type": head.RecordType,
	}
	b, _ := json.MarshalIndent(result, "", "  ")
	fmt.Fprintln(c.Stdout, string(b))
	return 0
}

func (c *CLI) runTypedView(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("typed-view", flag.ContinueOnError)
	viewID := fs.String("view", "", "view ID (e.g. task_exec, strategy)")
	nsPattern := fs.String("namespace", "", "namespace glob pattern (default: all)")
	maxItems := fs.Int("limit", 0, "max items")
	output := fs.String("output", "json", "output format: json|table")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}
	if *viewID == "" {
		return c.fail("view ID is required (--view task_exec|strategy)")
	}

	reg := contexttypes.NewRegistry()
	viewDef, ok := reg.GetView(*viewID)
	if !ok {
		return c.fail(fmt.Sprintf("view %q not found", *viewID))
	}

	limit := viewDef.MaxItems
	if *maxItems > 0 {
		limit = *maxItems
	}
	if limit <= 0 {
		limit = 50
	}

	namespaces := []string{"*"}
	if *nsPattern != "" {
		namespaces = []string{*nsPattern}
	}

	statuses := []string{"draft", "reviewed", "canonical"}
	items, err := c.Store.Select(ctx, contextstore.Selector{
		Namespaces:    namespaces,
		RevisionScope: "head",
		Types:         viewDef.Types,
		Statuses:      statuses,
		Limit:         limit,
	})
	if err != nil {
		return c.fail(err.Error())
	}

	if *output == "table" {
		fmt.Fprintf(c.Stdout, "VIEW: %s (%d items)\n", *viewID, len(items))
		fmt.Fprintf(c.Stdout, "%-20s %-16s %-12s %-30s %s\n", "TYPE", "STATUS", "VERSION", "NAMESPACE", "KEY")
		fmt.Fprintf(c.Stdout, "%s\n", strings.Repeat("-", 100))
		for _, rec := range items {
			fmt.Fprintf(c.Stdout, "%-20s %-16s %-12d %-30s %s\n",
				rec.RecordType, rec.Status, rec.ContentVersion, rec.Namespace, rec.Key)
		}
		return 0
	}

	// JSON output.
	out := make([]map[string]any, len(items))
	for i, rec := range items {
		out[i] = map[string]any{
			"record_id":       rec.RecordID,
			"namespace":       rec.Namespace,
			"key":             rec.Key,
			"record_type":     rec.RecordType,
			"status":          rec.Status,
			"content_version": rec.ContentVersion,
		}
	}
	result := map[string]any{
		"view":  *viewID,
		"items": out,
		"count": len(out),
		"types": viewDef.Types,
	}
	b, _ := json.MarshalIndent(result, "", "  ")
	fmt.Fprintln(c.Stdout, string(b))
	return 0
}

func (c *CLI) runTypesList(_ context.Context, _ []string) int {
	reg := contexttypes.NewRegistry()
	types := reg.ListTypes()
	b, _ := json.MarshalIndent(map[string]any{"types": types}, "", "  ")
	fmt.Fprintln(c.Stdout, string(b))
	return 0
}

func (c *CLI) runViewsList(_ context.Context, _ []string) int {
	reg := contexttypes.NewRegistry()
	views := reg.ListViews()
	b, _ := json.MarshalIndent(map[string]any{"views": views}, "", "  ")
	fmt.Fprintln(c.Stdout, string(b))
	return 0
}

func (c *CLI) runTTLCleanup(ctx context.Context, _ []string) int {
	cleaned, err := c.Store.CleanupExpiredTTL(ctx)
	if err != nil {
		return c.fail(err.Error())
	}
	fmt.Fprintf(c.Stdout, "cleaned %d expired records\n", cleaned)
	return 0
}

func (c *CLI) runContextPack(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("context-pack", flag.ContinueOnError)
	viewID := fs.String("view", "", "view ID")
	nsPattern := fs.String("namespace", "", "namespace glob")
	maxItems := fs.Int("limit", 50, "max items")
	maxTokens := fs.Int("max-tokens", 8000, "max tokens estimate")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}
	if *viewID == "" {
		return c.fail("view ID is required")
	}

	reg := contexttypes.NewRegistry()
	viewDef, ok := reg.GetView(*viewID)
	if !ok {
		return c.fail(fmt.Sprintf("view %q not found", *viewID))
	}

	namespaces := []string{"*"}
	if *nsPattern != "" {
		namespaces = []string{*nsPattern}
	}

	statuses := []string{"draft", "reviewed", "canonical"}
	items, err := c.Store.Select(ctx, contextstore.Selector{
		Namespaces:    namespaces,
		RevisionScope: "head",
		Types:         viewDef.Types,
		Statuses:      statuses,
		Limit:         *maxItems * 2,
	})
	if err != nil {
		return c.fail(err.Error())
	}

	// Rank.
	type ri struct {
		rec   contextstore.Record
		score float64
	}
	ranked := make([]ri, len(items))
	for i, rec := range items {
		ts := 1.0
		if ct, ok := reg.GetType(rec.RecordType); ok && ct.RetrievalRankBias > 0 {
			ts = ct.RetrievalRankBias
		}
		ss := 0.5
		if w, ok := viewDef.RankWeights[rec.Status]; ok {
			ss = w
		}
		ranked[i] = ri{rec: rec, score: ts * ss}
	}
	for i := 0; i < len(ranked)-1; i++ {
		for j := i + 1; j < len(ranked); j++ {
			if ranked[j].score > ranked[i].score {
				ranked[i], ranked[j] = ranked[j], ranked[i]
			}
		}
	}

	var packItems []map[string]any
	tokensSoFar := 0
	for _, rr := range ranked {
		if len(packItems) >= *maxItems {
			break
		}
		tokens := contextstore.EstimateTokens(rr.rec.Payload)
		if *maxTokens > 0 && tokensSoFar+tokens > *maxTokens {
			break
		}
		packItems = append(packItems, map[string]any{
			"record_id":   rr.rec.RecordID,
			"namespace":   rr.rec.Namespace,
			"key":         rr.rec.Key,
			"record_type": rr.rec.RecordType,
			"status":      rr.rec.Status,
			"payload":     json.RawMessage(rr.rec.Payload),
		})
		tokensSoFar += tokens
	}
	if packItems == nil {
		packItems = []map[string]any{}
	}

	result := map[string]any{
		"view":           *viewID,
		"generated_at":   time.Now().UTC().Format(time.RFC3339),
		"token_estimate": tokensSoFar,
		"items":          packItems,
		"count":          len(packItems),
	}
	b, _ := json.MarshalIndent(result, "", "  ")
	fmt.Fprintln(c.Stdout, string(b))
	return 0
}
