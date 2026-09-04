package contextcli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/hollis-labs/tesseract/internal/contextpolicy"
	"github.com/hollis-labs/tesseract/internal/contextstore"
)

// CLI implements context command handlers.
type CLI struct {
	Store  *contextstore.Store
	Policy *contextpolicy.Engine
	Stdout io.Writer
	Stderr io.Writer
	// ExecCommand is an optional command runner hook used by contract helpers.
	ExecCommand func(ctx context.Context, name string, args ...string) ([]byte, error)
}

// Run executes `context ...` commands.
func (c *CLI) Run(ctx context.Context, args []string) int {
	if c.Stdout == nil {
		c.Stdout = os.Stdout
	}
	if c.Stderr == nil {
		c.Stderr = os.Stderr
	}
	if c.Policy == nil {
		c.Policy = contextpolicy.New()
	}
	if c.Store == nil {
		return c.fail("store is required")
	}
	if err := c.reloadPolicies(ctx); err != nil {
		return c.fail(err.Error())
	}
	if len(args) == 0 || args[0] != "context" {
		return c.fail("usage: context <namespace|put|get|history|view|promote|maintenance|packet|broker|typed-put|status-promote|status-deprecate|typed-view|types|views|ttl-cleanup|context-pack> ...")
	}
	if len(args) < 2 {
		return c.fail("missing context subcommand")
	}

	switch args[1] {
	case "namespace":
		return c.runNamespace(args[2:])
	case "put":
		return c.runPut(ctx, args[2:])
	case "get":
		return c.runGet(ctx, args[2:])
	case "history":
		return c.runHistory(ctx, args[2:])
	case "view":
		return c.runView(ctx, args[2:])
	case "promote":
		return c.runPromote(ctx, args[2:])
	case "doctor":
		return c.runDoctor(ctx, args[2:])
	case "repair-heads":
		return c.runRepairHeads(ctx, args[2:])
	case "audit":
		return c.runAudit(ctx, args[2:])
	case "token":
		return c.runToken(ctx, args[2:])
	case "backup":
		return c.runBackup(ctx, args[2:])
	case "health":
		return c.runHealth(ctx, args[2:])
	case "bootstrap":
		return c.runBootstrap(ctx, args[2:])
	case "compact":
		return c.runCompact(ctx, args[2:])
	case "contract":
		return c.runContract(ctx, args[2:])
	case "maintenance":
		return c.runMaintenance(ctx, args[2:])
	case "packet":
		return c.runPacket(ctx, args[2:])
	case "broker":
		return c.runBroker(ctx, args[2:])
	case "typed-put":
		return c.runTypedPut(ctx, args[2:])
	case "status-promote":
		return c.runStatusPromote(ctx, args[2:])
	case "status-deprecate":
		return c.runStatusDeprecate(ctx, args[2:])
	case "typed-view":
		return c.runTypedView(ctx, args[2:])
	case "types":
		return c.runTypesList(ctx, args[2:])
	case "views":
		return c.runViewsList(ctx, args[2:])
	case "ttl-cleanup":
		return c.runTTLCleanup(ctx, args[2:])
	case "context-pack":
		return c.runContextPack(ctx, args[2:])
	default:
		return c.fail("unknown subcommand: " + args[1])
	}
}

type contractSuite struct {
	Name    string `json:"name"`
	Command string `json:"command"`
}

func contractSuites() []contractSuite {
	return []contractSuite{
		{Name: "api", Command: "go test ./tests/integration -run APIContract -count=1"},
		{Name: "api-errors", Command: "go test ./tests/integration -run APIErrorContract -count=1"},
		{Name: "audit", Command: "go test ./tests/integration -run AuditContract -count=1"},
		{Name: "metrics", Command: "go test ./tests/integration -run MetricsContract -count=1"},
		{Name: "log-metrics", Command: "go test ./tests/integration -run LogMetricsContract -count=1"},
		{Name: "readiness", Command: "go test ./tests/integration -run ReadinessContract -count=1"},
		{Name: "cli-health-summary", Command: "go test ./tests/integration -run CLIHealthSummaryContract -count=1"},
		{Name: "cli-audit", Command: "go test ./tests/integration -run CLIAuditContract -count=1"},
		{Name: "cli-contract-list-default-output", Command: "go test ./tests/integration -run ContractListDefaultOutputContract -count=1"},
		{Name: "cli-contract-list-deterministic-order", Command: "go test ./tests/integration -run ContractListDefaultOutputDeterministicContract -count=1"},
		{Name: "cli-contract-list-count-parity", Command: "go test ./tests/integration -run ContractListCountParityContract -count=1"},
		{Name: "cli-contract-list-invalid-output", Command: "go test ./tests/integration -run ContractListInvalidOutputErrorContract -count=1"},
		{Name: "cli-contract-list-table", Command: "go test ./tests/integration -run ContractListTableContract -count=1"},
		{Name: "cli-contract-list-table-header", Command: "go test ./tests/integration -run ContractListTableHeaderContract -count=1"},
		{Name: "cli-contract-list-table-count-parity", Command: "go test ./tests/integration -run ContractListTableCountParityContract -count=1"},
		{Name: "cli-contract-run-all-default-output", Command: "go test ./tests/integration -run ContractRunAllDefaultOutputContract -count=1"},
		{Name: "cli-contract-run-all-table", Command: "go test ./tests/integration -run ContractRunAllTableContract -count=1"},
		{Name: "cli-contract-run-all-execute-table", Command: "go test ./tests/integration -run ContractRunAllExecuteTableContract -count=1"},
		{Name: "cli-contract-run-all-execute-json", Command: "go test ./tests/integration -run ContractRunAllExecuteJSONContract -count=1"},
		{Name: "cli-contract-run-default-output", Command: "go test ./tests/integration -run ContractRunDefaultOutputContract -count=1"},
		{Name: "cli-contract-run-dry-table", Command: "go test ./tests/integration -run ContractRunTableDryContract -count=1"},
		{Name: "cli-contract-run-table-header", Command: "go test ./tests/integration -run ContractRunTableHeaderContract -count=1"},
		{Name: "cli-contract-run-table", Command: "go test ./tests/integration -run ContractRunExecuteTableContract -count=1"},
		{Name: "cli-contract-run-invalid-output", Command: "go test ./tests/integration -run ContractRunInvalidOutputErrorContract -count=1"},
		{Name: "cli-contract-run-execute-invalid-output", Command: "go test ./tests/integration -run ContractRunExecuteInvalidOutputErrorContract -count=1"},
		{Name: "cli-contract-run-unknown-suite", Command: "go test ./tests/integration -run ContractRunUnknownSuiteErrorContract -count=1"},
		{Name: "smoke-invalid-token", Command: "go test ./tests/integration -run SmokeInvalidTokenContract -count=1"},
		{Name: "make-contract-cli-list", Command: "go test ./tests/integration -run MakeContractCLIListContract -count=1"},
		{Name: "make-contract-cli-run", Command: "go test ./tests/integration -run MakeContractCLIRunContract -count=1"},
		{Name: "make-smoke-invalid-token", Command: "go test ./tests/integration -run MakeSmokeInvalidTokenContract -count=1"},
		{Name: "contract-suite-commands-format", Command: "go test ./tests/integration -run ContractSuiteCommandsFormatContract -count=1"},
		{Name: "contract-suite-commands-unique", Command: "go test ./tests/integration -run ContractSuiteCommandsUniqueContract -count=1"},
		{Name: "contract-suite-commands-prefix", Command: "go test ./tests/integration -run ContractSuiteCommandsPrefixContract -count=1"},
		{Name: "contract-suite-commands-deterministic", Command: "go test ./tests/integration -run ContractSuiteCommandsDeterministicContract -count=1"},
		{Name: "contract-suite-commands-suffix", Command: "go test ./tests/integration -run ContractSuiteCommandsSuffixContract -count=1"},
		{Name: "contract-suite-commands-token-count", Command: "go test ./tests/integration -run ContractSuiteCommandsTokenCountContract -count=1"},
		{Name: "contract-suite-commands-non-empty", Command: "go test ./tests/integration -run ContractSuiteCommandsNonEmptyContract -count=1"},
		{Name: "fixture-lint-script", Command: "go test ./tests/integration -run ContractFixtureLintScript -count=1"},
	}
}

func (c *CLI) runContract(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return c.fail("usage: context contract <list|run> ...")
	}
	switch args[0] {
	case "list":
		return c.runContractList(args[1:])
	case "run":
		return c.runContractRun(ctx, args[1:])
	default:
		return c.fail("usage: context contract <list|run> ...")
	}
}

