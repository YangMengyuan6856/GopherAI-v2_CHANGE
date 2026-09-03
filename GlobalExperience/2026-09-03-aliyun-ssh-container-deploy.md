# 2026-09-03 Aliyun SSH Container Deployment Experience

## 1. Background

GopherAI runs on an Aliyun ECS host and the actual runtime is inside Docker
containers. Local Windows is mainly used for editing code and packaging
artifacts, not for running the full application stack.

Current deployment target:

- Host access: SSH alias `gopherai-aliyun`
- Main app container: `gopherai2`
- Dependency containers: `rabbitmq`, `redis-vector`
- App path inside container: `/root/GopherAI-`
- Existing manual flow: upload package to ECS host, `docker cp` into
  `gopherai2`, then build and restart backend, MCP, and frontend.

Sensitive values such as public IP, private key path content, AccessKey,
application secrets, and real `config.toml` values should not be written into
repo docs or scripts.

## 2. What Worked

SSH is the simplest and most maintainable path for this project.

The key points:

- Use a fixed host alias: `gopherai-aliyun`.
- Use explicit OpenSSH config when running from Codex or scripts:
  `ssh -F C:\Users\Lenovo\.ssh\config gopherai-aliyun ...`.
- Keep the private key only on the local machine.
- Only append the public key to `/root/.ssh/authorized_keys` on the ECS host.
- Do not use Workbench CLI unless SSH becomes impossible, because SSH requires
  fewer cloud permissions and is easier to script.

Useful connection verification command:

```powershell
ssh -F C:\Users\Lenovo\.ssh\config `
  -o BatchMode=yes `
  -o ConnectTimeout=10 `
  gopherai-aliyun `
  "hostname && whoami && docker ps --format 'table {{.Names}}\t{{.Status}}'"
```

## 3. SSH Key Lessons

When using Windows `cmd.exe`, use `%USERPROFILE%`, not PowerShell syntax
`$env:USERPROFILE`.

Correct key path:

```text
C:\Users\Lenovo\.ssh\gopherai_aliyun_ed25519
```

Common wrong paths:

```text
C:\Users\Lenovo.ssh\...
C:\Users\Lenovo\.ssh\gopherai\_aliyun\_ed25519
```

Reason:

- `cmd.exe` does not expand `$env:USERPROFILE`.
- `_` does not need escaping on Windows.
- `\_` means a directory separator plus `_`, not an escaped underscore.

## 4. What Went Wrong This Time

Two problems happened during the first deployment attempt.

First, the remote script used:

```bash
docker exec "$container" bash -s
```

This does not reliably pass stdin into the container from a non-interactive
script. The correct form is:

```bash
docker exec -i "$container" bash -s
```

Because of this, the first attempt uploaded the bundle but did not actually run
the full container-side deployment steps. The existing service was not replaced.

Second, the retry reached:

```text
[container] building backend in staged release
```

and then became stuck for a long time. During this period, SSH later timed out
at:

```text
Connection timed out during banner exchange
```

The most likely cause is resource pressure on the small ECS/container runtime
while compiling a large Go project. The server was still reachable at TCP 22,
but sshd could not complete the banner handshake in time.

Conclusion: container-side compilation is risky on this ECS instance. It can
consume enough CPU/memory to make remote control unreliable.

## 5. Safety Boundary

Do not delete Docker containers or images for this project unless explicitly
approved.

Must keep:

- Container `gopherai2`
- Container `rabbitmq`
- Container `redis-vector`
- Images used by those containers
- `/root/GopherAI-`
- `/root/GopherAI-/config/config.toml`
- `/root/GopherAI-/uploads`
- `/root/GopherAI-/vue-frontend/node_modules`

Generally safe to clean:

- Host old zip: `/root/GopherAI-.zip`
- Host old export: `/root/GopherAI_Export`
- Host deploy bundles: `/root/GopherAI_Deploy/bundles/GopherAI_*.tar.gz`
- Container failed staged releases: `/root/GopherAI-.__new_*`
- Container deploy bundles:
  `/root/GopherAI_Deploy/bundles/GopherAI_*.tar.gz`
- Residual build processes: `go build`, `/compile`, `/link`
- Log contents:
  `/root/GopherAI-/backend.log`,
  `/root/GopherAI-/common/mcp/mcp.log`,
  `/root/GopherAI-/vue-frontend/frontend.log`

Keep rollback directories by default:

