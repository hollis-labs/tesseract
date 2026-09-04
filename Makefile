.PHONY: test contracts contract-api contract-errors contract-metrics smoke smoke-invalid-token validate e2e-local e2e-managed contract-lint contract-commands contract-cli-list contract-cli-run frontend build install build-all install-all

BASE_URL ?= http://127.0.0.1:8089
TOKEN ?=
METRICS ?= 0
AUTH_MODE ?= none
INVALID_TOKEN_CHECK ?= 0
INVALID_TOKEN ?= invalid-token
ITERATION ?=
RUN_DATE ?= $(shell date +%F)
PROFILE ?= orchestrator
RUNLOG_OUT ?=
# Tests and contract helpers must not inherit developer-only path or benchmark
# overrides. In particular, TESS_MEASURE_DB opts otherwise-skipped tests into
# opening and mutating the named store. Provider keys are cleared as well so a
# hermetic gate cannot make an accidental network request.
HERMETIC_ENV := env \
	-u TESSERACT_DB_PATH \
	-u TESSERACT_WORKSPACE \
	-u TESSERACT_PLUGINS_DIR \
	-u TESSERACT_MEMORY_DECAY_INTERVAL \
	-u TESS_MEASURE_DB \
	-u TESS_MEASURE_NS \
	-u TESS_MEASURE_RANKING \
	-u OPENAI_API_KEY \
	-u ANTHROPIC_API_KEY
SUITE ?= all
# Stamped into main.version so `tesseract --version` reports a release rather
# than falling back to the VCS pseudo-version debug.ReadBuildInfo yields for a
# plain checkout build. `go install module@vX.Y.Z` needs no stamp — the module
# proxy supplies the real tag — so this exists for locally built binaries.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

TEST ?= go test ./...

ifeq ($(strip $(TOKEN)),)
SMOKE_TOKEN_ARG :=
else
SMOKE_TOKEN_ARG := --token $(TOKEN)
endif

ifeq ($(METRICS),1)
SMOKE_METRICS_ARG := --metrics
else
SMOKE_METRICS_ARG :=
endif

ifeq ($(INVALID_TOKEN_CHECK),1)
SMOKE_INVALID_TOKEN_ARG := --assert-invalid-token --invalid-token $(INVALID_TOKEN)
else
SMOKE_INVALID_TOKEN_ARG :=
endif

# The built web UI is committed at internal/webui/dist, so `build` and
# `install` deliberately do NOT depend on `frontend`: they compile the bundle
# already in the tree and need no Node toolchain, which keeps them working on
# a clone that has only Go — the same guarantee plain `go build`/`go install`
# already give. Chaining `frontend` into them made a Node install mandatory
# for everyone, including contributors who never touch frontend/.
#
# Rebuild the bundle explicitly with `make frontend`, or with `build-all` /
# `install-all` when a frontend change needs to reach the binary. Note that
# `frontend` rewrites internal/webui/dist, which is tracked — expect a diff.
frontend:
	cd frontend && npm ci --no-audit --no-fund && npm run build
	rm -rf internal/webui/dist
	cp -r frontend/dist internal/webui/dist

build:
	go build -ldflags "-X main.version=$(VERSION)" -o tesseract ./cmd/tesseract/

install:
	go install -ldflags "-X main.version=$(VERSION)" ./cmd/tesseract/

build-all: frontend build

install-all: frontend install

test:
	$(HERMETIC_ENV) $(TEST)

contract-api:
	$(HERMETIC_ENV) go test ./tests/integration -run APIContract -count=1

contract-errors:
	$(HERMETIC_ENV) go test ./tests/integration -run APIErrorContract -count=1

contract-metrics:
	$(HERMETIC_ENV) go test ./tests/integration -run MetricsContract -count=1

contracts: contract-api contract-errors contract-metrics

smoke:
	@scripts/tesseract-smoke.sh --base-url $(BASE_URL) --auth-mode $(AUTH_MODE) $(SMOKE_TOKEN_ARG) $(SMOKE_METRICS_ARG) $(SMOKE_INVALID_TOKEN_ARG)

smoke-invalid-token:
	@if [ "$(AUTH_MODE)" = "none" ]; then echo "AUTH_MODE must be static|managed for smoke-invalid-token"; exit 2; fi
	@if [ -z "$(TOKEN)" ]; then echo "TOKEN is required for smoke-invalid-token"; exit 2; fi
	@$(MAKE) smoke AUTH_MODE=$(AUTH_MODE) TOKEN=$(TOKEN) METRICS=$(METRICS) INVALID_TOKEN_CHECK=1 INVALID_TOKEN=$(INVALID_TOKEN) BASE_URL=$(BASE_URL)

validate: contract-cli-run
	@echo "validate complete: all contract suites passed"

e2e-local:
	scripts/tesseract-e2e-local.sh

e2e-managed:
	AUTH_MODE=managed scripts/tesseract-e2e-local.sh

contract-lint:
	scripts/contract-fixture-lint.sh

contract-commands:
	scripts/contract-suite-commands.sh

# Run a context contract command only after proving that every resolved path is
# beneath a fresh temporary root. Keeping this as one recipe macro ensures the
# list and execute targets cannot drift onto the developer's live store.
define run_contract_cli
	@set -eu; \
	root_dir="$$(mktemp -d "$${TMPDIR:-/tmp}/tesseract-contract.XXXXXX")"; \
	root_dir="$$(cd "$$root_dir" && pwd -P)"; \
	trap 'rm -rf "$$root_dir"' EXIT HUP INT TERM; \
	path_output="$$( \
		$(HERMETIC_ENV) \
		XDG_DATA_HOME="$$root_dir/data" \
		XDG_STATE_HOME="$$root_dir/state" \
		XDG_CACHE_HOME="$$root_dir/cache" \
		XDG_CONFIG_HOME="$$root_dir/config" \
		go run ./cmd/tesseract path \
	)"; \
	printf '%s\n' "$$path_output" | awk -v root="$$root_dir" ' \
		BEGIN { checked = 0 } \
		$$1 == "data" || $$1 == "state" || $$1 == "cache" || $$1 == "config" || \
		$$1 == "workspace-dir" || $$1 == "main-db" || $$1 == "config-file" || \
		$$1 == "records" || $$1 == "queue-db" { \
			value = $$0; sub(/^[^[:space:]]+[[:space:]]+/, "", value); \
			if (index(value, root "/") != 1) { \
				printf "resolved path escaped temporary root: %s -> %s\n", $$1, value > "/dev/stderr"; \
				exit 1; \
			} \
			checked++; \
		} \
		END { \
			if (checked != 9) { \
				printf "tesseract path returned %d checked paths, want 9\n", checked > "/dev/stderr"; \
				exit 1; \
			} \
		}'; \
	$(HERMETIC_ENV) \
		XDG_DATA_HOME="$$root_dir/data" \
		XDG_STATE_HOME="$$root_dir/state" \
		XDG_CACHE_HOME="$$root_dir/cache" \
		XDG_CONFIG_HOME="$$root_dir/config" \
		go run ./cmd/tesseract context contract $(1)
endef

contract-cli-list:
	$(call run_contract_cli,list --output table)

contract-cli-run:
	$(call run_contract_cli,run --suite $(SUITE) --execute --output table)