func (c *CLI) runContractList(args []string) int {
	fs := flag.NewFlagSet("contract list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	output := fs.String("output", "json", "json|table")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}
	suites := contractSuites()
	switch strings.TrimSpace(*output) {
	case "json", "":
		return c.writeJSON(map[string]any{"count": len(suites), "items": suites})
	case "table":
		w := tabwriter.NewWriter(c.Stdout, 2, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "SUITE\tCOMMAND")
		for _, suite := range suites {
			_, _ = fmt.Fprintf(w, "%s\t%s\n", suite.Name, suite.Command)
		}
		_ = w.Flush()
		return 0
	default:
		return c.fail("output must be json|table")
	}
}

func (c *CLI) runContractRun(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("contract run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	suiteName := fs.String("suite", "all", "suite name or all")
	execute := fs.Bool("execute", false, "execute the suite command(s)")
	output := fs.String("output", "json", "json|table")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}
	allSuites := contractSuites()
	selected := make([]contractSuite, 0, len(allSuites))
	if strings.TrimSpace(*suiteName) == "all" {
		selected = append(selected, allSuites...)
	} else {
		for _, suite := range allSuites {
			if suite.Name == strings.TrimSpace(*suiteName) {
				selected = append(selected, suite)
				break
			}
		}
		if len(selected) == 0 {
			return c.fail("unknown contract suite: " + strings.TrimSpace(*suiteName))
		}
	}

	type result struct {
		Suite   string `json:"suite"`
		Command string `json:"command"`
		OK      bool   `json:"ok"`
		Output  string `json:"output,omitempty"`
	}
	results := make([]result, 0, len(selected))
	for _, suite := range selected {
		entry := result{Suite: suite.Name, Command: suite.Command, OK: true}
		if *execute {
			out, err := c.execCommand(ctx, suite.Command)
			entry.Output = strings.TrimSpace(string(out))
			if err != nil {
				entry.OK = false
			}
		}
		results = append(results, entry)
	}

	switch strings.TrimSpace(*output) {
	case "json", "":
		return c.writeJSON(map[string]any{
			"executed": *execute,
			"count":    len(results),
			"items":    results,
		})
	case "table":
		w := tabwriter.NewWriter(c.Stdout, 2, 4, 2, ' ', 0)
		if *execute {
			_, _ = fmt.Fprintln(w, "SUITE\tOK\tCOMMAND")
			for _, item := range results {
				_, _ = fmt.Fprintf(w, "%s\t%t\t%s\n", item.Suite, item.OK, item.Command)
			}
		} else {
			_, _ = fmt.Fprintln(w, "SUITE\tCOMMAND")
			for _, item := range results {
				_, _ = fmt.Fprintf(w, "%s\t%s\n", item.Suite, item.Command)
			}
		}
		_ = w.Flush()
		return 0
	default:
		return c.fail("output must be json|table")
	}
}

func (c *CLI) execCommand(ctx context.Context, cmdline string) ([]byte, error) {
	fields := strings.Fields(strings.TrimSpace(cmdline))
	if len(fields) == 0 {
		return nil, errors.New("empty command")
	}
	if c.ExecCommand != nil {
		return c.ExecCommand(ctx, fields[0], fields[1:]...)
	}
	return exec.CommandContext(ctx, fields[0], fields[1:]...).CombinedOutput()
}

func (c *CLI) runNamespace(args []string) int {
	if len(args) == 0 {
		return c.fail("usage: context namespace <register|show> ...")
	}
	switch args[0] {
	case "register":
		return c.runNamespaceRegister(args[1:])
	case "show":
		return c.runNamespaceShow(args[1:])
	default:
		return c.fail("usage: context namespace <register|show> ...")
	}
}

func (c *CLI) runNamespaceRegister(args []string) int {
	fs := flag.NewFlagSet("namespace register", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	namespace := fs.String("namespace", "", "namespace")
	ownerType := fs.String("owner-type", "", "owner-type")
	ownerID := fs.String("owner-id", "", "owner-id")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}
	entry := contextstore.NamespacePolicyEntry{
		Namespace: *namespace,
		OwnerType: *ownerType,
		OwnerID:   *ownerID,
	}
	if err := c.Store.UpsertNamespacePolicy(context.Background(), entry); err != nil {
		return c.fail(err.Error())
	}
	if err := c.Policy.RegisterNamespace(*namespace, *ownerType, *ownerID, nil); err != nil {
		return c.fail(err.Error())
	}
	return c.writeJSON(map[string]any{"namespace": *namespace, "owner_type": *ownerType, "owner_id": *ownerID})
}

func (c *CLI) runNamespaceShow(args []string) int {
	fs := flag.NewFlagSet("namespace show", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	namespace := fs.String("namespace", "", "namespace")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}
	entry, err := c.Store.GetNamespacePolicy(context.Background(), *namespace)
	if err != nil {
		return c.fail(err.Error())
	}
	_ = c.Policy.RegisterNamespace(entry.Namespace, entry.OwnerType, entry.OwnerID, entry.Policy)
	return c.writeJSON(map[string]any{
		"namespace":  *namespace,
		"owner_type": entry.OwnerType,
		"owner_id":   entry.OwnerID,
		"policy":     entry.Policy,
	})
}

func (c *CLI) runPut(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("put", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	clientID := fs.String("client-id", "", "client-id")
	actor := fs.String("actor", "", "actor")
	namespace := fs.String("namespace", "", "namespace")
	key := fs.String("key", "", "key")
	jsonArg := fs.String("json", "", "json payload")
	fileArg := fs.String("file", "", "payload file")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}
	payload, err := readPayload(*jsonArg, *fileArg)
	if err != nil {
		return c.fail(err.Error())
	}
	if err := c.Policy.CanWrite(*clientID, *actor, *namespace); err != nil {
		return c.fail(err.Error())
	}
	if err := c.Policy.ValidatePayload(*namespace, payload); err != nil {
		return c.fail(err.Error())
	}
	rec, err := c.Store.AppendRecord(ctx, contextstore.AppendInput{Namespace: *namespace, Key: *key, Actor: *actor, Payload: payload})
	if err != nil {
		return c.fail(err.Error())
	}
	_ = c.Store.EmitWrite(ctx, *actor, *namespace, *key, rec.Revision, rec.RecordID, json.RawMessage(`{"source":"cli"}`))
	return c.writeJSON(rec)
}

func (c *CLI) runGet(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("get", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	namespace := fs.String("namespace", "", "namespace")
	key := fs.String("key", "", "key")
	output := fs.String("output", "json", "json|table")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}
	rec, err := c.Store.Head(ctx, *namespace, *key)
	if err != nil {
		return c.fail(err.Error())
	}
	return c.writeRecords([]contextstore.Record{rec}, *output)
}

func (c *CLI) runHistory(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("history", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	namespace := fs.String("namespace", "", "namespace")
	key := fs.String("key", "", "key")
	limit := fs.Int("limit", 0, "limit")
	output := fs.String("output", "json", "json|table")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}
	recs, err := c.Store.History(ctx, *namespace, *key, *limit)
	if err != nil {
		return c.fail(err.Error())
	}
	return c.writeRecords(recs, *output)
}

func (c *CLI) runView(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("view", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	selector := fs.String("selector", "", "selector json")
	selectorFile := fs.String("selector-file", "", "selector file")
	includePayload := fs.Bool("include-payload", false, "include payload")
	limit := fs.Int("limit", 0, "limit")
	output := fs.String("output", "json", "json|table")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}

	sel, err := readSelector(*selector, *selectorFile)
	if err != nil {
		return c.fail(err.Error())
	}
	if *limit > 0 {
		sel.Limit = *limit
	}
	recs, err := c.Store.Select(ctx, sel)
	if err != nil {
		return c.fail(err.Error())
	}
	if !*includePayload {
		for i := range recs {
			recs[i].Payload = nil
		}
	}
	return c.writeRecords(recs, *output)
}

func (c *CLI) runPromote(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return c.fail("usage: context promote <request|list|approve|apply|accept> ...")
	}
	switch args[0] {
	case "request":
		return c.runPromoteRequest(ctx, args[1:])
	case "list":
		return c.runPromoteList(ctx, args[1:])
	case "approve":
		return c.runPromoteApprove(ctx, args[1:])
	case "apply":
		return c.runPromoteApply(ctx, args[1:])
	case "accept":
		return c.runPromoteAccept(ctx, args[1:])
	default:
		return c.fail("usage: context promote <request|list|approve|apply|accept> ...")
	}
}

