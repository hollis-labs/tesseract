package memory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// --- Pointer verification: resolving knowledge pointers against the world ---
//
// Shaped as a plan/apply pair, like the kind migration in migrate_kinds.go: a
// dry-run builds the full set of observations and writes nothing, and --apply
// commits exactly that set.
//
// One thing differs from a migration, and it matters for how the approval
// flags behave. A migration's plan is a pure function of the corpus, so
// rebuilding it gives the same answer. This plan is a function of the corpus
// AND of the outside world at the moment it ran, so rebuilding it can
// legitimately give a different answer — a host that was down came back. The
// approval flags are still correct: they refuse when what would be written is
// no longer what was reviewed. They are simply not appropriate for an
// unattended scheduled run, which should omit them.
//
// Verification is NEVER on the write path. Nothing here is called from
// knowledge.Store.Write, and a pointer that fails to resolve does not fail,
// warn, or delay a write: a pointer unreachable at write time may be reachable
// an hour later, and one that resolves today may not tomorrow. Verification is
// an observation about the world, not a validation of the record.

// PointerResolver decides what one pointer's current state is.
//
// It returns an outcome and a short detail string. It does NOT return an
// error: a failure to reach the target IS the observation, and modeling it
// as an error would push callers toward the exact conflation this design
// avoids — treating "I could not tell" as "it is dead".
type PointerResolver interface {
	Resolve(ctx context.Context, scheme, locator string) (PointerOutcome, string)
}

// SchemeFile and the HTTP schemes are the schemes this build can resolve.
// Anything else is observed as unverifiable with an unsupported_scheme detail
// rather than passed over in silence: an entry pointing at a scheme nothing
// can check is a real fact about the corpus, and it must be queryable.
const (
	SchemeFile  = "file"
	SchemeHTTP  = "http"
	SchemeHTTPS = "https"
)

// ResolvableSchemes returns the schemes with a real resolver in this build.
func ResolvableSchemes() []string { return []string{SchemeFile, SchemeHTTP, SchemeHTTPS} }

// maxPointerRedirects caps redirect chains. A chain longer than this is
// observed as unverifiable, not unresolvable: a redirect loop is a statement
// about the server's configuration, not evidence the target is gone.
const maxPointerRedirects = 5

// DefaultHTTPTimeout bounds a single pointer's HTTP check.
const DefaultHTTPTimeout = 10 * time.Second

// defaultResolver dispatches on scheme.
type defaultResolver struct {
	httpClient *http.Client
	// allowNetwork gates the http/https resolvers. When false, an http(s)
	// pointer is reported as unverifiable/network_disabled rather than being
	// silently dropped — the entry stays visible as something nobody checked.
	allowNetwork bool
}

// NewPointerResolver returns the standard resolver: filesystem always,
// http/https only when allowNetwork is set.
func NewPointerResolver(timeout time.Duration, allowNetwork bool) PointerResolver {
	if timeout <= 0 {
		timeout = DefaultHTTPTimeout
	}
	return &defaultResolver{
		allowNetwork: allowNetwork,
		httpClient: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(_ *http.Request, via []*http.Request) error {
				if len(via) >= maxPointerRedirects {
					return fmt.Errorf("stopped after %d redirects", maxPointerRedirects)
				}
				return nil
			},
		},
	}
}

func (d *defaultResolver) Resolve(ctx context.Context, scheme, locator string) (PointerOutcome, string) {
	switch scheme {
	case SchemeFile:
		return resolveFile(locator)
	case SchemeHTTP, SchemeHTTPS:
		if !d.allowNetwork {
			return OutcomeUnverifiable, "network_disabled"
		}
		return d.resolveHTTP(ctx, locator)
	default:
		return OutcomeUnverifiable, "unsupported_scheme:" + scheme
	}
}

