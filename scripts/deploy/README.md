# GopherAI Aliyun SSH Deployment

This directory contains the verified Windows-to-Aliyun deployment workflow for
the existing `gopherai2` container environment.

## Normal deployment

Run from the repository root:

```powershell
.\scripts\deploy\deploy-aliyun.ps1 `
  -HostAlias gopherai-aliyun `
  -SshConfigPath C:\Users\Lenovo\.ssh\config `
  -RunLocalTests
```

After the first verified test run for a change, a retry may omit
`-RunLocalTests`; the script still builds both Linux binaries locally.

## Release flow

The default path is:

1. Optionally run root-module and MCP-module tests with `-p 1`.
2. Build the backend, index worker, and MCP as `linux/amd64`,
   `CGO_ENABLED=0` binaries.
3. Create a release manifest and SHA-256 checksum.
4. Package source plus the three binaries, excluding `.git`, `.claude`, runtime
   uploads, remote configuration, logs, frontend `node_modules`, and `dist`.
5. Upload through the SSH alias and verify the checksum on the host and again
   inside `gopherai2`.
6. Extract into a new versioned directory before stopping the current release.
7. Preserve the remote `config/config.toml`; move `uploads` and frontend
   `node_modules` without duplicating their disk usage.
8. Switch `/root/GopherAI-`, start MySQL/backend/index worker/Prometheus/
   Grafana/MCP/frontend with PID files, and wait for application ports plus the
   loopback-only observability ports 9092 and 9093.
9. Keep the previous directory for rollback. If startup fails after switching,
   restore the previous directory and runtime folders automatically.

The script never deletes Docker containers or images.

Prometheus and Grafana are one-time runtime dependencies. Bootstrap them before
the first release that contains their versioned assets:

```powershell
.\scripts\deploy\bootstrap-prometheus-aliyun.ps1
.\scripts\deploy\bootstrap-grafana-aliyun.ps1
```

Grafana OSS is pinned to `13.2.1` with an exact package SHA-256. Its port is
available only on the private Docker network and is not published by the host;
it uses the loopback Prometheus datasource and provisions the immutable
`gopherai-closed-loop-v1` dashboard. Normal
deployment validates all dashboard queries before stopping the active release,
requires provisioning to succeed, rejects a transient startup RSS above 220 MiB,
and requires Grafana to settle below 200 MiB after a 20-second grace period.
Grafana drops privileges to its package user; only the copied, read-only
dashboard/provisioning assets and the dedicated `/var/lib/gopherai-grafana`
data directory are reachable, not the project configuration under `/root`.

## Options

- `-DeployConfig`: intentionally include the local `config/config.toml`. Do not
  use this during normal deployment; the remote runtime config is preserved.
- `-SkipFrontend -AllowFrontendDowntime`: deploy backend/MCP without starting
  Vue. The explicit acknowledgement is required because the atomic switch stops
  the previous frontend process; this does not preserve the old frontend.
  server.
- `-DryRun`: validate local packaging and print remote operations without
  uploading or switching a release.
- `-BuildInContainer`: explicit emergency fallback. This is unsafe on the
  current 1.6 GiB ECS and must not be used during normal deployment.

## Verification

The script treats open ports as its startup gate. A release is accepted only
after a second read-only check confirms:

```text
frontend http://127.0.0.1:8080/ -> 200
backend  http://127.0.0.1:9090/ -> 404 (server reachable; no root route)
MCP      127.0.0.1:8081         -> TCP ready
Prometheus 127.0.0.1:9092        -> ready, 2/2 scrape targets
Grafana  container-private :9093 -> healthy, dashboard provisioned, no host port
public   :8080/api/...           -> proxied backend JSON
```

`/mcp` is a streaming endpoint and can keep an HTTP request open, so use TCP
readiness rather than waiting for its response body.

See `GlobalExperience/2026-09-03-aliyun-ssh-container-deploy.md` for the
environment facts, failure analysis, cleanup boundaries, and first successful
release evidence.
