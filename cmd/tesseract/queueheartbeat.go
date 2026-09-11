package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"
)

// Who owns the embed queue, and how a process that is not the daemon finds out.
//
// # The problem this exists to remove
//
// Every `tesseract mcp` child registers an embed worker against the SHARED
// queue at ~/.local/state/tesseract/queue.db, and so does the API daemon. Any
// of them can reserve any job. An MCP child runs the binary it was spawned
// with, for the life of its session, so a deploy of the API service does not
// deploy the work the queue performs.
//
// Measured twice, two days apart: 14 stale children against 1 fresh daemon
// during the schema-20 deploy (CW-20260825-0018), and 9 against 1 during
// schema 21 (CW-20260911-0043), where the projected novelty series collected
// nothing for hours. Every layer reports success — the write returns 200, the
// revision is embedded, the queue drains, the service is on a verified new PID.
// Only the new column is silently empty, and only on MOST rows rather than all,
// which is harder to notice than nothing working.
//
// That matters most for exactly the work it keeps hitting: novelty is fixed at
// embed time and never recomputed, so a row a stale worker takes is not delayed,
// it is permanently unscored.
//
// # The shape, decided by Chrispian 2026-09-11
//
//	daemon -> writes a claim row here, refreshed on an interval
//	child  -> before reserving a job, reads the claim and stands down if it is live
//
// The child consults it once per POLL CYCLE rather than once at startup, so the
// decision tracks reality in both directions: a daemon that dies later is
// noticed, and a daemon that starts later is deferred to. A one-shot check at
// startup would get one of those wrong for the life of the session.
//
// Not "remove the child's worker": an install where no daemon ever runs would
// then never embed anything written over MCP. Preserving that standalone case
// is the whole reason this is a claim rather than a deletion.
//
// # Why PRESENCE is the load-bearing signal
//
// This row is read by processes running ANY binary version, so its schema is a
// cross-version contract. The contract is kept as small as possible: the ROW'S
// EXISTENCE means "a daemon has run against this queue", and its contents are
// only the freshness detail. That gives three states a future reader can act on
// without understanding the columns:
//
//	absent      -> no daemon has ever run here. Work. This is the standalone
//	               case, and it is why deferring on an unreadable row is safe:
//	               a standalone install has no row at all, so it is never
//	               stranded by a format it cannot parse.
//	readable    -> compare the timestamp against the freshness window.
//	unreadable  -> a daemon exists and this binary cannot parse its claim.
//	               Defer. Failing toward the daemon is the safe direction.
//
// The child never CREATES this table. Only the daemon does. If a child created
// it, "absent" would stop meaning "no daemon has ever run here" and the
// standalone case would break the first time a child started.
//
// # What this cannot do
//
// It cannot protect its own rollout. Children spawned BEFORE it ships never
// learn to look for the claim and keep taking jobs until their sessions end —
// hours to days. Children spawned after it defer to a live daemon, and the
// daemon always runs the just-deployed binary because `reload` restarts it. So
// the exposure is a one-time transition cost rather than a standing limitation,
// and `make deploy-check` is what covers the transition and, more durably,
// catches any future non-conforming holder.

const (
	// daemonClaimTable lives in queue.db rather than the context store because
	// that is the database every one of these processes already holds open, so
	// the check costs no new handle, no port and no token.
	daemonClaimTable = "tesseract_daemon_claim"

	// daemonClaimInterval is how often a live daemon refreshes its claim. It
	// refreshes on a TIMER rather than on job activity: an idle daemon still
	// owns the queue, and a claim that decayed while nothing was being written
	// would hand the queue to stale children during exactly the quiet period
	// before a deploy.
	daemonClaimInterval = 10 * time.Second

	// daemonClaimFreshness is how long a claim is honored after its last
	// refresh — three missed beats. Wider than the interval so an ordinary
	// scheduling hiccup does not look like a dead daemon.
	daemonClaimFreshness = 30 * time.Second
)

// claimState is what a child found when it looked.
type claimState int

const (
	claimAbsent     claimState = iota // no daemon has ever run here
	claimStale                        // a daemon ran, but not recently
	claimFresh                        // a daemon owns the queue right now
	claimUnreadable                   // a daemon exists; this binary cannot parse its claim
)

func (c claimState) String() string {
	switch c {
	case claimAbsent:
		return "absent"
	case claimStale:
		return "stale"
	case claimFresh:
		return "fresh"
	default:
		return "unreadable"
	}
}

// childShouldWork reports whether a child may run its embed worker.
//
// Note which states say yes. Absent and stale mean no daemon is doing the work,
// so the child must — that is the standalone case and the daemon-is-down case,
// and they are the reason the child's worker is not simply deleted.
func (c claimState) childShouldWork() bool {
	return c == claimAbsent || c == claimStale
}

