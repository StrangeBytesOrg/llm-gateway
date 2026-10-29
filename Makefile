.PHONY: build test race vet vuln check load
build:
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
