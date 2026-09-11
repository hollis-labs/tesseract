#!/usr/bin/env bash
#
# Verifies that a Tesseract deploy actually took — and, separately, that the
# work the queue performs is deployed too (CW-20260911-0039).
#
# WHY THIS EXISTS. `cerberus sync -> apply -> reload` deploys the API service.
# It does not deploy deferred work: every `tesseract mcp` child registers an
# embed worker against the SHARED queue and runs the binary it was spawned with
# for the life of its session. Measured twice, two days apart — 14 stale workers
# against 1 fresh, then 9 against 1 — and on both occasions every layer reported
# success while a new column stayed silently empty on most rows.
#
# WHAT A GREEN RETURN IS WORTH. Nothing on its own. `apply` has been observed
# succeeding in two distinct shapes that BOTH leave the old process serving:
# having written a plist, and having done nothing at all ("already current
# (launchd unchanged)"). It is scoped to the plist, not to the process. So this
# checks the process and the bytes, not the return value.
#
# WHAT IT DELIBERATELY DOES NOT DO. It does not grep the artifact for a string
# introduced by the change. That is a good ad-hoc technique for confirming one
# specific deploy landed, and a bad standing check: it couples to a log message
# that will legitimately change, and then it either breaks or gets weakened
# until it proves nothing. The durable question is "is what is running what we
# just built", and a SHA-256 comparison answers it without depending on any
# string surviving a refactor.
#
# Exit 0 means: the artifact is the build, the running service started after
# that artifact was written, and every other holder of the embed queue is
# running a binary at least as new as itself. A holder that is merely PRESENT
# is not a failure — once the daemon-claim gate has shipped, a conforming child
# holds the queue and never reserves from it. A holder that PREDATES its own
# binary is the failure, because it is executing an image older than what is
# installed.
#
# macOS/launchd, matching how this service is actually deployed.

set -uo pipefail

SERVICE_LABEL="${SERVICE_LABEL:-com.fragments-engine.cerberus.tesseract.tesseract-api-service}"
ARTIFACT="${ARTIFACT:-$HOME/.cerberus/apps/tesseract/tesseract-api-service/bin/tesseract-api-service}"
BUILD="${BUILD:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/tesseract}"
QUEUE_DB="${QUEUE_DB:-$HOME/.local/state/tesseract/queue.db}"

failures=0
note() { printf '  %s\n' "$*"; }
ok()   { printf 'ok    %s\n' "$*"; }
bad()  { printf 'FAIL  %s\n' "$*" >&2; failures=$((failures + 1)); }

printf 'tesseract deploy check\n\n'

# --- 1. the deployed artifact is the build -----------------------------------
if [ ! -x "$ARTIFACT" ]; then
	bad "no deployed artifact at $ARTIFACT"
elif [ ! -f "$BUILD" ]; then
	note "no local build at $BUILD — skipping the artifact/build comparison."
	note "Run 'make build' first if you are checking a deploy you just made."
else
	a=$(shasum -a 256 "$ARTIFACT" | awk '{print $1}')
	b=$(shasum -a 256 "$BUILD" | awk '{print $1}')
	if [ "$a" = "$b" ]; then
		ok "artifact matches the local build (${a:0:12})"
	else
		bad "the deployed artifact is NOT the local build"
		note "artifact $a"
		note "build    $b"
		note "'sync' copies the build to the artifact. If these differ, either sync did not run"
		note "or the build is newer than the sync."
	fi
fi

# --- 2. the service is running, and started AFTER the artifact was written ----
#
# This is the check that catches a green 'apply' that left the old process
# serving. A PID alone proves nothing: the old process has one too. What
# distinguishes them is whether the process predates the bytes it is supposed
# to be running.
pid=$(launchctl list 2>/dev/null | awk -v l="$SERVICE_LABEL" '$3 == l {print $1}')
if [ -z "$pid" ] || [ "$pid" = "-" ]; then
	bad "service $SERVICE_LABEL is not running"