func (c *CLI) runPromoteRequest(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("promote request", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	actor := fs.String("actor", "cli", "actor creating the request")
	clientID := fs.String("client-id", "cli", "client identifier for namespace path")
	srcNS := fs.String("source-namespace", "", "source namespace (required)")
	srcKey := fs.String("source-key", "", "source key (required)")
	tgtNS := fs.String("target-namespace", "", "target namespace (required)")
	tgtKey := fs.String("target-key", "", "target key (required)")
	reason := fs.String("reason", "", "reason for the promotion")
	summary := fs.String("summary", "", "proposed summary of the record")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}
	if *srcNS == "" || *srcKey == "" || *tgtNS == "" || *tgtKey == "" {
		return c.fail("--source-namespace, --source-key, --target-namespace, --target-key required")
	}

	src, err := c.Store.Head(ctx, *srcNS, *srcKey)
	if err != nil {
		return c.fail(fmt.Sprintf("source record not found: %v", err))
	}

	requestID := "req-" + fmt.Sprintf("%d", time.Now().UnixNano())
	now := time.Now().UTC().Format(time.RFC3339)
	pr := contextstore.PromoteRequest{
		Type:             "promote.request",
		RequestID:        requestID,
		SourceNamespace:  src.Namespace,
		SourceKey:        src.Key,
		SourceRevisionID: src.RecordID,
		SourceChecksum:   src.Checksum,
		TargetNamespace:  *tgtNS,
		TargetKey:        *tgtKey,
		Reason:           *reason,
		ProposedSummary:  *summary,
		Status:           "pending",
		RequestedAt:      now,
		RequestedBy:      *actor,
	}
	payload, _ := json.Marshal(pr)
	namespace := "app/" + *clientID + "/promotions"
	if _, err := c.Store.AppendRecord(ctx, contextstore.AppendInput{
		Namespace: namespace,
		Key:       requestID,
		Actor:     *actor,
		Payload:   payload,
	}); err != nil {
		return c.fail(err.Error())
	}

	_, _ = fmt.Fprintf(c.Stdout, "Promotion request created.\n")
	_, _ = fmt.Fprintf(c.Stdout, "  Request ID:  %s\n", requestID)
	_, _ = fmt.Fprintf(c.Stdout, "  Status:      pending\n")
	_, _ = fmt.Fprintf(c.Stdout, "  Source:      %s/%s (rev %s)\n", src.Namespace, src.Key, src.RecordID)
	_, _ = fmt.Fprintf(c.Stdout, "  Target:      %s/%s\n", *tgtNS, *tgtKey)
	if *reason != "" {
		_, _ = fmt.Fprintf(c.Stdout, "  Reason:      %s\n", *reason)
	}
	_, _ = fmt.Fprintf(c.Stdout, "  Next step:   context promote approve %s\n", requestID)
	return 0
}

func (c *CLI) runPromoteList(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("promote list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	status := fs.String("status", "pending", "filter by status: pending|approved|applied|all")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}

	recs, err := c.Store.Select(ctx, contextstore.Selector{
		Namespaces:    []string{"app/*/promotions"},
		RevisionScope: "head",
	})
	if err != nil {
		return c.fail(err.Error())
	}

	tw := tabwriter.NewWriter(c.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tSTATUS\tSOURCE\tTARGET\tREQUESTED")
	count := 0
	for _, rec := range recs {
		var pr contextstore.PromoteRequest
		if err := json.Unmarshal(rec.Payload, &pr); err != nil || pr.Type != "promote.request" {
			continue
		}
		if *status != "all" && pr.Status != *status {
			continue
		}
		reqAt := pr.RequestedAt
		if len(reqAt) > 16 {
			reqAt = reqAt[:16]
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s/%s\t%s/%s\t%s\n",
			pr.RequestID, pr.Status,
			pr.SourceNamespace, pr.SourceKey,
			pr.TargetNamespace, pr.TargetKey,
			reqAt)
		count++
	}
	_ = tw.Flush()
	if count == 0 {
		_, _ = fmt.Fprintf(c.Stdout, "(no promotion requests with status=%q)\n", *status)
	}
	return 0
}

func (c *CLI) runPromoteApprove(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("promote approve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	actor := fs.String("actor", "user", "actor approving the request")
	notes := fs.String("notes", "", "optional approval notes")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}
	if fs.NArg() == 0 {
		return c.fail("usage: context promote approve <request-id> [--notes N]")
	}
	requestID := fs.Arg(0)

	pr, reqNamespace, err := c.Store.GetPromoteRequest(ctx, requestID)
	if err != nil {
		return c.fail(err.Error())
	}
	if pr.Status != "pending" {
		return c.fail(fmt.Sprintf("request status is %q, cannot approve", pr.Status))
	}

	approvalID := "appr-" + fmt.Sprintf("%d", time.Now().UnixNano())
	now := time.Now().UTC().Format(time.RFC3339)
	pa := contextstore.PromoteApproval{
		Type:             "promote.approve",
		ApprovalID:       approvalID,
		RequestID:        requestID,
		RequestNamespace: reqNamespace,
		ApprovedAt:       now,
		ApprovedBy:       *actor,
		Notes:            *notes,
	}
	approvalPayload, _ := json.Marshal(pa)
	if _, err := c.Store.AppendRecord(ctx, contextstore.AppendInput{
		Namespace: "user/promotions",
		Key:       approvalID,
		Actor:     *actor,
		Payload:   approvalPayload,
	}); err != nil {
		return c.fail(err.Error())
	}

	pr.Status = "approved"
	pr.ApprovalID = approvalID
	pr.ApprovedBy = *actor
	updPayload, _ := json.Marshal(pr)
	if _, err := c.Store.AppendRecord(ctx, contextstore.AppendInput{
		Namespace: reqNamespace,
		Key:       requestID,
		Actor:     *actor,
		Payload:   updPayload,
	}); err != nil {
		return c.fail(err.Error())
	}

	_, _ = fmt.Fprintf(c.Stdout, "Approved promotion request %s.\n", requestID)
	_, _ = fmt.Fprintf(c.Stdout, "  Approval ID: %s\n", approvalID)
	_, _ = fmt.Fprintf(c.Stdout, "  Next step:   context promote apply %s\n", requestID)
	return 0
}

func (c *CLI) runPromoteApply(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("promote apply", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	actor := fs.String("actor", "user", "actor applying the promotion")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}
	if fs.NArg() == 0 {
		return c.fail("usage: context promote apply <request-id>")
	}
	requestID := fs.Arg(0)

	pr, reqNamespace, err := c.Store.GetPromoteRequest(ctx, requestID)
	if err != nil {
		return c.fail(err.Error())
	}
	if pr.Status != "approved" {
		return c.fail(fmt.Sprintf("request status is %q; must be approved first", pr.Status))
	}

	pa, err := c.Store.GetPromoteApproval(ctx, requestID)
	if err != nil {
		return c.fail(err.Error())
	}

	srcRec, err := c.Store.GetByRecordID(ctx, pr.SourceRevisionID)
	if err != nil {
		return c.fail(fmt.Sprintf("source record not found: %v", err))
	}

	newRec, err := c.Store.AppendRecord(ctx, contextstore.AppendInput{
		Namespace: pr.TargetNamespace,
		Key:       pr.TargetKey,
		Actor:     *actor,
		Payload:   srcRec.Payload,
	})
	if err != nil {
		return c.fail(err.Error())
	}

	pr.Status = "applied"
	appliedPayload, _ := json.Marshal(pr)
	_, _ = c.Store.AppendRecord(ctx, contextstore.AppendInput{
		Namespace: reqNamespace,
		Key:       requestID,
		Actor:     *actor,
		Payload:   appliedPayload,
	})

	_ = c.Store.EmitPromote(ctx, contextstore.EventPromote, *actor, pr.TargetNamespace, pr.TargetKey, newRec.Revision, newRec.RecordID, nil)

	_, _ = fmt.Fprintf(c.Stdout, "Promotion applied.\n")
	_, _ = fmt.Fprintf(c.Stdout, "  Record ID:   %s\n", newRec.RecordID)
	_, _ = fmt.Fprintf(c.Stdout, "  Target:      %s/%s\n", pr.TargetNamespace, pr.TargetKey)
	_, _ = fmt.Fprintf(c.Stdout, "  Audit trail: %s → %s → %s\n", requestID, pa.ApprovalID, newRec.RecordID)
	return 0
}

func (c *CLI) runPromoteAccept(ctx context.Context, args []string) int {
	// accept = approve + apply in sequence
	fs := flag.NewFlagSet("promote accept", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	actor := fs.String("actor", "user", "actor")
	notes := fs.String("notes", "", "approval notes")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}
	if fs.NArg() == 0 {
		return c.fail("usage: context promote accept <request-id> [--notes N]")
	}
	requestID := fs.Arg(0)

	approveArgs := []string{requestID, "--actor", *actor}
	if *notes != "" {
		approveArgs = append(approveArgs, "--notes", *notes)
	}
	if code := c.runPromoteApprove(ctx, approveArgs); code != 0 {
		return code
	}
	return c.runPromoteApply(ctx, []string{requestID, "--actor", *actor})
}

func (c *CLI) runDoctor(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	output := fs.String("output", "json", "json|table")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}
	issues, err := c.Store.ScanConsistency(ctx)
	if err != nil {
		return c.fail(err.Error())
	}

	switch strings.TrimSpace(*output) {
	case "json", "":
		return c.writeJSON(map[string]any{"count": len(issues), "issues": issues})
	case "table":
		w := tabwriter.NewWriter(c.Stdout, 2, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "TYPE\tNAMESPACE\tKEY\tREVISION\tRECORD_ID\tDETAIL")
		for _, issue := range issues {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\n", issue.Type, issue.Namespace, issue.Key, issue.Revision, issue.RecordID, issue.Detail)
		}
		_ = w.Flush()
		return 0
	default:
		return c.fail("output must be json|table")
	}
}

func (c *CLI) runRepairHeads(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("repair-heads", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	output := fs.String("output", "json", "json|table")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}
	rebuilt, err := c.Store.RebuildHeads(ctx)
	if err != nil {
		return c.fail(err.Error())
	}
	issues, err := c.Store.ScanConsistency(ctx)
	if err != nil {
		return c.fail(err.Error())
	}
	switch strings.TrimSpace(*output) {
	case "json", "":
		return c.writeJSON(map[string]any{
			"rebuilt_heads":    rebuilt,
			"remaining_issues": len(issues),
		})
	case "table":
		w := tabwriter.NewWriter(c.Stdout, 2, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "REBUILT_HEADS\tREMAINING_ISSUES")
		_, _ = fmt.Fprintf(w, "%d\t%d\n", rebuilt, len(issues))
		_ = w.Flush()
		return 0
	default:
		return c.fail("output must be json|table")
	}
}

func (c *CLI) runAudit(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limit := fs.Int("limit", 50, "limit")
	cursor := fs.Int64("cursor", 0, "pagination cursor")
	namespace := fs.String("namespace", "", "namespace filter")
	eventType := fs.String("event-type", "", "event type filter")
	output := fs.String("output", "json", "json|table")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}
	if *cursor < 0 {
		return c.fail("cursor must be a non-negative integer")
	}
	events, nextCursor, err := c.Store.QueryAuditEvents(ctx, contextstore.AuditQuery{
		Limit:     *limit,
		Cursor:    *cursor,
		Namespace: strings.TrimSpace(*namespace),
		EventType: strings.TrimSpace(*eventType),
	})
	if err != nil {
		return c.fail(err.Error())
	}
	switch strings.TrimSpace(*output) {
	case "json", "":
		return c.writeJSON(map[string]any{"count": len(events), "items": events, "next_cursor": nextCursor})
	case "table":
		w := tabwriter.NewWriter(c.Stdout, 2, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "ID\tEVENT\tACTOR\tNAMESPACE\tKEY\tREVISION\tCREATED_AT")
		for _, event := range events {
			_, _ = fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%d\t%s\n", event.ID, event.EventType, event.Actor, event.Namespace, event.Key, event.Revision, event.CreatedAt)
		}
		if nextCursor != nil {
			_, _ = fmt.Fprintf(w, "NEXT_CURSOR\t%d\t\t\t\t\t\n", *nextCursor)
		}
		_ = w.Flush()
		return 0
	default:
		return c.fail("output must be json|table")
	}
}