```text
/root/GopherAI-.__previous_*
```

Only remove them after a successful deployment has been verified.

## 6. Better Deployment Strategy

Future deployments should avoid compiling the full Go project on ECS.

Preferred path:

1. Build Linux binaries locally or in a stronger build environment.
2. Package source plus binaries.
3. Upload package through SSH/SCP.
4. Copy package into `gopherai2`.
5. Preserve remote runtime state:
   `config/config.toml`, `uploads`, `vue-frontend/node_modules`.
6. Switch directories only after all required artifacts are present.
7. Restart services.
8. Verify process list, logs, and HTTP health checks.

Recommended artifact layout:

```text
release/
  GopherAI
  common/mcp/gopherai-mcp
  source files
```

Suggested local build direction:

```powershell
$env:GOOS = "linux"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"
go build -p 1 -o .\dist\linux-amd64\GopherAI .\main.go .\pprof_server.go

Push-Location .\common\mcp
go build -p 1 -o ..\..\dist\linux-amd64\gopherai-mcp
Pop-Location
```

If any dependency requires CGO, do not force `CGO_ENABLED=0`; instead build
inside a Linux build container or GitHub Actions runner, then upload the
resulting binaries to ECS.

## 7. Safer Runtime Start Order

When using existing binaries, the start order should be:

```bash
docker start rabbitmq redis-vector gopherai2
docker exec gopherai2 bash
service mysql start
cd /root/GopherAI-
: > backend.log
nohup ./GopherAI > backend.log 2>&1 &
cd /root/GopherAI-/common/mcp
: > mcp.log
nohup ./gopherai-mcp -mode server > mcp.log 2>&1 &
cd /root/GopherAI-/vue-frontend
: > frontend.log
nohup npm run serve > frontend.log 2>&1 &
```

Validation:

```bash
docker exec gopherai2 bash -lc '
pgrep -af "./GopherAI|gopherai-mcp|vue-cli-service|node" || true
tail -n 40 /root/GopherAI-/backend.log || true
tail -n 30 /root/GopherAI-/common/mcp/mcp.log || true
tail -n 30 /root/GopherAI-/vue-frontend/frontend.log || true
'
```

## 8. Script Design Rules

Deployment scripts should follow these rules:

- Always use `ssh -F C:\Users\Lenovo\.ssh\config`.
- Always use `docker exec -i ... bash -s` when piping scripts into a container.
- Do not compile on ECS by default.
- Do not overwrite remote `config/config.toml` unless an explicit flag is used.
- Do not package `.git`, `.claude`, `uploads`, `node_modules`, logs, or old
  binaries.
- Do not delete containers or images.
- Stage the new release under a temporary path first.
- Switch `/root/GopherAI-` only after preparation succeeds.
- Keep one or more `.__previous_*` rollback directories until the new release
  has been verified.
- Treat the frontend as ready only after `frontend.log` contains
  `Compiled successfully` and an HTTP request to `/ai-chat` returns 200. A
  listening port alone is not proof that webpack compiled the new source.

## 9. Next Action

Update `scripts/deploy/deploy-aliyun.ps1` so that the default path is:

```text
local build -> upload package -> container copy -> preserve runtime state ->
atomic directory switch -> start existing binaries -> health check
```

Use container-side `go build` only behind an explicit opt-in flag such as
`-BuildInContainer`.

## 10. Confirmed Runtime Facts After SSH Inspection

The following facts were verified on the ECS host on 2026-09-03:

- Host memory is approximately 1.6 GiB and swap is disabled.
- The root filesystem is 40 GiB. It was 64% used before failed-release cleanup
  and 61% used afterward.
- `gopherai2`, `rabbitmq`, and `redis-vector` all use Docker restart policy
  `no`; a host reboot leaves them stopped until explicitly started.
- `gopherai2` has no Docker mount. Its MySQL data and other state may therefore
  live in the container writable layer. Never delete or recreate this container
  without a separately verified backup and migration plan.
- RabbitMQ has a volume at `/var/lib/rabbitmq`; Redis Vector has a volume at
  `/data`.
- The application container has Go 1.22.6, Node 22.21.0, npm 10.9.4, and MySQL
  8.0.43.
- The current source requires Go 1.24/1.25, so the container Go toolchain is not
  a valid release builder even without the memory limit.
- With MySQL, backend, MCP, and the Vue development server running,
  `gopherai2` used about 806 MiB of its 1.575 GiB limit.
