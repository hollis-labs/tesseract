package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/tesseract/internal/config"
	"github.com/hollis-labs/tesseract/internal/contextstore"
	"github.com/hollis-labs/tesseract/internal/sqlitedsn"
)

// Who owns the embed queue (CW-20260911-0039).
//
// The failure these guard against is not a crash. It is that the wrong process
// quietly does the work with a binary that predates the deploy, while every
// layer reports success. So the assertions are about WHICH process reserves a
// job, not about whether embedding works.

func openProbeQueueDB(t *testing.T) *sql.DB {
	t.Helper()
	return openProbeDBAt(t, filepath.Join(t.TempDir(), "queue.db"))
}

// openProbeDBAt opens a queue database the way production does, through the
// shared DSN helper (WAL and a busy timeout), and pins it to one connection.
// Without both, a supervisor reading the claim on a tick contends with the test
// writing it and the test fails with SQLITE_BUSY for reasons that have nothing
// to do with what it is asserting.
func openProbeDBAt(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", sqlitedsn.DSN(path))
	if err != nil {
		t.Fatalf("open probe queue db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestDaemonClaimDistinguishesItsFourStates is the cross-version contract.
//
// ABSENT and UNREADABLE are the pair that matters and the pair that is easiest
// to collapse. Absent means no daemon has ever run against this queue, so the
// child must work — that is the standalone install the whole design exists to
// preserve. Unreadable means a daemon exists and this binary cannot parse its
// claim, so the child must defer. Deferring on unreadable is only safe BECAUSE
// absence is distinguishable; collapse the two and a standalone install stops
// embedding the first time it meets a format it does not know.
func TestDaemonClaimDistinguishesItsFourStates(t *testing.T) {
	ctx := context.Background()
	db := openProbeQueueDB(t)

	if got := readDaemonClaim(ctx, db, daemonClaimFreshness); got != claimAbsent {
		t.Fatalf("no table at all reads as %s, want absent — a standalone install would stop embedding", got)
	}
	if err := ensureDaemonClaim(ctx, db); err != nil {
		t.Fatalf("ensure claim: %v", err)
	}
	// Table created but nothing claiming: a daemon has run here, none owns the
	// queue now, so the work is the child's. Same answer as stale, different
	// reason from absent.
	if got := readDaemonClaim(ctx, db, daemonClaimFreshness); got != claimStale {
		t.Fatalf("empty claim table reads as %s, want stale", got)
	}

	if err := writeDaemonClaim(ctx, db); err != nil {
		t.Fatalf("write claim: %v", err)
	}
	if got := readDaemonClaim(ctx, db, daemonClaimFreshness); got != claimFresh {
		t.Fatalf("a claim just written reads as %s, want fresh", got)
	}
	if readDaemonClaim(ctx, db, daemonClaimFreshness).childShouldWork() {
		t.Error("a child would take jobs while a live daemon owns the queue, which is the whole defect")
	}

	// Backdate past the window: the daemon is gone and the child must take over,
	// or a dead daemon means nothing ever embeds again.
	if _, err := db.ExecContext(ctx, `UPDATE `+daemonClaimTable+` SET updated_at = ?`,
		time.Now().Add(-2*daemonClaimFreshness).Unix()); err != nil {
		t.Fatalf("backdate claim: %v", err)
	}
	if got := readDaemonClaim(ctx, db, daemonClaimFreshness); got != claimStale {
		t.Fatalf("a claim past the freshness window reads as %s, want stale", got)
	}
	if !readDaemonClaim(ctx, db, daemonClaimFreshness).childShouldWork() {
		t.Error("no child would take jobs after the daemon died; the queue would never drain")
	}

	// A claim this binary cannot parse still means a daemon exists.
	if _, err := db.ExecContext(ctx, `DROP TABLE `+daemonClaimTable); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE `+daemonClaimTable+` (id INTEGER PRIMARY KEY, something_else TEXT)`); err != nil {
		t.Fatalf("create incompatible claim table: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO `+daemonClaimTable+` (id, something_else) VALUES (1, 'from a newer build')`); err != nil {
		t.Fatalf("insert incompatible row: %v", err)
	}
	got := readDaemonClaim(ctx, db, daemonClaimFreshness)
	if got != claimUnreadable {
		t.Fatalf("a claim in an unknown format reads as %s, want unreadable", got)
	}
	if got.childShouldWork() {
		t.Error("a child took jobs from a claim it could not parse; the safe direction is to defer, " +
			"and it is safe precisely because an absent table is distinguishable from an unreadable one")
	}
}

// TestChildGateTracksTheClaimInBothDirections is the core decision.
//
// Consulted per poll cycle rather than once at startup, so it tracks reality in
// BOTH directions: a daemon that dies later is noticed, and a daemon that starts
// later is deferred to. A one-shot check would get one of those wrong for the
// life of the session, which is the whole reason this is a claim read on every
// cycle rather than a flag decided at boot.
func TestChildGateTracksTheClaimInBothDirections(t *testing.T) {
	ctx := context.Background()
	db := openProbeQueueDB(t)
	gate := childCanReserve(db, daemonClaimFreshness, func(string, ...any) {})

	// No daemon has ever run here: the standalone case, and the child must work.
	if !gate(ctx) {
		t.Fatal("a child refused to take jobs on a store with no daemon claim at all. That is the " +
			"standalone install, and it would now never embed anything written over MCP")
	}

	if err := ensureDaemonClaim(ctx, db); err != nil {
		t.Fatalf("ensure claim: %v", err)
	}
	if err := writeDaemonClaim(ctx, db); err != nil {
		t.Fatalf("write claim: %v", err)
	}
	if gate(ctx) {
		t.Fatal("a child would take jobs while a live daemon owns the queue, which is the defect")
	}

	// The daemon dies.
	if _, err := db.ExecContext(ctx, `UPDATE `+daemonClaimTable+` SET updated_at = ?`,
		time.Now().Add(-2*daemonClaimFreshness).Unix()); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	if !gate(ctx) {
		t.Fatal("no child took over after the daemon's claim went stale; the queue would never drain")
	}

	// And comes back.
	if err := writeDaemonClaim(ctx, db); err != nil {
		t.Fatalf("refresh claim: %v", err)
	}
	if gate(ctx) {
		t.Fatal("a child kept taking jobs after the daemon returned; the decision is not being " +
			"re-made, so it is a startup check rather than a per-cycle one")
	}
}

// TestChildSubsystemDoesNotReserveWhileTheDaemonClaimIsFresh is the end-to-end
// half: the gate is actually wired into the worker, not merely correct in
// isolation.
//
// `attempts` is the observable because the driver increments it inside Pop. It
// is therefore a direct reading of whether this process RESERVED anything,
// rather than an inference from whether a handler ran.
func TestChildSubsystemDoesNotReserveWhileTheDaemonClaimIsFresh(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	layout := hermeticLayout(t)
	store, err := contextstore.Open(ctx, contextstore.Config{
		RootDir:    layout.DataDir(),
		RecordsDir: filepath.Join(layout.StateDir(), "records"),
		DBPath:     layout.MainDB(),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	mem, err := setupChildMemorySubsystemWithEmbedder(ctx, store, nil, layout, config.Defaults(), nil)
	if err != nil {
		t.Fatalf("setup child subsystem: %v", err)
	}
	t.Cleanup(func() { cancel(); _ = mem.Close() })

	probe := openProbeDBAt(t, mem.QueueDBPath)
	// A daemon owns the queue. The child created no table, so this test has to.
	if err := ensureDaemonClaim(ctx, probe); err != nil {
		t.Fatalf("ensure claim: %v", err)
	}
	if err := writeDaemonClaim(ctx, probe); err != nil {
		t.Fatalf("write claim: %v", err)
	}
	if _, err := probe.ExecContext(ctx,
		`INSERT INTO jobs (queue, type, payload, attempts, max_tries, available_at, created_at)
		 VALUES ('tesseract', 'probe.not.a.real.handler', '{}', 0, 1, ?, ?)`,
		time.Now().Unix(), time.Now().Unix()); err != nil {
		t.Fatalf("seed probe job: %v", err)
	}

	// Long enough for several poll cycles at the 3s default.
	time.Sleep(7 * time.Second)
	var present, attempts int
	if err := probe.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(MAX(attempts), 0) FROM jobs WHERE type = 'probe.not.a.real.handler'`).
		Scan(&present, &attempts); err != nil {
		t.Fatalf("read job state: %v", err)
	}
	if present == 0 || attempts != 0 {
		t.Fatalf("a child reserved a job while a live daemon claimed the queue "+
			"(present=%d attempts=%d). That is the defect this task exists to remove: the work "+
			"would be done by whatever binary this process was spawned with", present, attempts)
	}

	// Daemon dies: the same child must pick the job up without restarting.
	if _, err := probe.ExecContext(ctx, `UPDATE `+daemonClaimTable+` SET updated_at = ?`,
		time.Now().Add(-2*daemonClaimFreshness).Unix()); err != nil {
		t.Fatalf("backdate claim: %v", err)
	}
	waitFor(t, 20*time.Second, func() bool {
		var n int
		if err := probe.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM failed_jobs WHERE type = 'probe.not.a.real.handler'`).Scan(&n); err != nil {
			return false
		}
		return n > 0
	}, "the child never took the job after the daemon's claim went stale, so a dead daemon means "+
		"nothing embeds at all")
}

// TestDaemonSubsystemPublishesAClaimAndStillWorks is the guard on the one
// mistake that breaks everything quietly.
//
// If the daemon consulted the claim it would find its own, conclude a daemon
// owns the queue, stand down — and nothing in the system would embed anything.
// It fails closed and silent, in the area whose documented failure mode is
// already "silently unscored".
//
// The daemon here starts against a claim that is ALREADY fresh, which is
// exactly the state a restarted daemon meets: its predecessor's claim is
// seconds old. A daemon that gates on freshness never recovers from its own
// restart.
func TestDaemonSubsystemPublishesAClaimAndStillWorks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	layout := hermeticLayout(t)
	store, err := contextstore.Open(ctx, contextstore.Config{
		RootDir:    layout.DataDir(),
		RecordsDir: filepath.Join(layout.StateDir(), "records"),
		DBPath:     layout.MainDB(),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	mem, err := setupDaemonMemorySubsystemWithEmbedder(ctx, store, nil, layout, config.Defaults(), nil)
	if err != nil {
		t.Fatalf("setup daemon subsystem: %v", err)
	}
	t.Cleanup(func() { cancel(); _ = mem.Close() })

	probe := openProbeDBAt(t, mem.QueueDBPath)

	// The daemon publishes a claim, which is what children read.
	waitFor(t, 5*time.Second, func() bool {
		return readDaemonClaim(ctx, probe, daemonClaimFreshness) == claimFresh
	}, "the daemon never published a claim, so every child would think the queue is unowned")

	// And works anyway. Reservation is the observable: attempts goes above zero
	// only if this process polled and took the job.
	if _, err := probe.ExecContext(ctx,
		`INSERT INTO jobs (queue, type, payload, attempts, max_tries, available_at, created_at)
		 VALUES ('tesseract', 'probe.not.a.real.handler', '{}', 0, 1, ?, ?)`,
		time.Now().Unix(), time.Now().Unix()); err != nil {
		t.Fatalf("seed probe job: %v", err)
	}
	// The probe job names no registered handler, so go-queue treats it as a
	// permanent failure: it DELETEs the row from `jobs` and records it in
	// `failed_jobs`. Either half is proof the job was reserved, which is the
	// only thing under test here — whether this process polls at all.
	waitFor(t, 20*time.Second, func() bool {
		var n int
		if err := probe.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM failed_jobs WHERE type = 'probe.not.a.real.handler'`).Scan(&n); err != nil {
			return false
		}
		return n > 0
	}, "the daemon never reserved a queued job while its own claim was fresh — it is deferring to itself, "+
		"and in production that means nothing embeds at all")
}

// TestChildSubsystemNeverCreatesTheClaimTable protects the standalone case.
//
// If a child created the table, "absent" would stop meaning "no daemon has ever
// run here" the moment any MCP session started. Every subsequent child would
// read a daemon-exists state on a machine that has no daemon, defer forever,
// and the install would silently never embed anything written over MCP. That is
// the exact failure removing the child's worker outright would have caused, and
// preserving it is why this is a claim rather than a deletion.
func TestChildSubsystemNeverCreatesTheClaimTable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	layout := hermeticLayout(t)
	store, err := contextstore.Open(ctx, contextstore.Config{
		RootDir:    layout.DataDir(),
		RecordsDir: filepath.Join(layout.StateDir(), "records"),
		DBPath:     layout.MainDB(),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	mem, err := setupChildMemorySubsystemWithEmbedder(ctx, store, nil, layout, config.Defaults(), nil)
	if err != nil {
		t.Fatalf("setup child subsystem: %v", err)
	}
	t.Cleanup(func() { cancel(); _ = mem.Close() })

	probe := openProbeDBAt(t, mem.QueueDBPath)

	time.Sleep(200 * time.Millisecond)
	if got := readDaemonClaim(ctx, probe, daemonClaimFreshness); got != claimAbsent {
		t.Fatalf("after a child ran, the claim reads as %s, want absent. A child created the claim "+
			"table, so a standalone install now believes a daemon owns its queue and will never "+
			"embed anything again", got)
	}
}

func waitFor(t *testing.T, limit time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(msg)
}