func (c *CLI) runToken(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return c.fail("usage: context token <create|list|show|revoke|issue|rotate> ...")
	}
	switch args[0] {
	case "create":
		return c.runTokenCreate(ctx, args[1:])
	case "issue":
		return c.runTokenIssue(ctx, args[1:])
	case "rotate":
		return c.runTokenRotate(ctx, args[1:])
	case "revoke":
		return c.runTokenRevoke(ctx, args[1:])
	case "list":
		return c.runTokenList(ctx, args[1:])
	case "show":
		return c.runTokenShow(ctx, args[1:])
	default:
		return c.fail("unknown token command: " + args[0])
	}
}

func (c *CLI) runTokenCreate(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("token create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	name := fs.String("name", "", "token name/label (required)")
	clientID := fs.String("client-id", "", "client identity (e.g. app:claude, user)")
	scopesStr := fs.String("scopes", "", "comma-separated scopes (write,packet,promote.request,...)")
	namespacesStr := fs.String("namespaces", "", "comma-separated namespace globs (e.g. app/claude/*)")
	expires := fs.String("expires", "", "expiry as RFC3339 or YYYY-MM-DD")
	ttl := fs.String("ttl", "", "expiry as Go duration (e.g. 8760h)")
	output := fs.String("output", "table", "json|table")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}
	if strings.TrimSpace(*name) == "" {
		return c.fail("--name is required")
	}

	var scopes []string
	if strings.TrimSpace(*scopesStr) != "" {
		for _, s := range strings.Split(*scopesStr, ",") {
			if t := strings.TrimSpace(s); t != "" {
				scopes = append(scopes, t)
			}
		}
	}
	var globs []string
	if strings.TrimSpace(*namespacesStr) != "" {
		for _, s := range strings.Split(*namespacesStr, ",") {
			if t := strings.TrimSpace(s); t != "" {
				globs = append(globs, t)
			}
		}
	}

	var dur time.Duration
	if strings.TrimSpace(*expires) != "" {
		t, err := time.Parse(time.RFC3339, *expires)
		if err != nil {
			t, err = time.Parse("2006-01-02", *expires)
			if err != nil {
				return c.fail("--expires must be RFC3339 or YYYY-MM-DD")
			}
			t = t.UTC()
		}
		dur = time.Until(t)
		if dur <= 0 {
			return c.fail("--expires must be in the future")
		}
	} else if strings.TrimSpace(*ttl) != "" {
		parsed, err := time.ParseDuration(*ttl)
		if err != nil {
			return c.fail("--ttl must be a valid Go duration")
		}
		dur = parsed
	}

	token, meta, err := c.Store.CreateAuthToken(ctx, contextstore.TokenCreateInput{
		Label:          *name,
		ClientID:       *clientID,
		Scopes:         scopes,
		NamespaceGlobs: globs,
		TTL:            dur,
	})
	if err != nil {
		return c.fail(err.Error())
	}

	switch strings.TrimSpace(*output) {
	case "json":
		return c.writeJSON(map[string]any{
			"token": token, "id": meta.TokenID, "name": meta.Label,
			"client_id": meta.ClientID, "scopes": meta.Scopes,
			"namespace_globs": meta.NamespaceGlobs,
			"created_at":      meta.CreatedAt, "expires_at": meta.ExpiresAt,
		})
	default:
		_, _ = fmt.Fprintln(c.Stdout, "Token created. Copy this value now — it will not be shown again.")
		_, _ = fmt.Fprintln(c.Stdout)
		w := tabwriter.NewWriter(c.Stdout, 2, 4, 2, ' ', 0)
		_, _ = fmt.Fprintf(w, "  Token:\t%s\n", token)
		_, _ = fmt.Fprintf(w, "  ID:\t%s\n", meta.TokenID)
		_, _ = fmt.Fprintf(w, "  Name:\t%s\n", meta.Label)
		_, _ = fmt.Fprintf(w, "  Client:\t%s\n", meta.ClientID)
		_, _ = fmt.Fprintf(w, "  Scopes:\t%s\n", strings.Join(meta.Scopes, ", "))
		_, _ = fmt.Fprintf(w, "  Namespaces:\t%s\n", strings.Join(meta.NamespaceGlobs, ", "))
		_, _ = fmt.Fprintf(w, "  Expires:\t%s\n", meta.ExpiresAt)
		_ = w.Flush()
		return 0
	}
}

