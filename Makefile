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
# CONTRACT_ROOT isolates the contract-test targets onto a throwaway root, so
# they never resolve the real ~/.local/state/tesseract and collide with a
# running daemon. go-apppaths (CW-20260517-0066) has no single "one base for
# everything" knob, so the whole layout is nested by pinning all four
# $XDG_*_HOME roots at the same directory — which is what CONTRACT_ENV does.
CONTRACT_ROOT ?= .tesseract/tmp/contract
CONTRACT_ENV := XDG_DATA_HOME=$(CONTRACT_ROOT) XDG_STATE_HOME=$(CONTRACT_ROOT) \
                XDG_CACHE_HOME=$(CONTRACT_ROOT) XDG_CONFIG_HOME=$(CONTRACT_ROOT)
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
	cd frontend && npm install && npm run build
	rm -rf internal/webui/dist
	cp -r frontend/dist internal/webui/dist

build:
	go build -ldflags "-X main.version=$(VERSION)" -o tesseract ./cmd/tesseract/

install:
	go install -ldflags "-X main.version=$(VERSION)" ./cmd/tesseract/

build-all: frontend build

install-all: frontend install

test:
	$(TEST)

contract-api:
	go test ./tests/integration -run APIContract -count=1

contract-errors:
	go test ./tests/integration -run APIErrorContract -count=1

contract-metrics:
	go test ./tests/integration -run MetricsContract -count=1

contracts: contract-api contract-errors contract-metrics

smoke:
	scripts/tesseract-smoke.sh --base-url $(BASE_URL) --auth-mode $(AUTH_MODE) $(SMOKE_TOKEN_ARG) $(SMOKE_METRICS_ARG) $(SMOKE_INVALID_TOKEN_ARG)

smoke-invalid-token:
	@if [ "$(AUTH_MODE)" = "none" ]; then echo "AUTH_MODE must be static|managed for smoke-invalid-token"; exit 2; fi
	@if [ -z "$(TOKEN)" ]; then echo "TOKEN is required for smoke-invalid-token"; exit 2; fi
	$(MAKE) smoke AUTH_MODE=$(AUTH_MODE) TOKEN=$(TOKEN) METRICS=$(METRICS) INVALID_TOKEN_CHECK=1 INVALID_TOKEN=$(INVALID_TOKEN) BASE_URL=$(BASE_URL)

validate: contracts
	@echo "validate complete: contract suites passed"

e2e-local:
	scripts/tesseract-e2e-local.sh

e2e-managed:
	AUTH_MODE=managed scripts/tesseract-e2e-local.sh

contract-lint:
	scripts/contract-fixture-lint.sh

contract-commands:
	scripts/contract-suite-commands.sh

contract-cli-list:
	@mkdir -p $(CONTRACT_ROOT)
	$(CONTRACT_ENV) go run ./cmd/tesseract context contract list --output table

contract-cli-run:
	@mkdir -p $(CONTRACT_ROOT)
	$(CONTRACT_ENV) go run ./cmd/tesseract context contract run --suite $(SUITE) --output table