- Runtime ports are frontend 8080, MCP 8081, and backend 9090.

These facts turn the previous "likely resource pressure" diagnosis into a
confirmed deployment constraint: remote compilation is not the normal path.

## 11. First Successful Local-Binary Release

Release `20260903151408-0b72d5801a71-dirty` was deployed successfully using:

```text
Windows Go 1.25.0
-> linux/amd64, CGO_ENABLED=0
-> source + backend ELF + MCP ELF
-> SHA-256 verified on host and inside container
-> versioned directory switch
-> runtime directory move
-> PID-managed startup
-> port and public-proxy verification
```

Verified results:

- Root Go module tests passed.
- Nested MCP module tests passed.
- Backend and MCP binaries both had ELF magic `7F 45 4C 46`.
- Backend, MCP, and Vue processes stayed alive after deployment.
- Vue compiled successfully in approximately 10.9 seconds.
- Container-local frontend returned HTTP 200.
- Container-local backend root returned HTTP 404, proving the HTTP server was
  reachable even though no root route exists.
- The public frontend returned HTTP 200.
- A public request through `/api/user/login` reached the backend and returned a
  normal application JSON response.
- The removed image-recognition API returned HTTP 404.
- The active release kept `uploads`, remote `config/config.toml`, and frontend
  `node_modules`.
- The previous application directory remained available as a rollback release.

## 12. New Failure Lessons From the First Release

### 12.1 A GOPATH root can interfere with nested modules

The local machine originally used `F:\Golang` as GOPATH. Go printed:

```text
warning: ignoring go.mod in $GOPATH F:\Golang
```

and nested MCP module commands could fail to find their `go.mod`. The stable
pattern is:

- use a temporary GOPATH for each release process;
- reuse `F:\Golang\pkg\mod` only as `GOMODCACHE`;
- reuse a stable Go build cache;
- pass `go -C <module-directory>` explicitly for both modules.

The deployment script now discovers the existing module cache explicitly, so
an elevated PowerShell process does not redownload every dependency.

### 12.2 Tar exclude patterns can match by basename

The first local-binary bundle used:

```text
--exclude=GopherAI
```

GNU tar also matched `.deploy-bin/GopherAI`, producing a bundle that contained
only the MCP binary. The deployment stopped before switching the live directory
because the staged backend binary was missing.

Do not exclude release binaries by a broad basename. Before uploading, inspect
the archive and require both entries:

```text
.deploy-bin/GopherAI
.deploy-bin/gopherai-mcp
```

### 12.3 Streaming MCP HTTP is not a normal health response

An HTTP GET to `/mcp` can return headers and then remain open waiting for stream
data. A body-based `curl` health check may therefore time out even while MCP is
healthy. Use a TCP readiness check for port 8081 or add a dedicated finite
health endpoint later.

### 12.4 Verify through the real public path

Direct public access to backend port 9090 was not reliable from the local
network, while the actual user path worked correctly:

```text
public :8080 -> Vue dev server -> /api proxy -> backend :9090
```

Release acceptance should include a request through the public frontend proxy,
not only a direct backend-port check.

## 13. Cleanup Performed After the Successful Release

The following failed artifacts were permanently removed after their exact
paths were verified:

- two failed `.__new_*` staged release directories (about 285 MiB and 13 MiB);
- three failed release bundles and their failed sidecars;
- the obsolete host ZIP/export paths when present.

The active project, successful bundle/checksum/manifest, all Docker
containers/images, runtime data, and the successful release's
`.__previous_*` rollback directory were retained.

## 14. Health Gates Must Follow the Public Runtime Topology

Port-open checks only prove that a process accepted a socket. The release gate
now checks the finite backend endpoints in order:

```text
/health/live -> /health/ready
```

`live` must remain independent of downstream services so a temporary Redis or
RabbitMQ issue does not create a restart loop. `ready` checks MySQL, Redis cache,
Redis Vector capability, RabbitMQ, and required model configuration separately.
It returns only stable status/error codes, never DSNs, credentials, or raw
dependency errors.

The public deployment exposes only Vue port 8080, so `vue.config.js` must proxy
`/health` to backend port 9090 without the `/api/v1` rewrite used by application
routes. The deployment script still accepts a TCP fallback only while rolling
back to a historical release that returns 404 because it predates the health
contract; current releases must pass both HTTP gates.