func (c *CLI) runTokenShow(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("token show", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	output := fs.String("output", "table", "json|table")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}
	if len(fs.Args()) == 0 {
		return c.fail("usage: context token show <token-id>")
	}
	tokenID := fs.Args()[0]
	meta, err := c.Store.GetAuthToken(ctx, tokenID)
	if err != nil {
		return c.fail(err.Error())
	}
	switch strings.TrimSpace(*output) {
	case "json":
		return c.writeJSON(map[string]any{
			"id": meta.TokenID, "name": meta.Label,
			"client_id": meta.ClientID, "scopes": meta.Scopes,
			"namespace_globs": meta.NamespaceGlobs,
			"created_at":      meta.CreatedAt, "expires_at": meta.ExpiresAt,
			"revoked": meta.RevokedAt != "",
		})
	default:
		status := "active"
		if meta.RevokedAt != "" {
			status = "revoked"
		}
		w := tabwriter.NewWriter(c.Stdout, 2, 4, 2, ' ', 0)
		_, _ = fmt.Fprintf(w, "  ID:\t%s\n", meta.TokenID)
		_, _ = fmt.Fprintf(w, "  Name:\t%s\n", meta.Label)
		_, _ = fmt.Fprintf(w, "  Client:\t%s\n", meta.ClientID)
		_, _ = fmt.Fprintf(w, "  Scopes:\t%s\n", strings.Join(meta.Scopes, ", "))
		_, _ = fmt.Fprintf(w, "  Namespaces:\t%s\n", strings.Join(meta.NamespaceGlobs, ", "))
		_, _ = fmt.Fprintf(w, "  Created:\t%s\n", meta.CreatedAt)
		_, _ = fmt.Fprintf(w, "  Expires:\t%s\n", meta.ExpiresAt)
		_, _ = fmt.Fprintf(w, "  Status:\t%s\n", status)
		_ = w.Flush()
		return 0
	}
}

func (c *CLI) runTokenIssue(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("token issue", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	label := fs.String("label", "default", "label")
	ttl := fs.String("ttl", "", "token ttl (e.g. 1h)")
	output := fs.String("output", "json", "json|table")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}
	var dur time.Duration
	if strings.TrimSpace(*ttl) != "" {
		parsed, err := time.ParseDuration(*ttl)
		if err != nil {
			return c.fail("ttl must be a valid duration")
		}
		dur = parsed
	}
	token, meta, err := c.Store.IssueAuthToken(ctx, *label, dur)
	if err != nil {
		return c.fail(err.Error())
	}
	switch strings.TrimSpace(*output) {
	case "json", "":
		return c.writeJSON(map[string]any{"token": token, "meta": meta})
	case "table":
		w := tabwriter.NewWriter(c.Stdout, 2, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "TOKEN\tTOKEN_ID\tLABEL\tEXPIRES_AT")
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", token, meta.TokenID, meta.Label, meta.ExpiresAt)
		_ = w.Flush()
		return 0
	default:
		return c.fail("output must be json|table")
	}
}

func (c *CLI) runTokenRotate(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("token rotate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	oldToken := fs.String("token", "", "existing token")
	label := fs.String("label", "", "new token label")
	ttl := fs.String("ttl", "", "new token ttl (e.g. 1h)")
	output := fs.String("output", "json", "json|table")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}
	var dur time.Duration
	if strings.TrimSpace(*ttl) != "" {
		parsed, err := time.ParseDuration(*ttl)
		if err != nil {
			return c.fail("ttl must be a valid duration")
		}
		dur = parsed
	}
	token, meta, err := c.Store.RotateAuthToken(ctx, *oldToken, *label, dur)
	if err != nil {
		return c.fail(err.Error())
	}
	switch strings.TrimSpace(*output) {
	case "json", "":
		return c.writeJSON(map[string]any{"token": token, "meta": meta})
	case "table":
		w := tabwriter.NewWriter(c.Stdout, 2, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "TOKEN\tTOKEN_ID\tLABEL\tEXPIRES_AT")
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", token, meta.TokenID, meta.Label, meta.ExpiresAt)
		_ = w.Flush()
		return 0
	default:
		return c.fail("output must be json|table")
	}
}

func (c *CLI) runTokenRevoke(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("token revoke", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	token := fs.String("token", "", "raw token value (legacy)")
	id := fs.String("id", "", "token-id to revoke by ID")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}

	// Allow positional arg as token-id
	if *id == "" && len(fs.Args()) > 0 {
		*id = fs.Args()[0]
	}

	if *id != "" {
		if err := c.Store.RevokeAuthTokenByID(ctx, *id); err != nil {
			return c.fail(err.Error())
		}
		_, _ = fmt.Fprintf(c.Stdout, "Token %s revoked. Requests using this token will be rejected immediately.\n", *id)
		return 0
	}
	if *token == "" {
		return c.fail("usage: context token revoke <token-id>  or  --token <raw-value>")
	}
	if err := c.Store.RevokeAuthToken(ctx, *token); err != nil {
		return c.fail(err.Error())
	}
	return c.writeJSON(map[string]any{"revoked": true})
}

func (c *CLI) runTokenList(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("token list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limit := fs.Int("limit", 50, "limit")
	showRevoked := fs.Bool("show-revoked", false, "include revoked tokens")
	output := fs.String("output", "table", "json|table")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}
	tokens, err := c.Store.ListAuthTokens(ctx, *limit)
	if err != nil {
		return c.fail(err.Error())
	}
	if !*showRevoked {
		active := tokens[:0]
		for _, t := range tokens {
			if t.RevokedAt == "" {
				active = append(active, t)
			}
		}
		tokens = active
	}
	switch strings.TrimSpace(*output) {
	case "json":
		return c.writeJSON(map[string]any{"count": len(tokens), "items": tokens})
	default:
		w := tabwriter.NewWriter(c.Stdout, 2, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "ID\tNAME\tCLIENT\tSCOPES\tNAMESPACES\tEXPIRES\tSTATUS")
		for _, t := range tokens {
			scopes := strings.Join(t.Scopes, ",")
			if len(t.Scopes) == 7 {
				scopes = "(all)"
			}
			namespaces := strings.Join(t.NamespaceGlobs, ",")
			expires := t.ExpiresAt
			if expires == "" {
				expires = "never"
			}
			status := "active"
			if t.RevokedAt != "" {
				status = "revoked"
			}
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				t.TokenID, t.Label, t.ClientID, scopes, namespaces, expires, status)
		}
		_ = w.Flush()
		return 0
	}
}

func (c *CLI) runBackup(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return c.fail("usage: context backup <export|restore> ...")
	}
	switch args[0] {
	case "export":
		return c.runBackupExport(ctx, args[1:])
	case "restore":
		return c.runBackupRestore(ctx, args[1:])
	case "verify":
		return c.runBackupVerify(args[1:])
	default:
		return c.fail("usage: context backup <export|restore|verify> ...")
	}
}

func (c *CLI) runBackupExport(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("backup export", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	// Backups are directories as of format v2 (manifest + whole-database
	// snapshot + payload tree), not the single JSON file v1 wrote.
	outPath := fs.String("out", "", "backup output directory (must be new or empty)")
	configPath := fs.String("config", "", "optional config.yaml to include in the backup")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}
	if strings.TrimSpace(*outPath) == "" {
		return c.fail("--out is required")
	}
	opts := contextstore.ExportBackupOptions{ConfigPath: *configPath}
	if err := c.Store.ExportBackupWithOptions(ctx, *outPath, opts); err != nil {
		return c.fail(err.Error())
	}
	info, err := contextstore.InspectBackup(*outPath)
	if err != nil {
		return c.fail(err.Error())
	}
	return c.writeJSON(map[string]any{
		"exported":       true,
		"path":           *outPath,
		"format_version": info.FormatVersion,
		"schema_version": info.SchemaVersion,
		"files":          info.Files,
		"record_count":   info.RecordCount,
	})
}

func (c *CLI) runBackupRestore(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("backup restore", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	inPath := fs.String("in", "", "backup directory (v2) or snapshot file (legacy v1)")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}
	if strings.TrimSpace(*inPath) == "" {
		return c.fail("--in is required")
	}
	if err := c.Store.RestoreBackup(ctx, *inPath); err != nil {
		return c.fail(err.Error())
	}
	return c.writeJSON(map[string]any{"restored": true, "path": *inPath})
}

