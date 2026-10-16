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
# PLATFORMS. macOS/launchd (the default there) and Linux/systemd --user (the
# hub). Everything platform-specific lives in the helper functions below; the
# three checks are the same on both. Overrides, all optional:
#
#   SERVICE_LABEL  launchd label (macOS) or systemd user unit name (Linux)
#   ARTIFACT       the deployed binary
#   BUILD          the local build to compare against (default ./tesseract)
#   BUILD_SHA      sha256 of the build, for when the build is not on this
#                  machine (the hub has no Go: it is cross-built elsewhere).
#                  Takes precedence over BUILD.
#   QUEUE_DB       the embed queue database
#   SYSTEMCTL_ARGS systemctl scope, default "--user" (Linux only)
#
# Hub invocation (Linux): the unit sets XDG_STATE_HOME, which a login shell
# does not have, so name the queue explicitly:
#
#   BUILD_SHA=<sha256 of the build> \
#   QUEUE_DB=/var/lib/fabric/xdg/state/tesseract/queue.db \
#   scripts/deploy-check.sh

set -uo pipefail

case "$(uname -s)" in
	Linux) OS=linux ;;
	*)     OS=darwin ;;
esac

if [ "$OS" = linux ]; then
	SERVICE_LABEL="${SERVICE_LABEL:-com.hollis-labs.cerberus.hub-tesseract.service}"
	ARTIFACT="${ARTIFACT:-/var/lib/fabric/bin/tesseract}"
	QUEUE_DB="${QUEUE_DB:-${XDG_STATE_HOME:-$HOME/.local/state}/tesseract/queue.db}"
else
	SERVICE_LABEL="${SERVICE_LABEL:-com.fragments-engine.cerberus.tesseract.tesseract-api-service}"
	ARTIFACT="${ARTIFACT:-$HOME/.cerberus/apps/tesseract/tesseract-api-service/bin/tesseract-api-service}"
	QUEUE_DB="${QUEUE_DB:-$HOME/.local/state/tesseract/queue.db}"
fi
BUILD="${BUILD:-$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")/.." && pwd)/tesseract}"
BUILD_SHA="${BUILD_SHA:-}"
SYSTEMCTL_ARGS="${SYSTEMCTL_ARGS:---user}"

failures=0
note() { printf '  %s\n' "$*"; }
ok()   { printf 'ok    %s\n' "$*"; }
bad()  { printf 'FAIL  %s\n' "$*" >&2; failures=$((failures + 1)); }

# --- platform helpers ----------------------------------------------------------
sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'
	else shasum -a 256 "$1" | awk '{print $1}'; fi
}

# mtime of a file, in epoch seconds ("" if unreadable).
file_mtime() {
	if [ "$OS" = linux ]; then stat -L -c %Y "$1" 2>/dev/null; else stat -f %m "$1" 2>/dev/null; fi
}

fmt_epoch() {
	if [ "$OS" = linux ]; then date -d "@$1" "$2"; else date -r "$1" "$2"; fi
}

# PID of the service, or "" when it is not running.
service_pid() {
	if [ "$OS" = linux ]; then
		# shellcheck disable=SC2086
		local p; p=$(systemctl $SYSTEMCTL_ARGS show "$SERVICE_LABEL" -p MainPID --value 2>/dev/null)
		[ "$p" = 0 ] && p=""
		printf '%s' "$p"
	else
		launchctl list 2>/dev/null | awk -v l="$SERVICE_LABEL" '$3 == l {print $1}'
	fi
}

# Process start time in epoch seconds ("" if it cannot be determined). Linux
# derives it from elapsed seconds, which is timezone-independent.
proc_start_epoch() {
	if [ "$OS" = linux ]; then
		local et; et=$(ps -p "$1" -o etimes= 2>/dev/null | tr -d ' ')
		[ -n "$et" ] && echo $(( $(date +%s) - et ))
	else
		local st; st=$(ps -p "$1" -o lstart= 2>/dev/null | sed 's/^ *//')
		date -j -f "%a %b %e %T %Y" "$st" +%s 2>/dev/null
	fi
}

