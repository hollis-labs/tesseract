module github.com/hollis-labs/tesseract

go 1.26.1

require (
	github.com/anthropics/anthropic-sdk-go v1.41.0
	github.com/hollis-labs/go-embed-contracts v0.1.1
	github.com/hollis-labs/go-llm-contracts v0.1.0
	github.com/hollis-labs/go-llm-types v0.1.0
	github.com/hollis-labs/go-mcp v0.1.0
	github.com/hollis-labs/go-mcp-sanitize v0.1.0
	github.com/hollis-labs/go-modelsdev v0.2.0
	github.com/hollis-labs/go-otel v0.1.0
	github.com/hollis-labs/go-queue v0.1.0
	github.com/hollis-labs/plugin-sdk v0.3.0
	github.com/mark3labs/mcp-go v0.47.0
	github.com/oklog/ulid/v2 v2.1.1
	github.com/openai/openai-go v1.12.0
	gopkg.in/yaml.v3 v3.0.1
	modernc.org/sqlite v1.48.1
)

replace github.com/hollis-labs/go-mcp => ../../libs/go-mcp

replace github.com/hollis-labs/go-otel => ../../libs/go-otel

replace github.com/hollis-labs/go-queue => ../../libs/go-queue

replace github.com/hollis-labs/go-modelsdev => ../../libs/go-modelsdev

replace github.com/hollis-labs/go-llm-types => ../../libs/go-llm-types

replace github.com/hollis-labs/go-llm-contracts => ../../libs/go-llm-contracts

replace github.com/hollis-labs/go-embed-contracts => ../../libs/go-embed-contracts

require (
	github.com/bahlo/generic-list-go v0.2.0 // indirect
	github.com/buger/jsonparser v1.1.2 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/jsonschema-go v0.4.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.28.0 // indirect
	github.com/invopop/jsonschema v0.13.0 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/spf13/cast v1.7.1 // indirect
	github.com/standard-webhooks/standard-webhooks/libraries v0.0.0-20260427160145-3afa6683f8b2 // indirect
	github.com/tidwall/gjson v1.18.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	github.com/wk8/go-ordered-map/v2 v2.1.8 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel v1.43.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.41.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.41.0 // indirect
	go.opentelemetry.io/otel/metric v1.43.0 // indirect
	go.opentelemetry.io/otel/sdk v1.41.0 // indirect
	go.opentelemetry.io/otel/trace v1.43.0 // indirect
	go.opentelemetry.io/proto/otlp v1.9.0 // indirect
	golang.org/x/net v0.54.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.44.0 // indirect
	golang.org/x/text v0.37.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260217215200-42d3e9bedb6d // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260311181403-84a4fc48630c // indirect
	google.golang.org/grpc v1.79.2 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	modernc.org/libc v1.70.0 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)