func (c *CLI) runBackupVerify(args []string) int {
	fs := flag.NewFlagSet("backup verify", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	inPath := fs.String("in", "", "backup directory (v2) or snapshot file (legacy v1)")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}
	if strings.TrimSpace(*inPath) == "" {
		return c.fail("--in is required")
	}
	info, err := contextstore.InspectBackup(*inPath)
	if err != nil {
		return c.fail(err.Error())
	}
	return c.writeJSON(map[string]any{
		"verified":       true,
		"path":           *inPath,
		"format_version": info.FormatVersion,
		"schema_version": info.SchemaVersion,
		"created_at":     info.CreatedAt,
		"files":          info.Files,
		"record_count":   info.RecordCount,
	})
}

func (c *CLI) runHealth(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("health", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	output := fs.String("output", "json", "json|table")
	summary := fs.Bool("summary", false, "emit readiness status summary")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}
	report, err := c.Store.Readiness(ctx)
	if err != nil {
		return c.fail(err.Error())
	}
	if *summary {
		payload := map[string]any{
			"status":             report.Status,
			"healthy":            report.Healthy,
			"consistency_issues": report.ConsistencyIssues,
			"records_dir_exists": report.RecordsDirExists,
			"schema_version":     report.SchemaVersion,
		}
		switch strings.TrimSpace(*output) {
		case "json", "":
			return c.writeJSON(payload)
		case "table":
			w := tabwriter.NewWriter(c.Stdout, 2, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "STATUS\tHEALTHY\tSCHEMA_VERSION\tCONSISTENCY_ISSUES\tRECORDS_DIR_EXISTS")
			_, _ = fmt.Fprintf(w, "%s\t%t\t%d\t%d\t%t\n", report.Status, report.Healthy, report.SchemaVersion, report.ConsistencyIssues, report.RecordsDirExists)
			_ = w.Flush()
			return 0
		default:
			return c.fail("output must be json|table")
		}
	}

	switch strings.TrimSpace(*output) {
	case "json", "":
		return c.writeJSON(report)
	case "table":
		w := tabwriter.NewWriter(c.Stdout, 2, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "STATUS\tHEALTHY\tSCHEMA_VERSION\tCONSISTENCY_ISSUES\tRECORDS_DIR_EXISTS\tDB_PATH")
		_, _ = fmt.Fprintf(w, "%s\t%t\t%d\t%d\t%t\t%s\n", report.Status, report.Healthy, report.SchemaVersion, report.ConsistencyIssues, report.RecordsDirExists, report.DBPath)
		_ = w.Flush()
		return 0
	default:
		return c.fail("output must be json|table")
	}
}

func (c *CLI) runBootstrap(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("bootstrap", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	defaultApp := fs.String("default-app", "default", "default app namespace owner id")
	output := fs.String("output", "json", "json|table")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}

	seed := []contextstore.NamespacePolicyEntry{
		{Namespace: "user/goals", OwnerType: "user", OwnerID: "user"},
		{Namespace: "user/preferences", OwnerType: "user", OwnerID: "user"},
	}
	if strings.TrimSpace(*defaultApp) != "" {
		seed = append(seed, contextstore.NamespacePolicyEntry{
			Namespace: "app/" + strings.TrimSpace(*defaultApp) + "/session",
			OwnerType: "app",
			OwnerID:   strings.TrimSpace(*defaultApp),
		})
	}

	for _, entry := range seed {
		if err := c.Store.UpsertNamespacePolicy(ctx, entry); err != nil {
			return c.fail(err.Error())
		}
		if err := c.Policy.RegisterNamespace(entry.Namespace, entry.OwnerType, entry.OwnerID, entry.Policy); err != nil {
			return c.fail(err.Error())
		}
	}

	report, err := c.Store.Readiness(ctx)
	if err != nil {
		return c.fail(err.Error())
	}
	switch strings.TrimSpace(*output) {
	case "json", "":
		return c.writeJSON(map[string]any{
			"bootstrapped":      true,
			"seeded_namespaces": seed,
			"readiness":         report,
		})
	case "table":
		w := tabwriter.NewWriter(c.Stdout, 2, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "BOOTSTRAPPED\tHEALTHY\tSCHEMA_VERSION\tCONSISTENCY_ISSUES")
		_, _ = fmt.Fprintf(w, "true\t%t\t%d\t%d\n", report.Healthy, report.SchemaVersion, report.ConsistencyIssues)
		_ = w.Flush()
		return 0
	default:
		return c.fail("output must be json|table")
	}
}

func (c *CLI) runCompact(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("compact", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	keepRevisions := fs.Int("keep-revisions", 1, "revisions to keep per namespace/key")
	keepAudit := fs.Int("keep-audit", 1000, "audit events to keep")
	output := fs.String("output", "json", "json|table")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}

	deletedRevisions, err := c.Store.CompactRevisions(ctx, *keepRevisions)
	if err != nil {
		return c.fail(err.Error())
	}
	trimmedAudit, err := c.Store.TrimAuditEvents(ctx, *keepAudit)
	if err != nil {
		return c.fail(err.Error())
	}
	report, err := c.Store.Readiness(ctx)
	if err != nil {
		return c.fail(err.Error())
	}
	switch strings.TrimSpace(*output) {
	case "json", "":
		return c.writeJSON(map[string]any{
			"deleted_revisions": deletedRevisions,
			"trimmed_audit":     trimmedAudit,
			"readiness":         report,
		})
	case "table":
		w := tabwriter.NewWriter(c.Stdout, 2, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "DELETED_REVISIONS\tTRIMMED_AUDIT\tHEALTHY\tCONSISTENCY_ISSUES")
		_, _ = fmt.Fprintf(w, "%d\t%d\t%t\t%d\n", deletedRevisions, trimmedAudit, report.Healthy, report.ConsistencyIssues)
		_ = w.Flush()
		return 0
	default:
		return c.fail("output must be json|table")
	}
}

func (c *CLI) reloadPolicies(ctx context.Context) error {
	entries, err := c.Store.ListNamespacePolicies(ctx)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := c.Policy.RegisterNamespace(entry.Namespace, entry.OwnerType, entry.OwnerID, entry.Policy); err != nil {
			return err
		}
	}
	return nil
}

func (c *CLI) writeRecords(recs []contextstore.Record, output string) int {
	switch strings.TrimSpace(output) {
	case "json", "":
		return c.writeJSON(recs)
	case "table":
		w := tabwriter.NewWriter(c.Stdout, 2, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "NAMESPACE\tKEY\tREVISION\tACTOR")
		for _, rec := range recs {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", rec.Namespace, rec.Key, rec.Revision, rec.Actor)
		}
		_ = w.Flush()
		return 0
	default:
		return c.fail("output must be json|table")
	}
}

func (c *CLI) writeJSON(value any) int {
	enc := json.NewEncoder(c.Stdout)
	if err := enc.Encode(value); err != nil {
		return c.fail(err.Error())
	}
	return 0
}

func (c *CLI) fail(msg string) int {
	_, _ = fmt.Fprintln(c.Stderr, "error:", msg)
	return 1
}

func readPayload(raw, filePath string) (json.RawMessage, error) {
	if strings.TrimSpace(raw) == "" && strings.TrimSpace(filePath) == "" {
		return nil, errors.New("provide one of --json or --file")
	}
	if strings.TrimSpace(raw) != "" && strings.TrimSpace(filePath) != "" {
		return nil, errors.New("provide only one of --json or --file")
	}
	if strings.TrimSpace(raw) != "" {
		if !json.Valid([]byte(raw)) {
			return nil, errors.New("--json must be valid JSON")
		}
		return json.RawMessage(raw), nil
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	if !json.Valid(data) {
		return nil, errors.New("--file payload must be valid JSON")
	}
	return data, nil
}

func readSelector(raw, filePath string) (contextstore.Selector, error) {
	if strings.TrimSpace(raw) == "" && strings.TrimSpace(filePath) == "" {
		return contextstore.Selector{}, errors.New("provide one of --selector or --selector-file")
	}
	if strings.TrimSpace(raw) != "" && strings.TrimSpace(filePath) != "" {
		return contextstore.Selector{}, errors.New("provide only one of --selector or --selector-file")
	}
	var data []byte
	if strings.TrimSpace(raw) != "" {
		data = []byte(raw)
	} else {
		b, err := os.ReadFile(filePath)
		if err != nil {
			return contextstore.Selector{}, err
		}
		data = b
	}
	var out contextstore.Selector
	if err := json.Unmarshal(data, &out); err != nil {
		return contextstore.Selector{}, err
	}
	return out, nil
}

func (c *CLI) runMaintenance(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return c.fail("usage: context maintenance <trim|compact> ...")
	}
	switch args[0] {
	case "trim":
		return c.runMaintenanceTrim(ctx, args[1:])
	case "compact":
		return c.runMaintenanceCompact(ctx, args[1:])
	default:
		return c.fail("usage: context maintenance <trim|compact> ...")
	}
}

