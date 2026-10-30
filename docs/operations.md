# Operations and production acceptance

## Deployment

Build with `make build`, then run `bin/gateway -config /path/to/config.yaml`. The binary supports `-check` to validate configuration without touching the database, `-verify-backup FILE` to check backup integrity, and `-export-admin-openapi` to print the generated admin contract. The last two modes require neither credentials nor configuration.

The service supports one active process per database. An advisory lock prevents another gateway process opening the same database. Use local persistent storage with working file locking and fsync; do not share the WAL database across network filesystems or hosts. Preserve the database, WAL, and shared-memory files together when managing a stopped instance. Never copy only a live `.db` file as a backup.

The process creates its data directory with mode 0700 and its database/lock/backup files with mode 0600. If deploying into an existing directory or importing a database, provision equivalent permissions yourself. Provider keys remain in the process environment. Rotate the admin token through your secret manager and restart; virtual keys rotate by creating a replacement and revoking the old key.

Run behind a TLS reverse proxy. Disable response buffering and set its streaming timeout above the gateway request timeout. Set edge connection and unauthenticated request limits. Do not enable automatic proxy retries for inference POST requests. Keep the admin listener local or on an authenticated private network; it includes metrics and backup operations. Do not log authorization headers or inference bodies at the proxy.

Container example (Linux host networking preserves the example's loopback-only admin listener):

```sh
docker build -t llm-gateway .
docker run --name llm-gateway --network host --read-only --tmpfs /tmp \
  --mount type=bind,src="$PWD/config.yaml",dst=/etc/gateway/config.yaml,readonly \
  --mount type=volume,src=llm-gateway-data,dst=/data \
  --env GATEWAY_ADMIN_TOKEN --env OPENROUTER_API_KEY \
  llm-gateway
```

The container runs as a non-root user. Ensure bind-mounted configuration is readable by that user. Preferably use an external supervisor/container restart policy. A single instance has deployment and host-recovery downtime; it does not provide automatic failover.

SIGTERM stops admission and attempts to drain within `shutdown_seconds`, then closes remaining connections and allows up to 12 additional seconds for inference accounting finalization. Provider requests have an independent maximum duration and stream idle timer. Unfinished records are recovered after restart; stale records whose finalization failed are also recovered by the reconciliation worker. Inference is never dispatched unless its admission record was committed.

## Container releases

Push a version tag to run checks and publish the Linux amd64 image:

```sh
git tag v0.1.0
git push origin v0.1.0
```

Pull by version tag:

```sh
docker pull ghcr.io/strangebytesorg/llm-gateway:v0.1.0
```

Deploying with a SHA-256 digest is strongly recommended:

```sh
docker pull ghcr.io/strangebytesorg/llm-gateway@sha256:REPLACE_WITH_RELEASE_DIGEST
```

Keep the previous digest for rollback, and stop the current instance gracefully before replacing it. Roll back only to a version compatible with the database.

## Monitoring

`GET /health/live` on the inference port is public. `GET /health/ready` and `GET /metrics` on the admin port require the admin credential. Readiness checks database connectivity and admission state; it does not test upstream credentials or provider health.

Scrape Prometheus text metrics using bearer authentication. Monitor active requests, rejections, upstream errors, pending/unresolved accounting, accounting storage errors, and database connection wait time. Admission, first-token, and request durations expose sums/counts; use the load test for percentiles. Successful requests include `Server-Timing: gateway;dur=...` with admission overhead in milliseconds and `X-Request-ID` for investigation.

Alert on any unresolved accounting or accounting write failure, rising pending backlog, sustained rejections/errors, database wait time, low disk space, backup job errors, and missing recent off-host backups. Logs include request ID, model alias, provider, lifecycle and stable error codes; prompts and responses are not logged. No high-cardinality user labels are emitted.

Pending spending is not reserved. An attacker with a valid key can overspend through concurrency or repeatedly interrupted requests. The configured concurrency/rate/output caps reduce that exposure, but do not make the spending limit strict. Set upstream account spend caps separately.

## Backups and restore

The service produces a consistent `VACUUM INTO` snapshot in `backup_directory` every `backup_interval_seconds` (default 24 hours) and verifies SQLite integrity and foreign keys. It also supports an authenticated manual backup:

```sh
curl -sS -X POST http://127.0.0.1:42081/admin/v1/backups \
  -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN"
```

The result contains the filename under the configured directory. Backup paths are generated by the service; the API cannot write arbitrary caller-selected paths. Budget disk space for the database plus the backup and WAL. Snapshots contain user, hashed-key, and usage metadata and must be protected accordingly.

Backups on the same disk are local recovery points. **Before production, schedule encrypted transfer to separate storage, implement retention, and monitor transfer success.** This repository cannot provision your storage credentials or retention policy. There is no automatic deletion of request history, audit records, or local backups; monitor growth and archive under your organization's retention policy.

Restore procedure:

1. Stop the gateway and confirm its process exited.
2. Verify the selected backup with `gateway -verify-backup /path/to/backup.db`.
3. Preserve the current database and any WAL/SHM files in a separate incident directory. Restore the snapshot into a fresh data directory; do not combine it with old WAL/SHM files. Set ownership and permissions for the service user.
4. Configure the restored database path and start the gateway. Check readiness, user/key access, period totals and unresolved accounting.
5. Reconcile provider activity since the snapshot, including requests absent from the restored database. A backup cannot recover requests that were created after its snapshot. Keep an external incident ledger for those discrepancies.

Measure and record restore duration on the actual deployment host. With daily successful off-host snapshots, the storage recovery point can be up to 24 hours old; incomplete transfers extend that. Accounting for later activity requires provider records. Choose a shorter interval if that loss window is unacceptable.