// resolveFile checks a filesystem pointer.
//
// Only os.IsNotExist earns unresolvable. Every other stat error — permission
// denied, a dead symlink component we cannot traverse, an I/O error, an
// unmounted volume — is unverifiable, because none of them establishes that
// the target is absent. An unmounted external drive is the case that makes
// this concrete: every path on it would otherwise be branded dead in one
// sweep, and the brand would outlive the unmount.
//
// A locator that is not absolute never reaches os.Stat at all. Statting a
// relative path resolves it against the JOB'S working directory, and a
// leading "~" against whoever ran it — so the recorded observation would be a
// fact about where the operator was standing rather than about the corpus,
// and two runs from different directories could legitimately disagree. The
// record does not carry the base such a path is relative to, so the honest
// answer is that we cannot tell. They surface with their own detail because
// they are an authoring defect with a different fix from a rotted path: the
// entry needs rewriting, not re-pointing.
func resolveFile(locator string) (PointerOutcome, string) {
	trimmed := strings.TrimSpace(locator)
	if trimmed == "" {
		return OutcomeUnverifiable, "empty_locator"
	}
	if strings.HasPrefix(trimmed, "~") {
		return OutcomeUnverifiable, "unexpanded_home_locator"
	}
	if !filepath.IsAbs(trimmed) {
		return OutcomeUnverifiable, "relative_locator"
	}
	_, err := os.Stat(trimmed)
	if err == nil {
		return OutcomeResolved, "stat_ok"
	}
	if errors.Is(err, os.ErrNotExist) {
		return OutcomeUnresolvable, "not_found"
	}
	if errors.Is(err, os.ErrPermission) {
		return OutcomeUnverifiable, "permission_denied"
	}
	return OutcomeUnverifiable, "stat_error"
}

// resolveHTTP checks an http(s) pointer.
//
// The status-code mapping is the heart of the transient-vs-dead distinction:
//
//	2xx                    resolved      — we saw it
//	401, 403               unverifiable  — the origin is up and refused US.
//	                                       The target may exist and probably
//	                                       does; we are not entitled to say.
//	404, 410               unresolvable  — an origin that answered told us it
//	                                       is not there. The only definitive
//	                                       negative HTTP offers.
//	408, 425, 429, 5xx     unverifiable  — the origin is struggling or
//	                                       throttling us. Says nothing about
//	                                       the target.
//	other 4xx              unverifiable  — a malformed request or a policy we
//	                                       do not model; our problem, not the
//	                                       target's.
//	transport failure      unverifiable  — DNS, TLS, refused, timeout,
//	                                       redirect loop. Silence is not a
//	                                       negative answer.
//
// A HEAD that comes back 405 or 501 is retried once as GET, because plenty of
// origins refuse HEAD while serving the resource perfectly well; treating that
// as an answer about the resource would be reading the method's status as the
// target's.
func (d *defaultResolver) resolveHTTP(ctx context.Context, locator string) (PointerOutcome, string) {
	outcome, detail, status := d.probe(ctx, http.MethodHead, locator)
	if status == http.StatusMethodNotAllowed || status == http.StatusNotImplemented {
		outcome, detail, _ = d.probe(ctx, http.MethodGet, locator)
	}
	return outcome, detail
}

func (d *defaultResolver) probe(ctx context.Context, method, locator string) (PointerOutcome, string, int) {
	req, err := http.NewRequestWithContext(ctx, method, locator, nil)
	if err != nil {
		// A locator we cannot even form a request from is malformed. That is a
		// defect in the record rather than an observation about the world, but
		// it is still not evidence the target is gone.
		return OutcomeUnverifiable, "malformed_locator", 0
	}
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return OutcomeUnverifiable, classifyTransportError(err), 0
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
		_ = resp.Body.Close()
	}()

	code := resp.StatusCode
	detail := "http_" + strconv.Itoa(code)
	switch {
	case code >= 200 && code < 300:
		return OutcomeResolved, detail, code
	case code == http.StatusNotFound || code == http.StatusGone:
		return OutcomeUnresolvable, detail, code
	default:
		return OutcomeUnverifiable, detail, code
	}
}

