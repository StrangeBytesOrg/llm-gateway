.PHONY: build openapi ui ui-dev test race vet vuln check load
openapi:
	@tmp="$$(mktemp)"; \
	go run ./cmd/gateway -export-admin-openapi > "$$tmp" || { rm -f "$$tmp"; exit 1; }; \
	install -m 0644 "$$tmp" web/openapi.json; \
	rm -f "$$tmp"
	cd web && bun run openapi
ui:
	cd web && bun ci && bun run build
build: ui
	go build -trimpath -o bin/gateway ./cmd/gateway
test:
	go test ./...
race:
	go test -race ./...
vet:
	go vet ./...
vuln:
	go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
check: test race vet vuln
load:
	GATEWAY_LOAD=1 go test ./internal/gateway -run '^TestLoad$$' -count=1 -v -timeout=3m
