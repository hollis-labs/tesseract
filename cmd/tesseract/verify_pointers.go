package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/sqlitedsn"

	_ "modernc.org/sqlite"
)

// Exit codes, mirroring migrate-knowledge-kinds: both mean "refused to apply,
// nothing was written", kept distinct so a caller can tell an approval problem
// from a plan problem.
const (
	exitVerifyRefusedExpectation = 3
	exitVerifyPostCheckFailed    = 4
)

// runVerifyPointers resolves knowledge pointers and records what it observed.
// Dry-run by default; --apply commits.
//
// A dry-run still RESOLVES — it has to, or it could not report what it would
// write — but it opens the database read-only, so "a dry-run cannot write" is
// a property of the connection rather than of the code path.
//
// Verification never runs on the write path. This command is the only thing in
// the tree that records an observation, and nothing in knowledge.Store.Write
// calls it.
func runVerifyPointers(ctx context.Context, defaultDB string, args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("verify-pointers", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dbPath := fs.String("db", defaultDB, "path to the SQLite store to verify against (defaults to resolved layout DB)")
	apply := fs.Bool("apply", false, "record the observations; otherwise dry-run")
	jsonOut := fs.Bool("json", false, "emit the full plan as JSON instead of the human-readable summary")
	scope := fs.String("scope", string(memory.ScopeHeads), "heads|all — verify current heads only, or every knowledge revision")
	schemes := fs.String("schemes", memory.SchemeFile,
		"comma-separated pointer schemes to ATTEMPT. Network schemes (http, https) are opt-in: "+
			"a run that silently fetched every URL in the corpus would be a surprise, and a rehearsal "+
			"against a copy would still make real requests. Resolvable: "+strings.Join(memory.ResolvableSchemes(), ", "))
	recheckAfter := fs.Duration("recheck-after", 0,
		"skip pointers observed more recently than this (e.g. 168h). Zero checks everything. "+
			"This is what bounds log growth on a schedule: the check is skipped, so no row is written.")
	timeout := fs.Duration("timeout", memory.DefaultHTTPTimeout, "per-pointer HTTP timeout")
	concurrency := fs.Int("concurrency", 8, "how many pointers to resolve at once")
	expectRows := fs.Int("expect-rows", -1, "refuse to apply unless the freshly built plan has exactly this many rows")
	expectDigest := fs.String("expect-digest", "", "refuse to apply unless the freshly built plan digests to this value")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	if *dbPath == "" {
		fmt.Fprintln(stderr, "error: --db is required (and no layout DB was resolved)")
		return 1
	}
	verifyScope := memory.VerificationScope(*scope)
	if !verifyScope.Valid() {
		fmt.Fprintf(stderr, "error: --scope must be one of heads, all (got %q)\n", *scope)
		return 1
	}
	schemeList, schemeErr := parseSchemeList(*schemes)
	if schemeErr != nil {
		fmt.Fprintf(stderr, "error: %v\n", schemeErr)
		return 1
	}

	// Open the DB directly (not via contextstore.Open) so a run against an
	// arbitrary file copy doesn't materialize workspace layout side-effects.
	db, err := sql.Open("sqlite", verifyDSN(*dbPath, *apply))
	if err != nil {
		fmt.Fprintf(stderr, "error: open db: %v\n", err)
		return 1
	}
	defer func() { _ = db.Close() }()
	if pingErr := db.PingContext(ctx); pingErr != nil {
		fmt.Fprintf(stderr, "error: ping db: %v\n", pingErr)
		return 1
	}

	// Preflight: the verification log arrives with schema 13, and this command
	// opens the DB directly rather than through contextstore.Open, so it never
	// migrates anything — a dry-run holds a read-only handle and could not
	// migrate even if it wanted to. Say that plainly instead of surfacing a
	// raw "no such table" from three layers down.
	if missing, checkErr := verificationLogMissing(ctx, db); checkErr != nil {
		fmt.Fprintf(stderr, "error: check for the verification log: %v\n", checkErr)
		return 1
	} else if missing {
		fmt.Fprintf(stderr, "error: %s has no pointer_verifications table.\n"+
			"  It is created by schema migration 13, which runs when the store is opened by\n"+
			"  `tesseract serve` or `tesseract mcp`. This command opens the database directly and\n"+
			"  never migrates. Open the store once with a migrating command, then re-run.\n", *dbPath)
		return 1
	}

	networkEnabled := false
	for _, s := range schemeList {
		if s == memory.SchemeHTTP || s == memory.SchemeHTTPS {
			networkEnabled = true
		}
	}

	startedAt := time.Now().UTC()
	plan, err := memory.BuildVerificationPlan(ctx, db, memory.VerifyOptions{
		Scope:        verifyScope,
		Schemes:      schemeList,
		RecheckAfter: *recheckAfter,
		Concurrency:  *concurrency,
		Resolver:     memory.NewPointerResolver(*timeout, networkEnabled),
	})
	if err != nil {
		fmt.Fprintf(stderr, "error: build plan: %v\n", err)
		return 1
	}

	// The pre-run distribution is what makes the dry-run readable: it says
	// what the corpus looks like BEFORE anything is recorded, so the plan can
	// be read as a delta rather than as an absolute.
	before, distErr := memory.PointerHealthDistribution(ctx, db, verifyScope)
	if distErr != nil {
		fmt.Fprintf(stderr, "error: health distribution: %v\n", distErr)
		return 1
	}

	if *jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(verificationEnvelope{
			VerificationPlan: plan,
			Digest:           plan.Digest(),
			HealthBefore:     before,
		})
	} else {
		printVerificationSummary(stdout, plan, before)
	}

	if !*apply {
		if !*jsonOut {
			fmt.Fprintf(stdout, "\n(dry-run; nothing written — the database was opened read-only)\n")
			fmt.Fprintf(stdout, "To record exactly this plan:\n  tesseract verify-pointers --db %s --scope %s --schemes %s --apply --expect-rows %d --expect-digest %s\n",
				*dbPath, plan.Scope, strings.Join(plan.Schemes, ","), len(plan.Rows), plan.Digest())
			fmt.Fprintf(stdout, "\nNote: --expect-digest binds the apply to these exact observations. It is right for a\n"+
				"reviewed run and wrong for an unattended one — the world moves, and a host that came\n"+
				"back up between review and apply will (correctly) refuse. Omit it on a schedule.\n")
		}
		return 0
	}

	// Approval checks first: they describe the plan as a whole, so they
	// refuse before any per-row consideration.
	if *expectRows >= 0 && len(plan.Rows) != *expectRows {
		fmt.Fprintf(stderr, "error: refusing to apply — plan has %d row(s), --expect-rows said %d; re-run the dry-run\n",
			len(plan.Rows), *expectRows)
		return exitVerifyRefusedExpectation
	}
	if *expectDigest != "" && !strings.EqualFold(plan.Digest(), *expectDigest) {
		fmt.Fprintf(stderr, "error: refusing to apply — plan digest is %s, --expect-digest said %s; "+
			"the corpus or the world changed since the plan was reviewed, re-run the dry-run\n",
			plan.Digest(), *expectDigest)
		return exitVerifyRefusedExpectation
	}

	inserted, err := memory.ApplyVerificationPlan(ctx, db, plan)
	if err != nil {
		fmt.Fprintf(stderr, "error: apply: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "\nRecorded: %d pointer observation(s).\n", inserted)

	// Assert the post-condition rather than inferring it from the insert
	// loop's own count — measured by aggregate query, a different code path.
	landed, err := memory.CountObservationsSince(ctx, db, startedAt)
	if err != nil {
		fmt.Fprintf(stderr, "error: post-apply observation count: %v\n", err)
		return 1
	}
	if landed < len(plan.Rows) {
		fmt.Fprintf(stderr, "error: applied %d row(s) but only %d observation(s) are readable back; investigate\n",
			len(plan.Rows), landed)
		return exitVerifyPostCheckFailed
	}

	after, err := memory.PointerHealthDistribution(ctx, db, verifyScope)
	if err != nil {
		fmt.Fprintf(stderr, "error: post-apply health distribution: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "\nPointer health after:")
	printHealthDistribution(stdout, after)
	return 0
}

// verificationLogMissing reports whether the store predates schema 13.
func verificationLogMissing(ctx context.Context, db *sql.DB) (bool, error) {
	var name string
	err := db.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'pointer_verifications'`).Scan(&name)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, nil
}

// verifyDSN picks the connection string. A dry-run gets a read-only handle so
// it cannot write even if the code path were wrong; only --apply gets a
// writable one. mode=ro also precludes the WAL pragma, itself a write.
func verifyDSN(dbPath string, apply bool) string {
	if apply {
		return sqlitedsn.DSN(dbPath, "journal_mode(WAL)")
	}
	return sqlitedsn.DSN(dbPath) + "&mode=ro"
}

// parseSchemeList splits and validates --schemes. An unresolvable scheme is
// rejected at the flag rather than silently producing a run that records
// unverifiable for everything — asking to check a scheme nothing can check is
// a mistake worth naming.
func parseSchemeList(raw string) ([]string, error) {
	resolvable := map[string]struct{}{}
	for _, s := range memory.ResolvableSchemes() {
		resolvable[s] = struct{}{}
	}
	var out []string
	seen := map[string]struct{}{}
	for _, part := range strings.Split(raw, ",") {
		s := strings.TrimSpace(part)
		if s == "" {
			continue
		}
		if _, ok := resolvable[s]; !ok {
			return nil, fmt.Errorf("--schemes: %q has no resolver in this build (resolvable: %s)",
				s, strings.Join(memory.ResolvableSchemes(), ", "))
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--schemes: at least one scheme is required")
	}
	sort.Strings(out)
	return out, nil
}

// verificationEnvelope adds derived values to the JSON form without embedding
// them in the plan struct itself.
type verificationEnvelope struct {
	memory.VerificationPlan
	Digest       string         `json:"digest"`
	HealthBefore map[string]int `json:"health_before"`
}

func printVerificationSummary(w io.Writer, plan memory.VerificationPlan, before map[string]int) {
	fmt.Fprintf(w, "Pointer verification plan — %d observation(s) over %d candidate revision(s)\n",
		len(plan.Rows), plan.Candidates)
	fmt.Fprintf(w, "Plan digest: %s\n", plan.Digest())
	fmt.Fprintf(w, "Scope: %s   Schemes attempted: %s   Network: %s\n",
		plan.Scope, strings.Join(plan.Schemes, ", "), enabledWord(plan.NetworkEnabled))
	fmt.Fprintf(w, "Distinct targets resolved: %d (each resolved once; the outcome fans out to every revision citing it)\n",
		plan.DistinctTargets)

	fmt.Fprintln(w, "\nPointer health before this run:")
	printHealthDistribution(w, before)

	counts := plan.OutcomeCounts()
	fmt.Fprintln(w, "\nWould record:")
	if len(plan.Rows) == 0 {
		fmt.Fprintln(w, "  (nothing — no candidate matched the scope, schemes and recency filters)")
	}
	for _, o := range []memory.PointerOutcome{memory.OutcomeResolved, memory.OutcomeUnresolvable, memory.OutcomeUnverifiable} {
		if n, ok := counts[o]; ok {
			fmt.Fprintf(w, "  %-14s %d\n", string(o), n)
		}
	}

	if len(plan.Skipped) > 0 {
		fmt.Fprintln(w, "\nNot checked (reported, not dropped — \"we did not look\" is information):")
		for _, s := range plan.Skipped {
			fmt.Fprintf(w, "  %-20s %-14s %d\n", string(s.Kind), s.Scheme, s.Count)
		}
	}

	// Detail breakdown within each non-resolved outcome. This is where the
	// transient-vs-dead reading actually happens: "unverifiable" is only
	// useful once you can see whether it is timeouts or an unknown scheme.
	byDetail := map[string]map[string]int{}
	for _, r := range plan.Rows {
		if r.Outcome == memory.OutcomeResolved {
			continue
		}
		k := string(r.Outcome)
		if byDetail[k] == nil {
			byDetail[k] = map[string]int{}
		}
		byDetail[k][r.Detail]++
	}
	if len(byDetail) > 0 {
		fmt.Fprintln(w, "\nNon-resolved detail breakdown:")
		outcomes := make([]string, 0, len(byDetail))
		for k := range byDetail {
			outcomes = append(outcomes, k)
		}
		sort.Strings(outcomes)
		for _, o := range outcomes {
			details := make([]string, 0, len(byDetail[o]))
			for d := range byDetail[o] {
				details = append(details, d)
			}
			sort.Strings(details)
			for _, d := range details {
				fmt.Fprintf(w, "  %-14s %-28s %d\n", o, d, byDetail[o][d])
			}
		}
	}

	// Per-row listing of everything that is not resolved — the actionable set.
	var suspect []memory.PointerObservation
	for _, r := range plan.Rows {
		if r.Outcome != memory.OutcomeResolved {
			suspect = append(suspect, r)
		}
	}
	if len(suspect) > 0 {
		fmt.Fprintf(w, "\nNon-resolved pointers (%d):\n", len(suspect))
		for _, r := range suspect {
			fmt.Fprintf(w, "  %s  %s [%s]\n    %s:%s\n", r.RevisionID, string(r.Outcome), r.Detail, r.Scheme, r.Locator)
		}
	}
}

func printHealthDistribution(w io.Writer, dist map[string]int) {
	if len(dist) == 0 {
		fmt.Fprintln(w, "  (no knowledge revisions in scope)")
		return
	}
	keys := make([]string, 0, len(dist))
	total := 0
	for k, v := range dist {
		keys = append(keys, k)
		total += v
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(w, "  %-16s %d\n", k, dist[k])
	}
	fmt.Fprintf(w, "  %-16s %d\n", "(total)", total)
}

func enabledWord(b bool) string {
	if b {
		return "enabled"
	}
	return "disabled (http/https pointers are not fetched)"
}