# PIDs holding $1 open.
file_holders() {
	if [ "$OS" = linux ]; then
		# One find over /proc/*/fd symlinks; a per-fd readlink loop is far too slow
		# on a host with thousands of descriptors.
		find /proc -maxdepth 3 -path '/proc/[0-9]*/fd/*' -lname "$1" 2>/dev/null \
			| awk -F/ '{print $3}' | sort -u
	else
		lsof -t "$1" 2>/dev/null
	fi
}

# mtime of the binary a process is running ("" if unknown). On Linux a binary
# that was replaced on disk reads "(deleted)": it is by definition older than
# what is installed, so report 2^62 and let the caller call it stale.
proc_exe_mtime() {
	if [ "$OS" = linux ]; then
		case "$(readlink "/proc/$1/exe" 2>/dev/null)" in
			*"(deleted)") echo 4611686018427387904 ;;
			"") ;;
			*) stat -L -c %Y "/proc/$1/exe" 2>/dev/null ;;
		esac
	else
		local exe; exe=$(ps -p "$1" -o comm= 2>/dev/null | sed 's/^ *//')
		stat -f %m "$exe" 2>/dev/null
	fi
}

describe_proc() {
	if [ "$OS" = linux ]; then ps -p "$1" -o pid=,lstart=,args= 2>/dev/null | cut -c1-160 | sed 's/^ *//'
	else ps -p "$1" -o pid=,lstart=,comm= 2>/dev/null | sed 's/^ *//'; fi
}

printf 'tesseract deploy check\n\n'

# --- 1. the deployed artifact is the build -----------------------------------
if [ ! -x "$ARTIFACT" ]; then
	bad "no deployed artifact at $ARTIFACT"
elif [ -z "$BUILD_SHA" ] && [ ! -f "$BUILD" ]; then
	note "no local build at $BUILD and no BUILD_SHA — skipping the artifact/build comparison."
	note "Run 'make build' first, or pass BUILD_SHA=<sha256> if the build is elsewhere."
else
	a=$(sha256_of "$ARTIFACT")
	b="${BUILD_SHA:-$(sha256_of "$BUILD")}"
	b=$(printf '%s' "$b" | tr 'A-F' 'a-f')
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
pid=$(service_pid)
if [ -z "$pid" ] || [ "$pid" = "-" ]; then
	bad "service $SERVICE_LABEL is not running"
else
	ok "service running as PID $pid"
	if [ -x "$ARTIFACT" ]; then
		artifact_mtime=$(file_mtime "$ARTIFACT")
		started_epoch=$(proc_start_epoch "$pid")
		if [ -z "$started_epoch" ] || [ -z "$artifact_mtime" ]; then
			note "could not compare process start to artifact mtime on this system; skipped"
		elif [ "$started_epoch" -ge "$artifact_mtime" ]; then
			ok "process started after the artifact was written ($(fmt_epoch "$artifact_mtime" '+%H:%M:%S') -> $(fmt_epoch "$started_epoch" '+%H:%M:%S'))"
		else
			bad "the running process PREDATES the artifact — it is serving the OLD binary"
			note "artifact written $(fmt_epoch "$artifact_mtime" '+%Y-%m-%d %H:%M:%S')"
			note "process started  $(fmt_epoch "$started_epoch" '+%Y-%m-%d %H:%M:%S')"
			note "'apply' returns success without restarting anything. Run 'reload' (macOS) or restart the unit (Linux)."
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
	holders=$(file_holders "$QUEUE_DB")
	stale=""
	current=0
	for p in $holders; do
		[ "$p" = "$pid" ] && continue
		# An MCP child can exit between listing and inspection; it holds nothing now.
		ps -p "$p" >/dev/null 2>&1 || continue
		started_epoch=$(proc_start_epoch "$p")
		exe_mtime=$(proc_exe_mtime "$p")
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
			note "$(describe_proc "$p")"
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
