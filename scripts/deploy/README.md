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
2. Build the backend and MCP as `linux/amd64`, `CGO_ENABLED=0` binaries.
3. Create a release manifest and SHA-256 checksum.
4. Package source plus the two binaries, excluding `.git`, `.claude`, runtime
   uploads, remote configuration, logs, frontend `node_modules`, and `dist`.
5. Upload through the SSH alias and verify the checksum on the host and again
   inside `gopherai2`.
6. Extract into a new versioned directory before stopping the current release.
7. Preserve the remote `config/config.toml`; move `uploads` and frontend
   `node_modules` without duplicating their disk usage.
8. Switch `/root/GopherAI-`, start MySQL/backend/MCP/frontend with PID files,
   and wait for ports 9090, 8081, and 8080.
9. Keep the previous directory for rollback. If startup fails after switching,
   restore the previous directory and runtime folders automatically.

The script never deletes Docker containers or images.

## Options

- `-DeployConfig`: intentionally include the local `config/config.toml`. Do not
  use this during normal deployment; the remote runtime config is preserved.
- `-SkipFrontend`: deploy backend/MCP without starting the Vue development
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
public   :8080/api/...           -> proxied backend JSON
```

`/mcp` is a streaming endpoint and can keep an HTTP request open, so use TCP
readiness rather than waiting for its response body.

See `GlobalExperience/2026-09-03-aliyun-ssh-container-deploy.md` for the
environment facts, failure analysis, cleanup boundaries, and first successful
release evidence.
