.PHONY: build test race vet check load
build:
	go build -trimpath -o bin/gateway ./cmd/gateway
test:
	go test ./...
race:
	go test -race ./...
vet:
	go vet ./...
check: test race vet
load:
	GATEWAY_LOAD=1 go test ./internal/gateway -run '^TestLoad$$' -count=1 -v -timeout=3m