// classifyTransportError turns a transport failure into a stable detail
// string. Every branch is unverifiable — this only records WHICH silence,
// so a reader can tell a systemic outage from one bad host.
func classifyTransportError(err error) string {
	var timeoutErr interface{ Timeout() bool }
	if errors.As(err, &timeoutErr) && timeoutErr.Timeout() {
		return "timeout"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "no such host"), strings.Contains(msg, "dns"):
		return "dns_failure"
	case strings.Contains(msg, "connection refused"):
		return "connection_refused"
	case strings.Contains(msg, "redirect"):
		return "too_many_redirects"
	case strings.Contains(msg, "certificate"), strings.Contains(msg, "tls"):
		return "tls_error"
	}
	return "transport_error"
}

// --- Plan ---------------------------------------------------------------

// VerificationScope selects which knowledge revisions are candidates.
type VerificationScope string

const (
	// ScopeHeads verifies only current heads. The default: a superseded
	// revision's pointer describes a reference nobody follows any more, and
	// checking it doubles both the work and the log for a fact that changes
	// no decision.
	ScopeHeads VerificationScope = "heads"
	// ScopeAll verifies every knowledge revision, heads and history alike.
	ScopeAll VerificationScope = "all"
)

// Valid reports whether v is a recognized scope.
func (v VerificationScope) Valid() bool { return v == ScopeHeads || v == ScopeAll }

// VerifyOptions configures a verification run.
type VerifyOptions struct {
	// Scope selects heads-only (default) or every revision.
	Scope VerificationScope

	// Schemes limits which pointer schemes are ATTEMPTED. A candidate whose
	// scheme is not listed is reported as skipped and gets no row, so a
	// narrowed run never writes an observation it did not actually make.
	Schemes []string

	// RecheckAfter skips candidates whose most recent observation is newer
	// than this. Zero checks everything.
	//
	// This is what keeps the log from growing with run frequency: on a weekly
	// cadence with RecheckAfter=168h, a pointer contributes at most one row a
	// week no matter how often the job is invoked, because the check itself is
	// skipped rather than the row deduplicated.
	RecheckAfter time.Duration

	// Concurrency bounds simultaneous resolutions. <=1 is sequential.
	Concurrency int

	// Resolver overrides the default. Tests inject a deterministic one; a
	// production run leaves it nil.
	Resolver PointerResolver

	// Now overrides the clock, for tests.
	Now time.Time
}

// VerificationSkipReason says why a candidate was not checked.
type VerificationSkipReason string

const (
	// SkipSchemeNotSelected: the run's --schemes did not include this scheme.
	SkipSchemeNotSelected VerificationSkipReason = "scheme_not_selected"
	// SkipRecentlyChecked: an observation newer than RecheckAfter exists.
	SkipRecentlyChecked VerificationSkipReason = "recently_checked"
	// SkipNoExternalSource: scheme "nil" — nothing to resolve, by design.
	SkipNoExternalSource VerificationSkipReason = "no_external_source"
)

// VerificationSkip is one candidate the run did not check, and why. Skips are
// reported rather than dropped: "we did not look" is information, and a run
// that quietly narrowed its own scope is indistinguishable from a clean one.
type VerificationSkip struct {
	Kind   VerificationSkipReason `json:"reason"`
	Scheme string                 `json:"scheme"`
	Count  int                    `json:"count"`
}

// VerificationPlan is the full set of observations a run would record.
type VerificationPlan struct {
	// Rows are the observations that --apply would INSERT, one per candidate
	// revision that was actually checked.
	Rows []PointerObservation `json:"rows"`
	// Skipped aggregates the candidates that were not checked, by reason.
	Skipped []VerificationSkip `json:"skipped"`

	Scope VerificationScope `json:"scope"`
	// Schemes is what the run was allowed to attempt.
	Schemes []string `json:"schemes"`
	// Candidates is how many knowledge revisions matched the scope before any
	// scheme or recency narrowing.
	Candidates int `json:"candidates"`
	// DistinctTargets is how many unique (scheme, locator) pairs were
	// resolved. Lower than len(Rows) when several revisions cite one target;
	// each target is resolved once and the outcome fanned out, so two
	// revisions naming the same file can never disagree within a run.
	DistinctTargets int `json:"distinct_targets"`
	// NetworkEnabled records whether http(s) pointers were actually fetched.
	NetworkEnabled bool `json:"network_enabled"`
}