func (c *CLI) runMaintenanceTrim(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("maintenance trim", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	namespace := fs.String("namespace", "user/cache/%", "namespace pattern (SQL LIKE syntax)")
	retention := fs.String("retention", "72h", "trim records older than this duration")
	dryRun := fs.Bool("dry-run", false, "show what would be trimmed without modifying data")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}

	dur, err := time.ParseDuration(*retention)
	if err != nil {
		return c.fail("retention must be a valid duration (e.g. 72h)")
	}
	cutoff := time.Now().UTC().Add(-dur).Format(time.RFC3339)
	trimmed, err := c.Store.TrimRecords(ctx, *namespace, cutoff, *dryRun)
	if err != nil {
		return c.fail(err.Error())
	}

	if *dryRun {
		_, _ = fmt.Fprintf(c.Stdout, "Would trim %d records from %s (dry-run, no changes made)\n", trimmed, *namespace)
	} else if trimmed == 0 {
		_, _ = fmt.Fprintf(c.Stdout, "Nothing to trim in %s (0 records matched retention policy)\n", *namespace)
	} else {
		_, _ = fmt.Fprintf(c.Stdout, "Trimmed %d records from %s\n", trimmed, *namespace)
		_ = c.Store.EmitMaintenance(ctx, contextstore.EventMaintenanceTrim, "cli", *namespace, nil)
	}
	return 0
}

func (c *CLI) runMaintenanceCompact(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("maintenance compact", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	namespace := fs.String("namespace", "", "namespace pattern (SQL LIKE syntax, required)")
	maxRevisions := fs.Int("max-revisions", 1, "revisions to keep per (namespace,key)")
	dryRun := fs.Bool("dry-run", false, "show what would be compacted without modifying data")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}
	if strings.TrimSpace(*namespace) == "" {
		return c.fail("--namespace is required")
	}

	compacted, err := c.Store.CompactNamespace(ctx, *namespace, *maxRevisions, *dryRun)
	if err != nil {
		return c.fail(err.Error())
	}

	if *dryRun {
		_, _ = fmt.Fprintf(c.Stdout, "Would compact %d revisions from %s (dry-run, no changes made)\n", compacted, *namespace)
	} else if compacted == 0 {
		_, _ = fmt.Fprintf(c.Stdout, "Nothing to compact in %s (0 excess revisions found)\n", *namespace)
	} else {
		_, _ = fmt.Fprintf(c.Stdout, "Compacted %d revisions from %s\n", compacted, *namespace)
		_ = c.Store.EmitMaintenance(ctx, contextstore.EventMaintenanceCompact, "cli", *namespace, nil)
	}
	return 0
}

func (c *CLI) runPacket(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("packet", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var namespaces []string
	fs.Func("namespace", "namespace pattern (repeatable)", func(v string) error {
		namespaces = append(namespaces, v)
		return nil
	})
	budgetItems := fs.Int("budget-items", 0, "max items to include (0=unlimited)")
	budgetBytes := fs.Int("budget-bytes", 0, "max bytes to include (0=unlimited)")
	budgetTokens := fs.Int("budget-tokens", 0, "max estimated tokens to include (0=unlimited)")
	since := fs.String("since", "", "include records created after this RFC3339 time")
	until := fs.String("until", "", "include records created before this RFC3339 time")
	noPins := fs.Bool("no-pins", false, "skip user/pins/* prepend")
	// payload-mode accepts only "full" here. It used to also accept
	// "head_only", which cut the payload at 512 bytes mid-JSON; every other
	// value fell through to full payloads silently. Byte capping is now
	// -payload-max-bytes, and the flag is kept so an old invocation gets told
	// what to use instead of being quietly ignored.
	payloadMode := fs.String("payload-mode", "full", "payload mode: full (byte capping moved to -payload-max-bytes)")
	payloadMaxBytes := fs.Int("payload-max-bytes", 0, "cap each payload at N bytes (0=no cap); a capped item reports payload_head, payload_truncated and payload_bytes instead of payload")
	manifest := fs.String("manifest", "summary", "manifest detail: summary|full")
	output := fs.String("output", "human", "output mode: human|json|manifest-only")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}
	if *payloadMode != "" && *payloadMode != "full" {
		return c.fail("payload-mode accepts only \"full\"; the former -payload-mode=head_only is now -payload-max-bytes=512")
	}
	if *payloadMaxBytes < 0 {
		return c.fail("payload-max-bytes must be >= 0; omit it or pass 0 for no cap")
	}

	var items []map[string]any
	sources := map[string]int{}
	bytesSoFar, tokensSoFar := 0, 0
	pinsIncluded := 0
	truncated := false
	truncationReason := ""

	budgetExceeded := func() bool {
		if *budgetItems > 0 && len(items) >= *budgetItems {
			truncationReason = "budget.max_items"
			return true
		}
		if *budgetBytes > 0 && bytesSoFar >= *budgetBytes {
			truncationReason = "budget.max_bytes"
			return true
		}
		if *budgetTokens > 0 && tokensSoFar >= *budgetTokens {
			truncationReason = "budget.max_tokens_estimate"
			return true
		}
		return false
	}

	addRecord := func(rec contextstore.Record) bool {
		if budgetExceeded() {
			return false
		}
		payload := rec.Payload
		item := map[string]any{
			"record_id":  rec.RecordID,
			"namespace":  rec.Namespace,
			"key":        rec.Key,
			"revision":   rec.Revision,
			"actor":      rec.Actor,
			"created_at": rec.CreatedAt,
		}
		served := len(payload)
		if *payloadMaxBytes > 0 && len(payload) > *payloadMaxBytes {
			// The prefix of a JSON object is not valid JSON, so the head is a
			// string and `payload` is withheld rather than shortened.
			served = *payloadMaxBytes
			item["payload_head"] = string(payload[:served])
			item["payload_truncated"] = true
			item["payload_bytes"] = len(payload)
		} else {
			item["payload"] = payload
		}
		items = append(items, item)
		bytesSoFar += served
		tokensSoFar += (served + 3) / 4
		parts := strings.SplitN(rec.Namespace, "/", 3)
		nsKey := rec.Namespace
		if len(parts) >= 2 {
			nsKey = parts[0] + "/" + parts[1]
		}
		sources[nsKey]++
		return true
	}

	inTime := func(rec contextstore.Record) bool {
		if *since != "" && rec.CreatedAt < *since {
			return false
		}
		if *until != "" && rec.CreatedAt > *until {
			return false
		}
		return true
	}

	// Prepend pins unless --no-pins.
	if !*noPins {
		pinSel := contextstore.Selector{Namespaces: []string{"user/pins/*"}, RevisionScope: "head"}
		if pins, err := c.Store.Select(ctx, pinSel); err == nil {
			for _, rec := range pins {
				if inTime(rec) && addRecord(rec) {
					pinsIncluded++
				}
			}
		}
	}

	// Main selection.
	sel := contextstore.Selector{Namespaces: namespaces, RevisionScope: "head"}
	candidates, err := c.Store.Select(ctx, sel)
	if err != nil {
		return c.fail(err.Error())
	}
	itemsTotal := 0
	for _, rec := range candidates {
		if !inTime(rec) {
			continue
		}
		itemsTotal++
		if !addRecord(rec) {
			truncated = true
			break
		}
	}

	manifestData := map[string]any{
		"pins_included":   pinsIncluded,
		"items_total":     itemsTotal,
		"items_returned":  len(items) - pinsIncluded,
		"bytes_returned":  bytesSoFar,
		"tokens_estimate": tokensSoFar,
		"truncated":       truncated,
		"sources":         sources,
	}
	if truncationReason != "" {
		manifestData["truncation_reason"] = truncationReason
	}
	if *manifest == "full" {
		manifestData["selector"] = sel
	}

	if items == nil {
		items = []map[string]any{}
	}

	switch *output {
	case "json":
		enc := json.NewEncoder(c.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]any{"items": items, "manifest": manifestData})
	case "manifest-only":
		enc := json.NewEncoder(c.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(manifestData)
	default:
		itemsReturned := len(items) - pinsIncluded
		trunc := ""
		if truncated {
			trunc = fmt.Sprintf(" (truncated: %s)", truncationReason)
		}
		_, _ = fmt.Fprintf(c.Stdout, "%d items, %d bytes (~%d tokens) from %d namespace(s)%s\n",
			itemsReturned, bytesSoFar, tokensSoFar, len(sources), trunc)
	}
	return 0
}