// ensureDaemonClaim creates the claim table. DAEMON ONLY.
//
// A child must never call this. The table's absence is what distinguishes "no
// daemon has ever run against this queue" from "a daemon exists and is quiet",
// and a child that created it would erase that distinction permanently the
// first time one started on a standalone install.
func ensureDaemonClaim(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS `+daemonClaimTable+` (
	id         INTEGER PRIMARY KEY CHECK (id = 1),
	updated_at INTEGER NOT NULL,
	pid        INTEGER NOT NULL,
	version    TEXT NOT NULL
)`)
	if err != nil {
		return fmt.Errorf("create daemon claim table: %w", err)
	}
	return nil
}

// writeDaemonClaim refreshes the daemon's claim. DAEMON ONLY.
func writeDaemonClaim(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
INSERT INTO `+daemonClaimTable+` (id, updated_at, pid, version) VALUES (1, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET updated_at = excluded.updated_at,
                              pid = excluded.pid,
                              version = excluded.version`,
		time.Now().Unix(), os.Getpid(), buildVersion())
	if err != nil {
		return fmt.Errorf("write daemon claim: %w", err)
	}
	return nil
}

// readDaemonClaim reports what a child should do. CHILD ONLY.
//
// Every failure mode that is not "the table does not exist" resolves to
// claimUnreadable, which means defer. That is the safe direction and it is safe
// precisely because absence is distinguishable: a store with no daemon has no
// table, so it reads absent and works.
func readDaemonClaim(ctx context.Context, db *sql.DB, freshness time.Duration) claimState {
	var present int
	err := db.QueryRowContext(ctx,
		`SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?`, daemonClaimTable).Scan(&present)
	if errors.Is(err, sql.ErrNoRows) {
		return claimAbsent
	}
	if err != nil {
		// The queue database itself is unreadable. Defer rather than pile a
		// second worker onto a database already in trouble.
		return claimUnreadable
	}

	var updatedAt int64
	err = db.QueryRowContext(ctx,
		`SELECT updated_at FROM `+daemonClaimTable+` WHERE id = 1`).Scan(&updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		// The table exists but holds no claim. A daemon created it, so a daemon
		// has run here — but none is claiming the queue now, so the work is the
		// child's. This is the same reasoning as stale, not as absent.
		return claimStale
	}
	if err != nil {
		return claimUnreadable
	}
	if time.Since(time.Unix(updatedAt, 0)) <= freshness {
		return claimFresh
	}
	return claimStale
}

// runDaemonClaim keeps the daemon's claim fresh until ctx ends. DAEMON ONLY.
//
// The claim is written once immediately so that a child polling in the first
// seconds after a daemon starts sees it, rather than racing the first tick.
func runDaemonClaim(ctx context.Context, db *sql.DB, logf func(string, ...any)) {
	write := func() {
		if err := writeDaemonClaim(ctx, db); err != nil && ctx.Err() == nil {
			logf("daemon queue claim: %v", err)
		}
	}
	write()
	ticker := time.NewTicker(daemonClaimInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			write()
		}
	}
}

// childCanReserve builds the gate a child hands to its worker.
//
// It answers one question, once per poll cycle, immediately before the worker
// would reserve a job: may this process take work right now? A live daemon
// claim means no.
//
// # Why a gate and not a supervisor
//
// The first version of this supervised the worker instead — start it when the
// claim went stale, cancel it when the claim went fresh — because go-queue
// v0.1.0 had no pre-reserve hook. That worked, and it carried a residual race
// worth recording since the reasoning generalizes: canceling a worker's context
// is NOT the same as telling it to stop taking work. go-queue hands that same
// context to the handler AND to the Delete/Release bookkeeping after it, so a
// cancel that lands mid-job aborts an embedding already paid for and then fails
// to record the outcome. The reservation self-heals — the SQLite driver
// reclaims one older than its RetryAfter — but the attempt counter does not,
// and at MaxTries 3 a job aborted three times is moved to failed_jobs
// permanently.
//
// go-queue v0.2.0 adds WorkerOpts.CanReserve, which gates only whether a NEW
// reservation is attempted. A job already held simply finishes. That removes
// the race rather than narrowing it, and it is the decided semantics — "check
// before reserving" — rather than an approximation of them.
func childCanReserve(db *sql.DB, freshness time.Duration, logf func(string, ...any)) func(context.Context) bool {
	var (
		last      claimState
		haveState bool
	)
	return func(ctx context.Context) bool {
		state := readDaemonClaim(ctx, db, freshness)
		if !haveState || state != last {
			logf("embed queue claim is %s; this process %s take jobs",
				state, map[bool]string{true: "will", false: "will not"}[state.childShouldWork()])
			last, haveState = state, true
		}
		return state.childShouldWork()
	}
}