// OutcomeCounts tallies the plan's rows by outcome.
func (p VerificationPlan) OutcomeCounts() map[PointerOutcome]int {
	counts := map[PointerOutcome]int{}
	for _, r := range p.Rows {
		counts[r.Outcome]++
	}
	return counts
}

// Digest fingerprints exactly what this plan would write: every row's
// revision, target and observed result, including the detail.
//
// Detail is covered on purpose. It is the field that separates a rate limit
// from a scheme nothing can check, so two plans that agree on outcome but
// disagree on detail are not the same plan and an approval of one is not an
// approval of the other. The cost is that a purely transient change refuses an
// --expect-digest apply, which is the conservative direction: re-run the
// dry-run and look at what moved.
//
// checked_at is deliberately NOT covered — it is the run's own clock, so
// including it would make every digest unique and the flag useless.
func (p VerificationPlan) Digest() string {
	lines := make([]string, 0, len(p.Rows))
	for _, r := range p.Rows {
		lines = append(lines, strings.Join([]string{
			r.RevisionID, r.Scheme, r.Locator, string(r.Outcome), r.Detail,
		}, "\x00"))
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// pointerScanHeads and pointerScanAll select candidate revisions. Both carry
// the latest observation timestamp so RecheckAfter can be applied without a
// second round-trip per row.
const pointerScanSelect = `
	SELECT r.revision_id,
	       COALESCE(r.facet_pointer_scheme, ''),
	       COALESCE(r.facet_pointer_locator, ''),
	       (SELECT pv.checked_at FROM pointer_verifications pv
	         WHERE pv.revision_id = r.revision_id
	         ORDER BY pv.id DESC LIMIT 1)
	FROM memory_revisions r`

const pointerScanHeads = pointerScanSelect + `
	JOIN memory_state s ON s.current_revision = r.revision_id
	WHERE r.domain = 'knowledge'
	  AND COALESCE(r.facet_pointer_scheme, '') <> ''
	ORDER BY r.revision_id`

const pointerScanAll = pointerScanSelect + `
	WHERE r.domain = 'knowledge'
	  AND COALESCE(r.facet_pointer_scheme, '') <> ''
	ORDER BY r.revision_id`

// pointerCandidate is one revision considered for verification.
type pointerCandidate struct {
	RevisionID   string
	Scheme       string
	Locator      string
	LastChecked  *time.Time
	targetIsSame string // "scheme\x00locator" dedup key
}

// BuildVerificationPlan scans knowledge pointers, resolves the ones the
// options select, and returns the observations an apply would record.
//
// It performs the resolutions — a dry-run has to actually look at the world in
// order to report what it would write. What it does NOT do is touch the
// database: the caller may hand it a read-only handle and every path here
// still works.
func BuildVerificationPlan(ctx context.Context, db *sql.DB, opts VerifyOptions) (VerificationPlan, error) {
	if opts.Scope == "" {
		opts.Scope = ScopeHeads
	}
	if !opts.Scope.Valid() {
		return VerificationPlan{}, fmt.Errorf("%w: unknown verification scope %q", ErrInvalidInput, opts.Scope)
	}
	if len(opts.Schemes) == 0 {
		opts.Schemes = []string{SchemeFile}
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	selected := map[string]struct{}{}
	for _, s := range opts.Schemes {
		selected[s] = struct{}{}
	}
	_, httpSelected := selected[SchemeHTTP]
	_, httpsSelected := selected[SchemeHTTPS]
	networkEnabled := httpSelected || httpsSelected

	resolver := opts.Resolver
	if resolver == nil {
		resolver = NewPointerResolver(DefaultHTTPTimeout, networkEnabled)
	}

	query := pointerScanHeads
	if opts.Scope == ScopeAll {
		query = pointerScanAll
	}

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return VerificationPlan{}, fmt.Errorf("scan knowledge pointers: %w", err)
	}
	defer func() { _ = rows.Close() }()

	plan := VerificationPlan{
		// Non-nil so a machine consumer can len() the JSON without a nil check.
		Rows:           []PointerObservation{},
		Skipped:        []VerificationSkip{},
		Scope:          opts.Scope,
		Schemes:        append([]string{}, opts.Schemes...),
		NetworkEnabled: networkEnabled,
	}
	sort.Strings(plan.Schemes)

	var candidates []pointerCandidate
	skips := map[VerificationSkipReason]map[string]int{}
	noteSkip := func(reason VerificationSkipReason, scheme string) {
		if skips[reason] == nil {
			skips[reason] = map[string]int{}
		}
		skips[reason][scheme]++
	}

	for rows.Next() {
		var c pointerCandidate
		var lastChecked sql.NullString
		if scanErr := rows.Scan(&c.RevisionID, &c.Scheme, &c.Locator, &lastChecked); scanErr != nil {
			return VerificationPlan{}, fmt.Errorf("scan pointer row: %w", scanErr)
		}
		plan.Candidates++

		if c.Scheme == SchemeNil {
			noteSkip(SkipNoExternalSource, c.Scheme)
			continue
		}
		if _, ok := selected[c.Scheme]; !ok {
			noteSkip(SkipSchemeNotSelected, c.Scheme)
			continue
		}
		if lastChecked.Valid && opts.RecheckAfter > 0 {
			if t, parseErr := parseMemoryTime(lastChecked.String); parseErr == nil {
				c.LastChecked = &t
				if now.Sub(t) < opts.RecheckAfter {
					noteSkip(SkipRecentlyChecked, c.Scheme)
					continue
				}
			}
		}
		c.targetIsSame = c.Scheme + "\x00" + c.Locator
		candidates = append(candidates, c)
	}
	if rows.Err() != nil {
		return VerificationPlan{}, fmt.Errorf("iterate pointer rows: %w", rows.Err())
	}

	for reason, byScheme := range skips {
		for scheme, n := range byScheme {
			plan.Skipped = append(plan.Skipped, VerificationSkip{Kind: reason, Scheme: scheme, Count: n})
		}
	}
	sort.Slice(plan.Skipped, func(i, j int) bool {
		if plan.Skipped[i].Kind != plan.Skipped[j].Kind {
			return plan.Skipped[i].Kind < plan.Skipped[j].Kind
		}
		return plan.Skipped[i].Scheme < plan.Skipped[j].Scheme
	})

	// Resolve each distinct target once, then fan the outcome out to every
	// revision citing it.
	targets := map[string]pointerCandidate{}
	for _, c := range candidates {
		if _, ok := targets[c.targetIsSame]; !ok {
			targets[c.targetIsSame] = c
		}
	}
	plan.DistinctTargets = len(targets)

	observed, err := resolveTargets(ctx, resolver, targets, opts.Concurrency)
	if err != nil {
		return VerificationPlan{}, err
	}

	for _, c := range candidates {
		res := observed[c.targetIsSame]
		plan.Rows = append(plan.Rows, PointerObservation{
			RevisionID: c.RevisionID,
			Scheme:     c.Scheme,
			Locator:    c.Locator,
			Outcome:    res.outcome,
			Detail:     res.detail,
			CheckedAt:  now,
		})
	}
	sort.Slice(plan.Rows, func(i, j int) bool { return plan.Rows[i].RevisionID < plan.Rows[j].RevisionID })

	return plan, nil
}

type resolution struct {
	outcome PointerOutcome
	detail  string
}

// resolveTargets runs the resolver over each distinct target, up to
// concurrency at a time. Output is a map, so ordering of completion cannot
// leak into the plan.
func resolveTargets(ctx context.Context, resolver PointerResolver, targets map[string]pointerCandidate, concurrency int) (map[string]resolution, error) {
	out := make(map[string]resolution, len(targets))
	if len(targets) == 0 {
		return out, nil
	}
	if concurrency < 1 {
		concurrency = 1
	}

	keys := make([]string, 0, len(targets))
	for k := range targets {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)

	for _, k := range keys {
		if err := ctx.Err(); err != nil {
			wg.Wait()
			return nil, err
		}
		c := targets[k]
		key := k
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			outcome, detail := resolver.Resolve(ctx, c.Scheme, c.Locator)
			mu.Lock()
			out[key] = resolution{outcome: outcome, detail: detail}
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out, nil
}

// ApplyVerificationPlan inserts the plan's observations in one transaction.
//
// INSERT only. There is no UPDATE and no DELETE anywhere in this file, which
// is what makes the append-only claim structural rather than a convention: the
// prior observation for a revision is still on disk after this returns, and
// memory_revisions is not touched at all.
//
// Refuses a plan carrying an outcome outside the vocabulary, so a caller
// cannot write a value the read path would not understand.
func ApplyVerificationPlan(ctx context.Context, db *sql.DB, plan VerificationPlan) (inserted int, err error) {
	for _, r := range plan.Rows {
		if !r.Outcome.Valid() {
			return 0, fmt.Errorf("row %s carries outcome %q outside the vocabulary", r.RevisionID, r.Outcome)
		}
	}
	if len(plan.Rows) == 0 {
		return 0, nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO pointer_verifications (revision_id, scheme, locator, outcome, checked_at, detail)
VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, fmt.Errorf("prepare observation insert: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, r := range plan.Rows {
		res, exErr := stmt.ExecContext(ctx, r.RevisionID, r.Scheme, r.Locator,
			string(r.Outcome), r.CheckedAt.UTC().Format(memoryTimeFormat), r.Detail)
		if exErr != nil {
			return inserted, fmt.Errorf("insert observation for %s: %w", r.RevisionID, exErr)
		}
		n, _ := res.RowsAffected()
		inserted += int(n)
	}

	if cerr := tx.Commit(); cerr != nil {
		return inserted, fmt.Errorf("commit: %w", cerr)
	}
	return inserted, nil
}

// PointerHealthDistribution counts knowledge revisions by derived pointer
// health, over the given scope. It is the corpus-level answer to "how bad is
// it" and the post-apply check that observations actually landed.
//
// Revisions with no pointer facet are reported under the SQL sentinel rather
// than dropped, so the returned counts sum to the scanned population and a
// reader can verify nothing went missing.
func PointerHealthDistribution(ctx context.Context, db *sql.DB, scope VerificationScope) (map[string]int, error) {
	if scope == "" {
		scope = ScopeHeads
	}
	if !scope.Valid() {
		return nil, fmt.Errorf("%w: unknown verification scope %q", ErrInvalidInput, scope)
	}

	from := `FROM memory_revisions r JOIN memory_state s ON s.current_revision = r.revision_id`
	if scope == ScopeAll {
		from = `FROM memory_revisions r`
	}
	query := `SELECT (` + pointerHealthStatusExpr + `) AS health, COUNT(*) ` +
		from + ` WHERE r.domain = 'knowledge' GROUP BY health`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("pointer health distribution: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[string]int{}
	for rows.Next() {
		var health string
		var n int
		if scanErr := rows.Scan(&health, &n); scanErr != nil {
			return nil, fmt.Errorf("scan health count: %w", scanErr)
		}
		out[health] = n
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("iterate health counts: %w", rows.Err())
	}
	return out, nil
}

// CountObservationsSince counts rows in the verification log with checked_at
// at or after t.
//
// It is the post-apply assertion, and it is a different code path from the
// insert loop's own return value on purpose: a count the writer reports about
// itself cannot catch a write that silently did not land.
//
// Schema 16 normalizes the append-only log to fixed-width UTC nanoseconds and
// all writers use the same codec, so this range predicate is chronologically
// correct and can use idx_pointer_verifications_checked_at.
func CountObservationsSince(ctx context.Context, db *sql.DB, t time.Time) (int, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pointer_verifications WHERE checked_at >= ?`,
		t.UTC().Format(memoryTimeFormat)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count observations: %w", err)
	}
	return n, nil
}
