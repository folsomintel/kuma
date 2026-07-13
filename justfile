# kuma-infra local commands

set dotenv-load := false

relay_addr := env("KUMA_RELAY_ADDR", ":8080")
api_addr := env("KUMA_API_ADDR", ":8090")
relay_url := env("KUMA_RELAY_URL", "ws://127.0.0.1:8080")
relay_auth_secret := env("KUMA_RELAY_AUTH_SECRET", "dev-relay-secret")
api_token := env("KUMA_API_TOKEN", "dev-token")
fuse_url := env("FUSE_BASE_URL", "http://127.0.0.1:8081")
fuse_token := env("FUSE_TOKEN", "fuse-token")
kumad_download_url := env("KUMAD_DOWNLOAD_URL", "")
kumad_download_sha256 := env("KUMAD_DOWNLOAD_SHA256", "")

default:
	@just --list

# Build all binaries into bin/
build:
	mkdir -p bin
	go build -o bin/kuma ./cmd/kuma
	go build -o bin/kumad ./cmd/kumad
	go build -o bin/kuma-relay ./cmd/kuma-relay
	go build -o bin/kuma-api ./cmd/kuma-api

# Run the kuma CLI
# Usage: just kuma run
#    or: just kuma remote list
#    or: just kuma keys
kuma *args:
	KUMA_RELAY_AUTH_SECRET={{relay_auth_secret}} \
	KUMA_RELAY_URL={{relay_url}} \
	go run ./cmd/kuma {{args}}

# Run the opaque WebSocket relay
relay:
	KUMA_RELAY_AUTH_SECRET={{relay_auth_secret}} \
	go run ./cmd/kuma-relay -addr {{relay_addr}} -auth-secret {{relay_auth_secret}}

# Create local kumad config (machine id + E2E key + join token)
init:
	KUMA_RELAY_AUTH_SECRET={{relay_auth_secret}} \
	go run ./cmd/kumad -relay-url {{relay_url}} -auth-secret {{relay_auth_secret}} init

# Mint a client join token for an existing machine_id (for kuma remote add)
# Usage: just mint-client <machine_id>
mint-client machine_id:
	KUMA_RELAY_AUTH_SECRET={{relay_auth_secret}} \
	go run ./cmd/kumad mint-token -machine-id {{machine_id}} -role client

# Run kumad (uses config/env/flags; run `just init` first)
kumad *args:
	go run ./cmd/kumad {{args}}

# Run kumad against the local relay with explicit credentials
# Usage: just kumad-local <machine_id> <key> <join_token>
kumad-local machine_id key join_token:
	go run ./cmd/kumad \
		-machine-id {{machine_id}} \
		-key {{key}} \
		-join-token {{join_token}} \
		-relay-url {{relay_url}}

# Run the control-plane API (requires Fuse for cloud agents)
api:
	KUMA_API_TOKEN={{api_token}} \
	KUMA_RELAY_URL={{relay_url}} \
	KUMA_RELAY_AUTH_SECRET={{relay_auth_secret}} \
	FUSE_BASE_URL={{fuse_url}} \
	FUSE_TOKEN={{fuse_token}} \
	KUMAD_DOWNLOAD_URL={{kumad_download_url}} \
	KUMAD_DOWNLOAD_SHA256={{kumad_download_sha256}} \
	go run ./cmd/kuma-api -addr {{api_addr}}

# Register a BYO device via the local API
# Usage: just device-create [name]
device-create name="dev":
	curl -sS -X POST "http://127.0.0.1{{api_addr}}/v1/devices" \
		-H "Authorization: Bearer {{api_token}}" \
		-H "Content-Type: application/json" \
		-d '{"name":"{{name}}"}'

# Create a Fuse-backed cloud agent via the local API
# Usage: just cloud-create [name]
cloud-create name="sandbox":
	curl -sS -X POST "http://127.0.0.1{{api_addr}}/v1/cloud-agents" \
		-H "Authorization: Bearer {{api_token}}" \
		-H "Content-Type: application/json" \
		-d '{"name":"{{name}}","cpus":2,"ram_mb":2048}'

# List cloud agents
cloud-list:
	curl -sS "http://127.0.0.1{{api_addr}}/v1/cloud-agents" \
		-H "Authorization: Bearer {{api_token}}"

# Run tests
test:
	go test ./...

# Run tests with the race detector
test-race:
	go test -race ./...

# Format and tidy
fmt:
	go mod tidy
	gofmt -w ./cmd ./internal

# Vet
vet:
	go vet ./...

# Lint (requires golangci-lint)
lint:
	golangci-lint run

# Format, vet, and test
check: fmt vet test
