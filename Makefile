.PHONY: test contracts contract-api contract-errors contract-metrics smoke smoke-invalid-token validate e2e-local e2e-managed runlog-init contract-lint agents-boot-check contract-commands contract-cli-list contract-cli-run bootstrap-sync bootstrap-report bootstrap-sync-apply bootstrap-sync-report frontend build install

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
# CONTEXTD_ROOT isolates the contract-test targets onto a throwaway root.
# Post go-apppaths migration (CW-20260517-0066) it is a deprecated one-release
# shim — contextd maps it onto $XDG_*_HOME so the whole layout still nests
# under it. When the shim is removed, switch these targets to setting
# $XDG_DATA_HOME/$XDG_STATE_HOME/$XDG_CONFIG_HOME directly.
CONTEXTD_ROOT ?= .volon/tmp/contextd
SUITE ?= all

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

frontend:
	cd frontend && npm install && npm run build
	rm -rf internal/webui/dist
	cp -r frontend/dist internal/webui/dist

build: frontend
	go build -o contextd ./cmd/contextd/

install: frontend
	go install ./cmd/contextd/

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
	scripts/contextd-smoke.sh --base-url $(BASE_URL) --auth-mode $(AUTH_MODE) $(SMOKE_TOKEN_ARG) $(SMOKE_METRICS_ARG) $(SMOKE_INVALID_TOKEN_ARG)

smoke-invalid-token:
	@if [ "$(AUTH_MODE)" = "none" ]; then echo "AUTH_MODE must be static|managed for smoke-invalid-token"; exit 2; fi
	@if [ -z "$(TOKEN)" ]; then echo "TOKEN is required for smoke-invalid-token"; exit 2; fi
	$(MAKE) smoke AUTH_MODE=$(AUTH_MODE) TOKEN=$(TOKEN) METRICS=$(METRICS) INVALID_TOKEN_CHECK=1 INVALID_TOKEN=$(INVALID_TOKEN) BASE_URL=$(BASE_URL)

validate: contracts
	@echo "validate complete: contract suites passed"

e2e-local:
	scripts/contextd-e2e-local.sh

e2e-managed:
	AUTH_MODE=managed scripts/contextd-e2e-local.sh

runlog-init:
	@if [ -z "$(ITERATION)" ]; then echo "ITERATION is required"; exit 2; fi
	@if [ -n "$(RUNLOG_OUT)" ]; then \
		scripts/volon-runlog-init.sh --iteration $(ITERATION) --date $(RUN_DATE) --profile $(PROFILE) --out $(RUNLOG_OUT); \
	else \
		scripts/volon-runlog-init.sh --iteration $(ITERATION) --date $(RUN_DATE) --profile $(PROFILE); \
	fi

contract-lint:
	scripts/contract-fixture-lint.sh

agents-boot-check:
	scripts/agents-boot-drift-check.sh

contract-commands:
	scripts/contract-suite-commands.sh

contract-cli-list:
	@mkdir -p $(CONTEXTD_ROOT)
	CONTEXTD_ROOT=$(CONTEXTD_ROOT) go run ./cmd/contextd context contract list --output table

contract-cli-run:
	@mkdir -p $(CONTEXTD_ROOT)
	CONTEXTD_ROOT=$(CONTEXTD_ROOT) go run ./cmd/contextd context contract run --suite $(SUITE) --output table

bootstrap-report:
	scripts/bootstrap-sync.sh

bootstrap-sync:
	scripts/bootstrap-sync.sh --apply

bootstrap-sync-report:
	scripts/bootstrap-sync.sh

bootstrap-sync-apply:
	scripts/bootstrap-sync.sh --apply