else
	ok "service running as PID $pid"
	if [ -x "$ARTIFACT" ]; then
		artifact_mtime=$(stat -f %m "$ARTIFACT" 2>/dev/null)
		started=$(ps -p "$pid" -o lstart= 2>/dev/null | sed 's/^ *//')
		started_epoch=$(date -j -f "%a %b %e %T %Y" "$started" +%s 2>/dev/null)
		if [ -z "$started_epoch" ] || [ -z "$artifact_mtime" ]; then
			note "could not compare process start to artifact mtime on this system; skipped"
		elif [ "$started_epoch" -ge "$artifact_mtime" ]; then
			ok "process started after the artifact was written ($(date -r "$artifact_mtime" '+%H:%M:%S') -> $(date -r "$started_epoch" '+%H:%M:%S'))"
		else
			bad "the running process PREDATES the artifact — it is serving the OLD binary"
			note "artifact written $(date -r "$artifact_mtime" '+%Y-%m-%d %H:%M:%S')"
			note "process started  $(date -r "$started_epoch" '+%Y-%m-%d %H:%M:%S')"
			note "'apply' returns success without restarting anything. Run 'reload'."
		fi
	fi
fi

# --- 3. nothing but the service holds the embed queue -------------------------
#
# The one this task exists for. A stale holder is a process that will reserve
# embed jobs and run them with a binary from before the deploy.
if [ ! -f "$QUEUE_DB" ]; then
	note "no queue database at $QUEUE_DB yet; nothing to check"
else
	# A holder is hazardous when it PREDATES the binary it is running, because
	# then it is executing an image older than what is installed on disk. A
	# holder started after its binary was written is running current code — and
	# once the daemon-claim gate has shipped, such a child holds the queue
	# without ever reserving from it, which is correct and must not be reported
	# as a failure. Failing on mere presence would mean failing on every machine
	# with a live MCP session, i.e. always, and a check that always fails is one
	# nobody reads.
	holders=$(lsof -t "$QUEUE_DB" 2>/dev/null)
	stale=""
	current=0
	for p in $holders; do
		[ "$p" = "$pid" ] && continue
		exe=$(ps -p "$p" -o comm= 2>/dev/null | sed 's/^ *//')
		started=$(ps -p "$p" -o lstart= 2>/dev/null | sed 's/^ *//')
		started_epoch=$(date -j -f "%a %b %e %T %Y" "$started" +%s 2>/dev/null)
		exe_mtime=$(stat -f %m "$exe" 2>/dev/null)
		if [ -n "$started_epoch" ] && [ -n "$exe_mtime" ] && [ "$started_epoch" -ge "$exe_mtime" ]; then
			current=$((current + 1))
			continue
		fi
		stale="$stale $p"
	done
	if [ -z "$stale" ]; then
		if [ "$current" -gt 0 ]; then
			ok "$current other queue holder(s), all running current binaries"
		else
			ok "the deployed service is the only holder of the embed queue"
		fi
	else
		bad "processes older than their own binaries hold the embed queue:"
		for p in $stale; do
			note "$(ps -p "$p" -o pid=,lstart=,comm= 2>/dev/null | sed 's/^ *//')"
		done
		note ""
		note "Each runs the image it was SPAWNED with, so any embed job it takes is processed by"
		note "code from before the deploy — silently, with every layer reporting success."
		note "Do NOT kill them: that cost 14 live sessions their tools mid-turn on 2026-09-11 and"
		note "is the remedy CW-20260911-0039 exists to remove. They drain as sessions end, and a"
		note "child new enough to read the daemon's claim stands down on its own."
	fi
fi

printf '\n'
if [ "$failures" -gt 0 ]; then
	printf 'deploy check FAILED (%d)\n' "$failures" >&2
	exit 1
fi
printf 'deploy check passed\n'