func (c *CLI) runBroker(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return c.fail("usage: context broker <plan|fetch> ...")
	}
	switch args[0] {
	case "plan":
		return c.runBrokerPlan(ctx, args[1:])
	case "fetch":
		return c.runBrokerFetch(ctx, args[1:])
	default:
		return c.fail("unknown broker command: " + args[0])
	}
}

// brokerStopwords mirrors the server-side stopword list for deterministic keyword extraction in the CLI.
var brokerStopwords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true,
	"at": true, "be": true, "by": true, "for": true, "from": true,
	"has": true, "have": true, "in": true, "is": true, "it": true,
	"its": true, "of": true, "on": true, "or": true, "the": true,
	"this": true, "that": true, "to": true, "was": true, "with": true,
	"we": true, "i": true, "my": true, "me": true, "our": true,
	"will": true, "not": true, "but": true, "into": true, "just": true,
	"task": true, "work": true, "previous": true, "new": true,
	"using": true, "use": true, "via": true, "which": true, "all": true,
}

func brokerExtractKeywords(text string, n int) []string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !('a' <= r && r <= 'z') && !('0' <= r && r <= '9') && r != '-' && r != '_'
	})
	seen := map[string]bool{}
	var out []string
	for _, w := range words {
		if len(w) < 3 || brokerStopwords[w] || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
		if len(out) >= n {
			break
		}
	}
	return out
}

func brokerBuildPlan(intent, summary string, maxItems, maxTokens int) (namespaces []string, includePins bool, rationale string) {
	switch intent {
	case "resume_task":
		keywords := brokerExtractKeywords(summary, 3)
		for _, kw := range keywords {
			namespaces = append(namespaces, "user/memory/"+kw+"*")
		}
		namespaces = append(namespaces, "user/pins/*")
		includePins = true
		if len(keywords) > 0 {
			rationale = fmt.Sprintf("resume_task: patterns from keywords [%s] + user/pins/*", strings.Join(keywords, ", "))
		} else {
			namespaces = append(namespaces, "user/memory/*")
			rationale = "resume_task: no keywords extracted; using user/memory/* + user/pins/*"
		}
	case "boot_project":
		namespaces = []string{"user/memory/*", "user/pins/*"}
		includePins = true
		if maxItems < 100 {
			maxItems = 100
		}
		rationale = "boot_project: user/memory/* + user/pins/* for full project boot"
	case "review_session":
		namespaces = []string{"user/cache/*", "user/pins/*"}
		includePins = true
		rationale = "review_session: user/cache/* (last 24h) + user/pins/*"
	default:
		namespaces = []string{"user/*"}
		rationale = "custom: using user/* (no explicit constraints provided)"
	}
	return namespaces, includePins, rationale
}

func (c *CLI) runBrokerPlan(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("broker plan", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	intent := fs.String("intent", "custom", "intent: resume_task|boot_project|review_session|custom")
	summary := fs.String("summary", "", "task summary for keyword extraction (resume_task intent)")
	maxItems := fs.Int("budget-items", 50, "max items budget")
	maxTokens := fs.Int("budget-tokens", 4000, "max tokens estimate budget")
	output := fs.String("output", "human", "human|json")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}

	namespaces, includePins, rationale := brokerBuildPlan(*intent, *summary, *maxItems, *maxTokens)

	planNS := strings.Join(namespaces, ", ")
	pinsStr := "no"
	if includePins {
		pinsStr = "yes"
	}

	switch strings.TrimSpace(*output) {
	case "json":
		return c.writeJSON(map[string]any{
			"plan": map[string]any{
				"selector": map[string]any{
					"namespaces":     namespaces,
					"revision_scope": "head",
					"order":          []string{"created_desc"},
					"limit":          *maxItems,
				},
				"assembly": map[string]any{
					"include_pins":   includePins,
					"budget":         map[string]any{"max_items": *maxItems, "max_tokens_estimate": *maxTokens},
					"shape":          map[string]any{"include_payload": true, "payload_mode": "full"},
					"manifest_level": "summary",
				},
			},
			"rationale": rationale,
			"warnings":  []string{},
		})
	default:
		_, _ = fmt.Fprintf(c.Stdout, "Broker plan for intent: %s\n\n", *intent)
		w := tabwriter.NewWriter(c.Stdout, 2, 4, 2, ' ', 0)
		_, _ = fmt.Fprintf(w, "  Namespaces:\t%s\n", planNS)
		_, _ = fmt.Fprintf(w, "  Include pins:\t%s\n", pinsStr)
		_, _ = fmt.Fprintf(w, "  Budget:\t%d items, %d tokens estimate\n", *maxItems, *maxTokens)
		_, _ = fmt.Fprintf(w, "  Rationale:\t%s\n", rationale)
		_ = w.Flush()
		_, _ = fmt.Fprintln(c.Stdout)
		_, _ = fmt.Fprintln(c.Stdout, "  To execute this plan:")
		for _, ns := range namespaces {
			_, _ = fmt.Fprintf(c.Stdout, "    context packet --namespace %q", ns)
		}
		_, _ = fmt.Fprintf(c.Stdout, " --budget-items %d --budget-tokens %d\n", *maxItems, *maxTokens)
		_, _ = fmt.Fprintln(c.Stdout)
		_, _ = fmt.Fprintln(c.Stdout, "  Or: context broker fetch --intent "+*intent+" --summary \""+*summary+"\"")
		return 0
	}
}

func (c *CLI) runBrokerFetch(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("broker fetch", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	intent := fs.String("intent", "custom", "intent: resume_task|boot_project|review_session|custom")
	summary := fs.String("summary", "", "task summary for keyword extraction")
	maxItems := fs.Int("budget-items", 50, "max items budget")
	maxTokens := fs.Int("budget-tokens", 4000, "max tokens estimate budget")
	output := fs.String("output", "human", "human|json")
	if err := fs.Parse(args); err != nil {
		return c.fail(err.Error())
	}

	namespaces, _, rationale := brokerBuildPlan(*intent, *summary, *maxItems, *maxTokens)

	_, _ = fmt.Fprintln(c.Stdout, "Fetching context via broker plan...")
	_, _ = fmt.Fprintf(c.Stdout, "  Plan: %s → %d namespace pattern(s)\n", *intent, len(namespaces))

	// Execute: select from all namespaces, apply budget.
	var allRecs []contextstore.Record
	seen := map[string]bool{}
	for _, ns := range namespaces {
		recs, err := c.Store.Select(ctx, contextstore.Selector{
			Namespaces:    []string{ns},
			RevisionScope: "head",
			Limit:         *maxItems,
		})
		if err != nil {
			return c.fail(err.Error())
		}
		for _, rec := range recs {
			if !seen[rec.RecordID] {
				seen[rec.RecordID] = true
				allRecs = append(allRecs, rec)
			}
		}
	}

	// Apply budget.
	var items []contextstore.Record
	bytesSoFar, tokensSoFar := 0, 0
	truncated := false
	for _, rec := range allRecs {
		if *maxItems > 0 && len(items) >= *maxItems {
			truncated = true
			break
		}
		if *maxTokens > 0 && tokensSoFar >= *maxTokens {
			truncated = true
			break
		}
		items = append(items, rec)
		bytesSoFar += len(rec.Payload)
		tokensSoFar += (len(rec.Payload) + 3) / 4
	}

	truncStr := "No"
	if truncated {
		truncStr = "Yes"
	}

	switch strings.TrimSpace(*output) {
	case "json":
		return c.writeJSON(map[string]any{
			"items":     items,
			"count":     len(items),
			"bytes":     bytesSoFar,
			"tokens":    tokensSoFar,
			"truncated": truncated,
			"rationale": rationale,
		})
	default:
		_, _ = fmt.Fprintf(c.Stdout, "  Result: %d items, %dB (~%d tokens). Truncated: %s\n",
			len(items), bytesSoFar, tokensSoFar, truncStr)
		return 0
	}
}
