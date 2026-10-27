# Local validation

Validated on 2026-10-25 using Go `1.27.3-X:nodwarf5`, Linux amd64, AMD Ryzen 5 7600X (12 logical CPUs). These results describe this development host, not a deployment SLA.

## Automated checks

- `go test -race -cover ./... -count=1 -timeout=90s`: passed. Core gateway package statement coverage: 72.2%. Executable lifecycle is tested in a subprocess, whose statements are not included in the parent coverage report.
- `go vet ./...`: passed.
- Go build: passed, including the Huma admin handlers and direct HTTP inference proxy.
- SSE fuzzing: passed, 129,544 executions over approximately six seconds.
- Fixed-point money fuzzing: passed, 57,519 executions over approximately five seconds.
- Usage-cost fuzzing: passed, 61,604 executions over approximately five seconds.
- The generated admin OpenAPI contract passes structural checks; JSON/YAML specifications, admin documentation, and schema routes require admin authentication.

Coverage includes weekly/monthly boundaries, leap years and month-end anchors; shared-key budget enforcement and in-flight overshoot; duplicate settlement and manual corrections; late charges; key expiration/revocation; admin authorization and validation; streamed tools and usage; truncated streams and cancellation; upstream header/idle timeouts; response size bounds; SQLite query-only, page-exhaustion and writer-contention failures; abrupt subprocess exit and recovery; atomic schema initialization and reopening; consistent backup verification/restoration; persisted usage replay; provider configuration snapshots; and executable startup, provisioning, SIGTERM and persistence.

## Load results

Both runs seeded 3,000 users, sent 240 streaming requests at a paced 20 requests/second, and used a local TLS fake upstream with five-second streams. SQLite used WAL and `synchronous=FULL`. No real provider requests or paid tokens were used.

| Measurement                                   | System temporary storage (tmpfs) | Workspace storage (Btrfs) |
| --------------------------------------------- | -------------------------------: | ------------------------: |
| Peak concurrent upstream streams              |                              100 |                       100 |
| Gateway admission p95                         |                         0.420 ms |                 37.137 ms |
| Workload elapsed time, excluding provisioning |                         17.007 s |                  17.255 s |
| Heap before workload                          |                          0.9 MiB |                   0.9 MiB |
| Heap after workload, before forced GC         |                         21.5 MiB |                  22.7 MiB |
| Settled requests                              |                              240 |                       240 |
| Ledger entries                                |                              240 |                       240 |
| Aggregate database connection wait            |                              0 s |                   3.787 s |

Both met the provisional targets: p95 admission below 50 ms, approximately 100 active streams, no lost or duplicate settled charges, and heap growth below 256 MiB. The Btrfs run included about 44 seconds of user/key provisioning before the timed inference workload; the overall test took 61.35 seconds. Aggregate connection wait sums wait across concurrent requests, rather than representing one request's latency.

Reproduce the durable-storage run with:

```sh
mkdir -p data/load-tests
go test ./internal/gateway -run '^TestLoad$' -count=1 -v -timeout=5m \
  -args -gateway-load -gateway-load-dir="$PWD/data/load-tests"
```

Run this against the intended deployment volume; filesystem, storage latency, simultaneous backup work and host contention can change results substantially. This short workload does not establish sustained-load capacity, hardware power-loss durability, or provider throughput.
