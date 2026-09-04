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
  `Compiled successfully` and an HTTP request to `/` returns 200. A
  listening port alone is not proof that webpack compiled the new source.

The M2-A rollout established that the Vue development server can return 404
for a direct `/ai-chat`
probe even though client-side routing works after the index page loads. The
release was correctly rolled back when ESLint found mixed tabs/spaces, but the
first rollback wait was prolonged because the deep-link probe could never
return 200. The reliable gate is therefore:

1. `frontend.log` contains `Compiled successfully`.
2. `http://127.0.0.1:8080/` returns HTTP 200.
3. Any `Failed to compile` or `ERROR in` line fails the release immediately.

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

## 15. M2-B1 Observability Release

Release `20260904091522-f07355c04e06` deployed request-level observability
successfully from commit `f07355c04e06b6d4557f728c5ac7deeaf022f059`.

Verified results:

- Root-module and nested MCP tests passed locally with Go 1.25.0.
- The backend and MCP were cross-compiled locally as Linux/amd64 binaries;
  the ECS host did not compile source.
- Bundle SHA-256 verification, atomic directory switch, live/ready checks,
  MCP TCP readiness, and Vue compile/HTTP checks passed.
- MySQL expand migration created the new `agent_runs` table and its trace,
  request, session, user-hash, intent, strategy, policy, status, and time
  indexes. No existing table or column was removed.
- Backend `/metrics` and the real public path `:8080/metrics` both returned
  Prometheus text exposition successfully.
- Exactly one active backend, MCP, and Vue process tree remained after the
  release. Use `ps` state and exact command matching instead of broad `pgrep`
  output when checking for duplicate processes.

### 15.1 Local Go commands must neutralize the broken global Go environment

The interactive Windows environment currently has both `GOROOT` and `GOPATH`
pointing at `F:\Golang`, but that directory is only suitable as a module cache
and does not contain a complete Go standard library. Direct `go test` commands
therefore fail with misleading messages such as `package context is not in
std`.

For manual test, tidy, or build commands, use the same isolation as the deploy
script:

```text
Go executable: C:\Program Files\Go\bin\go.exe
GOROOT:        C:\Program Files\Go
GOENV:         off
GOPATH:        a task-specific temporary directory
GOMODCACHE:    F:\Golang\pkg\mod
GOTOOLCHAIN:   local
```

Do not run `go mod tidy` before setting this environment. When changing a
dependency, also re-check that the module's `go` and `toolchain` directives
were not raised unintentionally. The Prometheus client was pinned to v1.23.2
so the repository remains compatible with its existing Go 1.24 module
directive while being built by the verified local Go 1.25 toolchain.

### 15.2 User acceptance evidence

The user completed a real streaming chat request after deployment and confirmed
that the page behavior was normal. Server-side verification showed:

```text
gopherai_requests_total{intent="legacy",status="success",strategy="legacy_chat"} 1
gopherai_request_duration_seconds_count{intent="legacy",strategy="legacy_chat"} 1
gopherai_agent_runs_total{agent="legacy_chat",status="success",strategy="legacy_chat"} 1
```

The corresponding structured log used trace
`be825b2e-26b0-4e8f-97fe-2a7178e31c3a`, reported
`persistence_status=stored`, and contained only the hashed user identifier.
This closes the M2-B1 user-acceptance gate.

## 16. M3-A1 Reliable Document Intake Release

Release `20260904093522-6d0084b3fb41` deployed the reliable document intake
slice from commit `6d0084b3fb41f43c1494e08ae405bac8d3026322`.

Verified results:

- Root-module and nested MCP tests passed locally before release.
- The backend and MCP Linux/amd64 binaries were built locally and installed
  from a bundle whose SHA-256 was verified on both the ECS host and inside the
  application container.
- The release switch preserved the existing `uploads` directory and frontend
  `node_modules`; live, ready, MCP TCP, Vue compile, and frontend HTTP gates all
  passed.
- MySQL expand migration created `knowledge_documents`,
  `knowledge_document_versions`, `knowledge_jobs`, and `outbox_events`.
- MySQL reported the intended unique indexes:
  `knowledge_documents(tenant_id, content_hash)` and
  `knowledge_document_versions(document_id, version)`.
- The backend and the public frontend proxy expose the document upload size
  histogram. The labeled upload counter appears after the first accepted,
  duplicate, rejected, or failed upload attempt.
- Exactly one backend, MCP host, and Vue process tree remained active.

This slice deliberately stops at reliable reception. A successful upload has
document status `uploaded` and job status `queued`; it does not mean retrieval
or document-grounded answers are available yet. The next slice must publish and
consume the Outbox work reliably, parse and chunk the stored content, then move
the document to `indexed` before chat routing may use it.

### 16.1 Do not assume passwordless MySQL root during release verification

The container's MySQL client correctly rejected a passwordless local root
query. Read-only deployment verification should use the application's existing
MySQL configuration inside the remote process and pass the password through a
short-lived environment variable such as `MYSQL_PWD`. Do not print the parsed
credential, include it in the command output, or copy it back to the local
workspace. This check is diagnostic only and must not rewrite the deployed
configuration.

### 16.2 User acceptance evidence

The user verified the four M3-A1 behaviors through the real public page:

- a new Markdown document was accepted and shown as waiting for indexing;
- a second distinct document increased the document count without deleting the
  first;
- uploading identical content reused the existing indexing task and did not
  increase the count;
- the document list and status remained visible after a browser refresh.

This closes the M3-A1 user-acceptance gate. It does not close the later parser,
worker, Redis indexing, retrieval, citation, or grounded-answer gates.

## 17. M3-A2 Reliable Index Worker Release

Release `20260904102319-9862406cd662` deployed the reliable asynchronous index
worker from feature commit `1c99674e626b40074153c7382e23961bb710620b`, the
polling-log fix `b1ac2744abda675b2ceb2553a80b0bc87531abaf`, and the
frontend status-meaning clarification `9862406cd662a3ac3e6b7fda52d7e759b8f5e9bb`.

Verified results:

- Root-module and nested MCP tests passed; backend, MCP, and the new index
  worker built successfully for Linux/amd64 on the local machine.
- Deployment now treats the index worker as a first-class artifact and PID,
  checks its finite `/health/live` and `/health/ready` endpoints on local port
  9091, and keeps historical releases without a worker rollback-compatible.
- The two document events created during M3-A1 were published from MySQL
  Outbox and consumed successfully after the worker first started.
- Aggregate authority state became: 2 indexed documents, 2 completed jobs, 2
  published Outbox events, and 2 indexed MySQL chunks.
- Redis contains `gopher:prod:v1:kb:chunks:idx` and two corresponding Chunk
  hashes. MySQL remains the authoritative Chunk store.
- RabbitMQ contains durable topic exchanges `gopher.jobs.v1` and
  `gopher.jobs.dlx.v1`, a durable manual-ack document queue, a durable 5-second
  retry queue, and a durable DLQ. All queues were empty after processing.
- Exactly one backend, index worker, MCP host, and Vue process were active.

The feature supports deterministic Markdown/TXT parsing, heading paths, fenced
code-block preservation, line ranges, bounded chunking, stable content hashes
and Chunk IDs, batched embeddings, tenant/user fields in the Redis schema,
publisher confirms, prefetch 1, finite retry, and dead-letter handling.

This release does not yet expose retrieval or document-grounded answering.
`indexed` means the document is ready for the next hybrid-retrieval slice, not
that the current Legacy chat strategy is already using it.

The frontend therefore labels this state “索引完成，等待检索接入” instead of
claiming that document question answering is already active.

### 17.1 Worker polling must not inherit development SQL logging

The first worker release was healthy, but its one-second Outbox poll inherited
Gin debug mode and GORM printed every empty query. This would grow the log
without adding diagnostic value. Set Gin to release mode before initializing
the worker's MySQL connection. After redeployment, the worker log remained one
62-byte structured `ready` line during idle polling.

### 17.2 Clear cross-compilation variables before native tests

After a Linux cross-build on Windows, running `go test` without clearing
`GOOS=linux`, `GOARCH=amd64`, and `CGO_ENABLED=0` creates a Linux test binary
that Windows reports as “not a valid Win32 application.” Restore the native
environment before tests. This failure is a command-environment issue, not a
source-code or test failure.

## 18. M3-A3 Tenant-safe Hybrid Retrieval Release

Release `20260904105609-6b310fe2bc67` contains the hybrid-retrieval feature
commit `007fb603003da6128946b4bc6aaed60777a6441c`, punctuated-identifier fix
`e69e0ed433d08b54726932fc93318db9e2414153`, and idle-polling/log fix
`6b310fe2bc6708360661d15c0ddcfd4888229ca0`.

Verified results:

- Root-module and nested MCP tests passed; backend, index worker, and MCP
  binaries built for Linux/amd64; all remote health gates and the real Vue
  compile passed.
- An authenticated public-browser test of both `GopherAI` and
  `Blue-Gopher-904` returned Dense 2, BM25 2, and fused 2. Both evidence cards
  carried `dense+bm25` provenance plus file, version, section, line range, and
  deterministic RRF score.
- Both retrieval paths enforce tenant and user TAG filters. Redis is only the
  retrieval index: every candidate is reloaded from MySQL and checked for
  indexed status and the same tenant/user ACL before it can be returned.
- Prometheus exposed a `hybrid/success` retrieval count plus latency and result
  histograms after the browser request.
- Search dependencies are initialized lazily. A temporarily unavailable
  embedding or Redis dependency therefore does not prevent the backend from
  registering routes or starting its health endpoints.

This release deliberately returns evidence previews only. It does not yet
ground chat answers, enforce an Evidence Gate, or create verified citations;
those belong to the next independently testable slice.

### 18.1 Preserve the exact identifier and search its components

The first public test showed that RediSearch tokenization did not match the
literal identifier `Blue-Gopher-904`, although dense retrieval still found the
correct chunks. Keyword query construction must preserve an escaped exact
form and also OR its alphanumeric components (`blue | gopher | 904`). A unit
test for `err-42` now protects this behavior.

### 18.2 Stop terminal-state polling and initialize release logging early

Document polling is useful only while at least one document is `uploaded` or
`parsing`. Stop the timer after every document reaches a terminal state and
restart it after a new upload. Also set Gin release mode before MySQL/GORM is
initialized; changing it later leaves verbose SQL logging enabled. The final
release produced no new three-second document-list requests after the initial
page load settled.

## 19. M3-A4 Evidence-gated KnowledgeAgent Release

Release `20260904110832-8e6e33213905` deployed feature commit
`8e6e332139051799e313d2c66ae7bc14de01344a`.

Verified results:

- Root-module and nested MCP tests passed. Linux backend/index-worker/MCP
  builds, health gates, and the real Vue compile all passed.
- The Evidence Gate requires cross-retriever support and a normalized top
  score before invoking the chat model. Empty, dense-only, keyword-only, and
  low-score cases return a controlled insufficient-evidence result.
- KnowledgeAgent receives only the bounded evidence pack. Evidence text is
  explicitly treated as untrusted input, and conflicting chunks must be
  surfaced rather than silently selecting one value.
- Model output is accepted only as JSON containing inline `[E<n>]` markers and
  a matching citation list. The verifier rejects unknown, missing,
  undeclared, malformed, or cross-tenant evidence references before returning
  an answer.
- A public signed-in query for the `Blue-Gopher-904` default retry count
  correctly reported the conflict between 7 and 9. The page exposed two
  expandable citations with document, version, section, lines, and the exact
  authoritative Chunk content.
- A public query about a Kubernetes node-taint policy produced
  `no_cross_retriever_support`, stated that no model was called, and requested
  more precise project evidence.
- Prometheus recorded one `answered/sufficient` and one
  `insufficient/no_cross_retriever_support` KnowledgeAgent attempt.

The standalone `/api/v1/knowledge/answer` endpoint is intentional for this
slice: it makes the evidence contract independently testable without routing
all ordinary chat through RAG before M4 intent recognition exists. The next
slice may register `rag_fast` with the shared AppService behind an explicit
feature/request gate; automatic `project_qa` selection remains an M4 concern.

## 20. Never Compile Or Run The Full Test Suite In The Runtime Container

On 2026-09-04, an attempted `go test -p 1 ./...` inside `gopherai2` downloaded
missing test dependencies and compiled the whole repository while the backend,
index worker, MCP host, Vue development server, MySQL, Redis, and RabbitMQ were
already running. Even with `-p 1`, the small ECS instance entered severe memory
and CPU pressure: TCP ports still accepted connections, but SSH timed out during
banner exchange and HTTP returned no response.

Operational rule:

- Run root-module tests and nested MCP tests on Windows with the installed Go
  toolchain.
- Cross-build Linux/amd64 binaries locally with `CGO_ENABLED=0` and `-p 1`.
- Upload only prebuilt binaries plus source/static assets.
- On the ECS host/container, perform only checksum verification, atomic release
  switching, finite health checks, process checks, and lightweight isolated
  runtime evaluations.
- Never use `go test ./...`, `go build ./...`, `npm install`, or other
  dependency-heavy compilation as a post-deploy check on this runtime host.

If Windows has a stale machine-wide `GOROOT` pointing at `F:\Golang`, invoking
`go.exe` by absolute path is not enough: explicitly set
`GOROOT=C:\Program Files\Go` for the test/build process. Preserve `GOPATH` only
as the module cache. The deployment script already uses the installed toolchain
for cross-builds; ad-hoc verification commands must follow the same rule.

If this mistake causes SSH banner timeouts, stop launching additional remote
commands because each connection competes for the same exhausted resources.
Terminate stale local `ssh.exe` clients, wait for the remote compile to exit or
be OOM-killed, and use the ECS console to restart the instance only if it does
not recover. After recovery, verify containers and health first, then redeploy a
clean prebuilt release. Do not delete or recreate `gopherai2`: the application,
configuration, upload data, MySQL service, and frontend dependencies live in
that container.

## 21. M3-A5 Unified `rag_fast` Chat Integration

M3-A5 registers KnowledgeAgent as the real `rag_fast` AppService strategy behind
an explicit `knowledge_required` request flag and environment feature flag.
This preserves ordinary Legacy chat while automatic `project_qa` recognition
remains an M4 task.

Implementation commits:

- `18eda3902a56619ca080288ff72ff4f627189f98`: AppService strategy, policy,
  explicit intent, SSE citation events, frontend control, and session storage.
- `6a830d09262d2755f84c198b53af1d20d7dc0c05`: preserve verified citation
  locations in the legacy text-only message history.
- `061767aee784bcaca7d4d32f896586982ec34c40`: report `KnowledgeAgent` and
  `rag_fast` as separate observability dimensions.
- `6ecc3ceac9830999a14cd7bb56a94cba22e93c59`: retry citation-format repair once,
  then return a cited safe fallback instead of exposing unverified model text.

The first three commits were deployed as release
`20260904112941-061767aee784`; live/ready, index-worker health, MCP TCP, Vue
compile, and frontend HTTP checks passed. Signed-in browser checks confirmed:

- checked `知识库回答` routes to `rag_fast · policy-rag-fast-v1` and returns the
  two conflicting retry values with expandable citations;
- reopening the stored conversation preserves human-readable document,
  version, section, and line-range references;
- leaving the control unchecked keeps ordinary messages on
  `legacy_chat · policy-v0`.

After the ECS instance was restarted, the three existing containers were found
intact and stopped (`Exited 255`). Do not recreate them. Running the normal
deployment script started `rabbitmq`, `redis-vector`, and `gopherai2`, preserved
the container-resident configuration/uploads/frontend dependencies, and
activated clean release `20260904155804-ec798aee6281`. A later final release,
`20260904161139-4b1d4d0e3307`, contains the same citation repair plus the RAG
evaluation fixes and checked-in report. Backend/worker ready checks, MCP TCP,
Vue compile/HTTP, bundle checksum, and the four expected application processes
all passed.

The user then completed the final five public-page acceptance checks against
release `20260904161139-4b1d4d0e3307`. Legacy routing isolation, the conflicting
7-versus-9 grounded answer with two citations, citation persistence after
refresh, streaming RAG completion with final citations, and repeated-query
citation safety fallback all behaved as expected. This closes the M3-A5 user
acceptance gate.

### 21.1 Restart recovery sequence

After an ECS reboot, first run read-only checks for uptime, memory, and
`docker ps -a`. It is normal for the project containers to remain in an Exited
state. If all three named containers still exist, run the deployment script
instead of manually starting services or deleting anything. The script starts
the dependencies, starts MySQL inside `gopherai2`, preserves runtime data,
switches the verified release atomically, and applies finite health gates.

The successful recovery confirmed the original incident was resource
exhaustion from compiling/testing inside the runtime container, not container
corruption. The safe response is one ECS restart followed by a locally built
clean release; additional concurrent SSH sessions or remote builds would only
extend the outage.

## 22. Isolated M3 RAG Evaluation

The first real 20-case cloud evaluation ran only a locally cross-built binary
inside `gopherai2` with `GOMAXPROCS=1` and `nice -n 10`. It used the dedicated
Redis index `gopher:eval-rag-core-v1:v1:kb:chunks:idx`, which was dropped before
and after each run. A post-run `FT._LIST` showed only production and historical
indexes; no evaluation index remained.

The first attempt exposed an Ark/DashScope constraint: one embedding request
may contain at most 10 texts. The shared indexing default had been 16, so an
11-chunk fixture failed before producing a report. Commit `c2676e8e` lowers the
production boundary to 10 and adds an 11-chunk `10 + 1` batching regression
test. This protects real large-document indexing as well as evaluation.

The complete run for candidate `d6add7fe` passed the technical gate:

- Recall@5: `1.0000`
- nDCG@5: `0.9815`
- MRR: `0.9750`
- Citation Precision: `0.9545`
- Citation Coverage: `0.9500`
- Unauthorized Recall: `0`
- Resolved Answer Rate: `0.9500`
- Error Rate: `0`

Unresolved safety responses may list the authorized evidence inspected while
making no factual claim. Keep those IDs in each case trace, but score Citation
Precision only on resolved factual answers; coverage and resolved-answer rate
continue to penalize the refusal. This avoids double-penalizing safety without
hiding it.

The report is reproducible at the dataset/fixture/retriever/config level, but
the external model is mutable. The 20 labels are still `pending_user`, so the
technical PASS is not yet eligible to freeze as an interview or regression
baseline (`human_reviewed=false`, `baseline_eligible=false`).

## 23. M3-B1 Structured Document Indexing

Release `20260904164832-ac761f4ef97e` adds real parser-aware indexing for
JSON, YAML/YML, and Go source while preserving the Markdown/TXT path. All Go
and MCP tests ran locally; the server received prebuilt Linux artifacts only.
Bundle verification, atomic switch, backend/worker readiness, MCP TCP, and Vue
compile/HTTP passed.

The implementation preserves data a later answer must cite:

- JSON/YAML scalar values become deterministic key-path sections such as
  `service > retry > max_attempts`, with source line ranges.
- Go source uses the standard Go AST and emits package, type, var/const,
  function, and method sections; oversized declarations split on source lines
  while keeping the owning symbol and bounded token size.
- Invalid JSON/YAML/Go syntax is a non-retryable parse failure and cannot write
  partial MySQL chunks or Redis vectors.
- Structured-data and Go parser families domain-separate their document
  fingerprints, so identical bytes interpreted as TXT and YAML do not reuse an
  incompatible index. The legacy Markdown/TXT hash stays unchanged, avoiding a
  one-time duplicate of existing uploads.

Three versioned acceptance files live in
`evals/fixtures/_manual_uploads`. Use them through the signed-in public page to
verify upload, asynchronous indexing, key/symbol retrieval, line-level
citations, and grounded answering. Do not replace this user test with remote
database injection; the real browser upload path is part of the feature.

## 24. M3-B2 Document Version Alias and Frontend Gate Rollback

Release `20260904173743-fe795b9e8759` adds immutable candidate uploads for an
existing logical document. MySQL `knowledge_documents.current_version` is the
authoritative read alias: the worker writes and verifies the candidate chunks
first, then moves this alias inside the same transaction that marks the job
complete. Retrieval joins chunks to the active document version, so a failed
candidate cannot replace or leak alongside the last successful version.
Completion is monotonic; a delayed older job cannot roll the alias back from a
higher version.

The first deployment candidate, `20260904173435-a779ec2ec4cc`, passed Backend
and Worker health but failed the Vue ESLint gate because the newly added block
mixed tabs and spaces. The deploy script stopped the candidate and restored the
previous release, including successful Backend/Worker/frontend checks. Do not
disable lint to get around this gate. Normalize source indentation locally,
commit the formatting fix, and rerun the same immutable deployment. The second
release passed Vue compilation and HTTP readiness along with all service gates.

This incident confirms two reusable rules:

- UI lint/compile is part of release readiness, not a cosmetic follow-up.
- A failed late gate must exercise the existing full rollback path; do not
  manually patch the half-deployed directory or restart individual processes.

Acceptance fixtures `m3b-version-alias-v1.md`, `m3b-version-alias-v2.md`, and
`m3b-version-alias-invalid.json` are checked in. The signed-in page test should
prove v1 remains visible while v2 is queued, v2 becomes the only authoritative
version after success, and an invalid later candidate reports failure while the
last successful version remains queryable.

## 25. M3-B3 Safe Rebuild and Eventual Delete

Release `20260904175427-c5236e91efb1` (commit `c5236e91efb1`) passed local root
and nested MCP tests, locally cross-built Linux artifacts, bundle verification,
the atomic release switch, Backend and Worker live/ready probes, MCP TCP, and
Vue compile/HTTP. The deployed bundle SHA-256 is
`ea0bb72530e3dc34cb040cc44ed801f3c0e938174e4649a9871e2bb7002a48d5`.

Rebuild and delete have deliberately different consistency contracts:

- Rebuild creates a new immutable version from the current stored artifact.
  The existing version remains queryable until the candidate is fully parsed,
  chunked, embedded, indexed, and atomically activated.
- Delete marks the MySQL authority row `deleted` in the same transaction that
  creates the delete Job and Outbox event. Therefore new queries stop seeing
  the document immediately, even before asynchronous Redis cleanup finishes.
- The Worker deletes only the exact Redis keys derived from MySQL Chunk IDs,
  batching 100 keys at a time. A cleanup retry is idempotent; exhausted retries
  preserve a stable failure code for operations rather than silently reviving
  query authority.
- Delete is rejected while an index job is queued, processing, or retrying.
  This avoids a race in which a late index completion could restore a document
  that the user thought was deleted.

The deletion is intentionally recoverable: MySQL document/version/chunk rows
and source storage remain as logically deleted audit data, while Redis serving
keys are removed. Do not describe this endpoint as irreversible data erasure.
If irreversible purge is ever required, add a separately authorized retention
workflow with storage and database backup evidence rather than extending this
request path.

## 26. M3-B4 Explainable Query/Gap Detector

Release `20260904180325-6d331cf32287` (commit `6d331cf32287`) passed the root
and nested MCP test suites, three local Linux builds, checksum verification,
atomic switch, Backend/Worker/MCP health gates, and Vue compile/HTTP. The
deployed bundle SHA-256 is
`c9ee88dc83d65972c2ca01d1c67232bedf40f3f7255c1ec0ab662237c53c3638`.

The `query-gap-v1` assessment is deliberately deterministic and recommend-only.
It inspects bounded query-shape signals (clauses, comparison, cross-document,
causal, analytical, ambiguity, and length) plus authorized retrieval outcomes
(top score, source count, hybrid support, and rank margin). It returns stable
reason codes and explicit rewrite/rerank/deep recommendations. At this release
it does not execute `rag_deep` or alter production routing; this keeps M3-24
measurable and makes M3-25/M3-26 activation separately testable and reversible.

Keep the frontend label “当前仅分析” until an actual bounded rewrite/rerank
implementation is deployed. Showing a recommendation as though it had already
run would make both user acceptance and later strategy evaluation invalid.

## 27. M3-B5 Conditional Query Rewrite Boundary

Release `20260904181007-40838108bb5b` (commit `40838108bb5b`) passed root/MCP
tests, local Linux builds, checksum and atomic switch, Backend/Worker/MCP health,
and Vue compile/HTTP. Bundle SHA-256:
`551e977ffb742ebacb26a29fb21aaf7c1e720d1ef3b2723fce72fc5f9ee4ece2`.

The Rewriter is fail-open at the retrieval boundary. It makes no model call for
the fast path, makes at most one four-second call when recommended, accepts at
most two bounded unique variants, and always keeps the exact original query as
the first retrieval query. Model errors, timeouts, invalid JSON, and unusable
variants return stable fallback outcomes instead of failing or replacing the
baseline query. The production release contains the tested component but does
not activate it in user traffic until the M3-27 deep strategy is complete.

## 28. M3-B6 Conditional Rerank Boundary

Release `20260904181709-890f6fb09ab8` (commit `890f6fb09ab8`) passed root/MCP
tests, local Linux builds, bundle verification, atomic switch, all service
health gates, and Vue compile/HTTP. Bundle SHA-256:
`ed6e7dff482405d5d8f431d1103f5d164adebfbc869c9d639228f859a9a335ab`.

Rerank is also fail-open. It is skipped unless recommended and given at least
two candidates; the one model call receives at most ten candidates with each
content field capped at 1200 runes. A valid response must be an exact
permutation of every authorized Evidence ID. Unknown, duplicate, or omitted
IDs, malformed JSON, model failure, and timeout all preserve the complete RRF
ordering. Do not silently accept a partial ranking: appending omitted evidence
would conceal an invalid model contract and weaken evaluation evidence.

## 29. M3-B7 Bounded and Observable RAG Deep Strategy

Release `20260904182953-79a80dc9c5b2` (commit `79a80dc9c5b2`) passed root/MCP
tests, local Linux builds, bundle verification, atomic switch, Backend/Worker/MCP
health gates, and Vue compile/HTTP. Bundle SHA-256:
`ee33f69ee85e59d9de7c13689312ac8e999655bbeddf7027435e5bb4dca55c9c`.

The production Deep path is a separate, reversible API and UI action. It always
starts with the ACL-authorized Hybrid baseline and applies fixed budgets of at
most three retrieval queries, one rewrite call, and one rerank call. A simple,
high-confidence request smart-skips every enhancement call. Complex or gapped
requests may add two rewritten searches, merge and deduplicate their evidence,
then rerank only the bounded authorized candidate set.

Every enhancement is fail-open: rewrite, additional retrieval, or rerank failure
keeps the baseline evidence and reports `partial_fallback`; it never converts an
enhancement failure into an empty answer. The endpoint reports independent
`rag_deep/rag-deep-v1` identity, query/candidate diagnostics, enhancement Token
usage, and stable outcomes. Prometheus exports bounded strategy request,
duration, and enhancement counters. This makes later Fast-versus-Deep evaluation
possible without using raw query text or dynamic error strings as metric labels.

## 30. M3-28/29/30 60-Case RAG Gate and Explainability Release

Release `20260904190254-560920e96696` (report commit
`560920e9669602c945e080711b494dfa7ba00237`) passed the complete deployment
pipeline: local root/MCP tests, three Linux/amd64 builds, bundle verification,
atomic switch, Backend/Index Worker/MCP health, Vue compile, HTTP, and unique
process checks. Bundle SHA-256:
`afe36b3393a036e793ae3a9a627a3e843f17ff811af4f767fdce3cde867fc59f`.

The v2 evaluation deliberately separates 50 answerable cases from 10
no-evidence cases. Retrieval and citation recall denominators include only cases
that should resolve; negative cases are measured by Evidence Gate Precision,
No-evidence Safe Rate, Unsupported Answer Rate, ACL leakage, and model-call
behavior. Mixing the two populations would reward a system for retrieving
irrelevant evidence on questions it should refuse.

The first 60-case run was useful because it failed: it exposed citation-format
fragility and Chinese queries that had semantic retrieval candidates but lacked
independent lexical support. The fixes remain bounded:

- Accept common model citation spellings such as `【E1】` and `[1]`, normalize
  them to verified Evidence IDs, and still reject every unknown or unauthorized
  ID through Citation Builder.
- Emit stable answer diagnostics (`status`, `reason`, `model_attempts`) so a
  deterministic safety fallback is distinguishable from an ordinary model
  answer.
- Permit Dense evidence to obtain independent Chinese lexical support only when
  at least four distinct CJK bigrams match and query coverage is at least 20%.
  Do not lower the existing Hybrid score threshold. Keep no-evidence, injection,
  credential, and cross-tenant negative tests alongside this rule.

The final isolated run measured Recall@5 `1.0000`, nDCG@5 `0.9599`, MRR
`0.9500`, Citation Precision `1.0000`, Citation Coverage `0.9800`, Unauthorized
Recall `0`, Resolved Answer Rate `1.0000`, Evidence Gate Precision `1.0000`,
No-evidence Safe Rate `1.0000`, Unsupported Answer Rate `0`, and Error Rate `0`.
Search/answer/end-to-end P95 were `149ms/1277ms/1380ms`, below the 8-second G3
gate. Dataset SHA-256 is
`c4882a694401f52b4acb002614c0e7a4f4445740cf38b2fb0c50953798376104`;
fixture SHA-256 is
`14c27846dcaf3f8f962f51de60936302930bd36a81c2a8c0918054889189345e`.

Use the dedicated Redis index `gopher:eval-rag-core-v2:v1:kb:chunks:idx` and
delete only that exact key before and after a run. Never point the evaluator at
the production knowledge index. The labels are still `pending_user`, so the
report must remain `human_reviewed=false` and `baseline_eligible=false` until a
human reviews all cases. Passing technical gates does not authorize a resume
claim that the dataset is a frozen baseline.

The same release adds user-visible lifecycle and answer diagnostics: each
document can expose status, current version, content type, and stable failure
code; citations expand to exact evidence and line range; Deep results expose
extra queries, candidate counts, Token use, Rewrite/Rerank latency, and fallback
reasons. These UI behaviors still require real signed-in acceptance even though
the cloud compile and runtime gates passed.

## 31. M4 Intent Shadow and Real Cascade Evaluation

Release `20260904194839-78b0a2d4a335` (commit `78b0a2d4a335`) introduced the
complete intent cascade as a production Shadow path. The existing fixed and
explicit routes remain authoritative. Normal JSON and SSE responses expose the
shadow suggestion separately, and the page labels the two lines as `actual
route` and `shadow decision (no traffic switch)`. MySQL stores only bounded
intent diagnostics in `agent_runs`; raw questions and answers remain excluded.
Prometheus exports bounded decision, stage-call, duration, and disagreement
metrics. The feature can be disabled with
`GOPHERAI_FEATURE_INTENT_SHADOW_ENABLED=false`.

The first real 150-case evaluation found a contract failure rather than a model
classification failure: optional `entities` metadata sometimes contained
arrays or numbers, so strict `map[string]string` decoding discarded otherwise
valid intent decisions. The corrected boundary keeps every routing field and
unknown top-level field strict, but drops or trims invalid optional entity
values and records `llm_entities_sanitized`. Low-confidence model results remain
candidate intents with `needs_clarify=true`; they are not permitted to activate
an Agent. This obeys the `<0.60 => clarify or general` safety requirement while
retaining calibration evidence.

Candidate commit `2dbaf8184c77589e67fd0a02ea3769b2b8189b70` measured Accuracy
`0.9533`, Macro-F1 `0.9530`, minimum class Recall `0.8800`, severe misroute
rate `0.0067`, Prototype and LLM call rates `0.5533`, calibration error
`0.1925`, and cascade P95 `878ms`. The technical G4 thresholds pass. The
dataset SHA-256 is
`6d3d8061feb3bacd7615c55d82c68345f2abd8a2ee72a8d111b5ed89024ae1c2`.
Because every label is still `pending_user`, the report intentionally remains
`human_reviewed=false` and `baseline_eligible=false`; do not quote this as a
human-reviewed resume baseline yet. The initial post-fix runtime release was
`20260904201438-2dbaf8184c77-dirty`; a clean documentation release must follow
after committing the generated report.

The clean report release is `20260904202008-72d678cd8689` (commit
`72d678cd8689`). Bundle SHA-256:
`26179a545ead9f8242de0276d13742a5f33b6b53c4990cbc205c91c695c6147a`.
It passed the same Backend/Worker/MCP/Vue health gates and supersedes the dirty
release as the reproducible M4 checkpoint.

## 32. M5-01/02/03 Diagnostic Contract, Dataset, and Safe Input Boundary

Release `20260904202409-9dfad3accb78` (commit
`9dfad3accb7804f5e0e760f9c531fc3e3b646a03`) added
`diagnostic-schema-v1`. The schema caps hypotheses at three, requires evidence
and one to five read-only verification steps per hypothesis, enforces descending
confidence, and prevents `confirmed` or `insufficient` conclusions from being
formed without their required evidence or clarification state. Bundle SHA-256:
`0ca3b08b8dcaf5cc4fa2db81913de44c0fa133b66a19eb1b53eb92b610b8a0f1`.

Release `20260904203636-37367925f466` (commit
`37367925f466a9422d2978b7c4b91ef834fdff80`) added the 40-case
`devsupport-diagnostic-v1` candidate and strict JSONL loader. The set is exactly
balanced across eight project-specific categories, has explicit acceptable root
causes, necessary steps, read-only verification, forbidden claims/actions, and
at least eight clarification cases. Unknown fields, duplicate IDs, category
imbalance, invalid bounds, and credential-like material fail validation. Dataset
SHA-256 is
`521d9765c600015f85a2ca4981c6ba7e77ec57da65872b1a6039ce70c1c9adf8`;
bundle SHA-256 is
`fc9d8f4b3270b1c5d41bfc28fb7d69001250ea2483081e863daa38c6ffb8d285`.
Labels remain `pending_user`, so this is not a human-reviewed baseline.

Release `20260904204301-a39f81357885` (commit
`a39f813578856caad5b28344e61278fb56f9be7f`) added the deterministic
`diagnostic-extractor-v1` input boundary. It caps input, per-line, excerpt, and
symptom sizes; redacts credential assignments, Bearer/JWT material, URI
credentials, private-key blocks, and email addresses; removes instruction-like
lines before extracting stable component/error signals; and emits bounded
environment facts as unconfirmed user observations. Bundle SHA-256:
`a39d28d1e0dcef8b1ae3220d2781275c04667615bd91f9b3eb0d0559154a2117`.

All three releases used local Linux/amd64 builds and passed atomic bundle
verification, Backend and Index Worker live/ready gates, MCP startup, Vue
compile/HTTP, and unique-process checks. These M5 pieces are deliberately not
wired into the live chat route yet. Do not activate `troubleshooting` until the
DiagnosticAgent, evidence gate, fallback, and route tests in M5-04/05 are
complete.

### PowerShell-to-Bash probe quoting lesson

For ad-hoc read-only verification from Windows PowerShell, do not embed a Bash
pipeline or regular-expression alternation directly in one SSH argument. On
2026-09-04 a final `pgrep` probe containing `|` was split before the remote Bash
could parse it; the command failed without changing remote state. Prefer the
versioned deploy script's base64 transport for multi-line remote scripts, or run
independent single-purpose SSH commands and check every exit code.

When probing a Vue history-mode route such as `/ai-chat`, send
`Accept: text/html`. A generic HTTP client's default `*/*` may receive
`Cannot GET /ai-chat` from the development server even though a browser
navigation and an HTML deep-link request both work. The deployment gate should
continue to check `/`; deep-link acceptance should use browser-equivalent
headers.

The final evening release is `20260904205101-22cf5c50bf94` (commit
`22cf5c50bf9474699e5e5865d427e6664656ae16`), bundle SHA-256
`d46a8f5507419e3ca7556e9e8c50d8dfcb7d6a74a55e578ccb2b2c9159f6651e`.
The release also corrects the legacy `LoginRequest.Password` tag from malformed
`json:password` syntax to `json:"password"` and adds a regression test. Root
`go test ./...`, root `go vet ./...`, the independent MCP module tests, and all
cloud runtime/Vue gates pass.

## 33. Structured Sibling Evidence and Safe Rebuild Migration

Release `20260904221332-23dc387303ab` (commit
`23dc387303ab4b6e8a089c57d2c2cd85d8db7291`) fixes a real acceptance failure
where a cross-document configuration question retrieved
`service.retry.dead_letter_exchange` but omitted its sibling
`service.retry.max_attempts`. MySQL inspection confirmed that parsing and
indexing had preserved both values; the loss occurred because every structured
leaf previously used its full key path as a separate section, which forced tiny
sibling fields into separate chunks before Top-K and reranking.

The `key-path-sibling-context-v2` chunker now groups scalar siblings under their
containing object or sequence item while keeping each complete leaf key path in
the chunk content. This preserves precise citations and lets retrieval of one
configuration field carry the adjacent fields required by multi-part
questions. The repair is versioned rather than silently changing the meaning
of `key-path-token-v1`.

Existing JSON/YAML documents are migrated through the normal **安全重建** path.
Rebuild reads and hashes the active immutable artifact, assigns the current
parser/chunker metadata to a new candidate version, and leaves the old active
alias queryable until indexing completes. It does not mutate active chunks in
place. After this release, rebuild both `m3b-config.json` and
`m3b-service.yaml` before repeating the C2 cross-document acceptance query.

The same acceptance review found a test-design error: asking for a deployment
manual's default backend port without uploading that manual correctly triggers
the Evidence Gate. D5 now uses the already indexed `m3b-config.json` fixture to
verify explicit RAG routing separately from Shadow intent. A missing source must
continue to produce an evidence-insufficient refusal; the safety gate was not
weakened to satisfy an invalid fixture.

Bundle SHA-256:
`3f9676995dcb736a8d5fb077ce2b44fa058b58a9ac33102dadffd38784ffca6e`.
Root `go test ./...`, root `go vet ./...`, the independent MCP tests, three
local Linux/amd64 builds, atomic bundle verification, Backend/Worker readiness,
MCP startup, Vue compile/HTTP, and unique-process checks all passed.

## 34. R1 Durable Diagnostic Harness 首次云端纵向验证

Release `20260905004340-a88d67d003d9-dirty`（commit
`a88d67d003d9cc5bafa1d47d648f2fb5253d9ddf`）首次上线了由 MySQL
持久化的诊断 Run/Step/Checkpoint、CAS 状态版本、请求与恢复幂等键、执行预算、取消、
`WAITING_USER` 恢复，以及只公开可审计摘要的 DiagnosticAgent 页面。Bundle SHA-256：
`5bda249854e4f60aa14314397af2aedbb468ccc4e2a4d055377108eddc922192`。

发布门通过：根模块和独立 MCP 全测通过；Backend、Index Worker live/ready 通过；
MCP 启动正常；Vue 在容器内编译成功且 HTTP 8080 可用；四个进程均只有一个活动实例。
MySQL 确认生成 `agent_lifecycle_runs`、`agent_lifecycle_steps`、
`agent_checkpoints` 三张表。三次真实浏览器运行最终形成 2 个 `SUCCEEDED` 和 1 个
`CANCELLED` Run。

浏览器纵向检查覆盖：

1. 输入 Docker、Redis connection refused、HTTP 502 和一个测试密码字段后，Run 从
   v1 单调推进到 v5，返回两个有 Evidence 的 hypothesis，每个只包含只读验证步骤；
   公开 Step 报告识别 3 个组件、2 个错误特征并完成 1 处脱敏。
2. 模糊输入停在 `WAITING_USER` v5，明确提出组件、错误特征和环境三个问题；补充
   Docker + Redis 7.2 + NOAUTH 后，同一个 Run 从 v6 恢复并在 v9 形成 95% 的待验证
   认证假设。它没有把用户观察直接升级成 `confirmed` 根因。
3. 另一条等待中的 Run 由页面取消到 `CANCELLED` v6，新增唯一
   `USER_CANCELLED` 公开步骤。
4. 只读数据库检查确认 checkpoint 中不存在输入的测试密码明文，匹配计数为 0。

### 本机 Go 配置与发布包噪声经验

本机的用户级 Go env 曾同时把 `GOROOT`、`GOPATH` 写成 `F:\Golang`，直接执行
`go test` 会把模块缓存目录误当成标准库根目录并报告 `package context is not in std`。
这不是项目编译失败。临时验证应显式设置 `GOROOT=C:\Program Files\Go`，将
`GOPATH` 保持为缓存位置；正式发布继续使用脚本中的 `GOENV=off`、独立临时 GOPATH
和本地工具链路径，不能为了通过一次构建去修改用户的全局 Go 配置。

该 release 的 `dirty` 来自根目录中未跟踪的验收截图。保留截图文件不删除，并把这批
确切文件名加入 `.gitignore`，避免后续 release 被无关截图标记为 dirty 或重复上传。
干净发布的包体仍约 90 MB，说明主要体积来自三个 Linux 二进制和项目资源，不能把
截图忽略误写成包体优化结果。功能提交本身已在发布前推送，Git SHA 可追溯。

清理证据文档和忽略规则后，干净 Release 为
`20260905005217-daf4c05bc04e`（commit `daf4c05bc04e6ec81bfead6b65a3ef664d88da58`），
bundle SHA-256 为
`d667e44bdb59a520b823c3ec68752152d634ebff50f3ab191bc46bd5ecb96ea8`。
该 release 再次通过 Backend/Worker live/ready、MCP 启动、Vue 编译/HTTP 和唯一进程门，
作为 R1 首次纵向版本的可复现云端检查点。

## 35. `-SkipFrontend` 会造成线上前端停机

Release `20260905010830-49750d32c53c` 在验证后端评测切片时使用了
`-SkipFrontend`。原部署脚本会先停止四个旧进程并原子替换整个项目目录，然后只跳过
新 Vue 进程的启动；因此该参数的实际语义是“允许前端停机”，并不是“保留原前端”。
后端、Worker 与 MCP 当时均通过健康门，但 8080 页面不可用。

发现后立即执行完整发布恢复，Release 为
`20260905011135-49750d32c53c`，bundle SHA-256 为
`2be4ef5f460a2778f149b687fb24d0fa78c5e1be53f6090fd9c5585b47a9bba8`；Backend、
Worker、MCP、Vue 编译/HTTP 和四进程唯一性均重新通过。

脚本现要求 `-SkipFrontend` 必须与显式的 `-AllowFrontendDowntime` 同时使用，否则在
打包、上传和远程停进程之前直接失败。线上增量验证默认始终执行完整发布；只有明确由
其他进程管理前端或刻意进行停机演练时，才允许使用这组参数。

## 36. Harness 指标、暂停计时与跨发布恢复演练

提交 `a4c8eacb` 将 Diagnostic Harness 的 durable create、幂等重放、CAS 状态迁移、
终态/原因、耗时和五类预算利用率接入 Prometheus。所有标签均来自固定枚举，明确不含
user、run、request、trace 等高基数标识。Release
`20260905011852-a4c8eacbe654`（bundle SHA-256
`dba2c860c87600792acc3f00ba5c4c46d031eea3c476e895199353a5d5fccf33`）中，真实
Redis NOAUTH 诊断产生了 1 次 create、4 次 durable transition、1 次 SUCCEEDED
terminal、1 次 duration observation 和五类 budget observation。

提交 `baf75e0f` 让页面在 sessionStorage 中只保存当前 Run ID，并在刷新后通过带 JWT 的
GET API 从 MySQL 恢复公开状态，而不是把 checkpoint 内容复制到浏览器；提交
`8005c5b3` 锁定 201/400/404/409、schema version、Trace ID、所有权隐藏和 checkpoint
私有 Artifact 不外泄等 HTTP 契约。

第一次跨发布演练发现一个真实语义缺陷：Run `0bf88f2f…` 能在后端重启后恢复，但
`WAITING_USER` 期间仍消耗最初 60 秒 deadline，补充证据后在 v9 被
`TIME_BUDGET_EXCEEDED` 终止。提交 `aa0afdc7` 将等待用户和服务停机期间的暂停时长，
与 `WAITING_USER → CONTEXT_READY` 的幂等 CAS 在同一事务中仅扩展一次 deadline。
不能在客户端重建 deadline，也不能让重放重复增加。

修复后先验证 Run `4d971b14…` 等待超过 60 秒仍可到 SUCCEEDED v9；随后在
`99be6c6d` 发布前创建 `f732041f…`，通过完整原子发布重启 Backend，再由浏览器恢复
同一 WAITING_USER v5 并补充 Redis NOAUTH 证据，最终到 SUCCEEDED v9，无超时且无
重复 Step。最终 Release `20260905014303-99be6c6dbc11`，bundle SHA-256
`0ef0e4731d96261070ced3613d1a64892391b1e42c6c6a7006e07fa0a3638e9c`，全部运行门
通过；该版本还提供只返回摘要、不返回逐例内容的可追溯诊断评测看板，并醒目标记 40 条
标签未人工复核、同集迭代、尚无密封留出集，不能将技术候选包装成正式基线。
## 37. 2026-09-05 R1 取消传播与 R2 工作记忆发布证据

- `0d274ae4` 为 Diagnostic Harness 增加请求 Context 取消收敛：SSE 断连会停止下游分析，Run 持久化为 `CANCELLED / REQUEST_CONTEXT_CANCELLED`；用户主动取消仍稳定保留 `USER_CANCELLED`。本地全量测试、`go vet`、Race 定向测试和 Linux 三产物通过。
- Release `20260905015430-0d274ae40bf5-dirty` 通过 Backend、Index Worker、MCP、Vue 门禁；此 release 的 dirty 仅来自本地交叉编译目录 `.codex-tmp` 未被 tar 排除，不是未提交业务代码。
- `7f46cd15` 将 `.codex-tmp` 同时加入 Git 忽略和部署 tar 排除。下一次 release `20260905021713-264ca3227bdd` 显示 `source dirty: False`，发布包从约 147 MB 降至约 90 MB。经验：发布器不能只依赖 `.gitignore`，打包命令也必须显式排除本地构建产物。
- `264ca322` 上线 R2 第一层：MySQL 权威消息同步写、Redis 最近 20 条/24h 热窗口、最新消息 ID 新鲜度核验、Redis miss/stale/error 时从 MySQL 重建，以及 `context-assembler-v2` Token 预算预览。
- 浏览器选择历史会话后首次显示 `rebuilt_from_mysql`、8/20 条；显式安全重建后再刷新显示 `hit`。真实新增一问一答后窗口变为 10/20，MySQL 最新消息 ID 为 1832；Redis LLEN 为 10，TTL 为 86237 秒，key 使用用户+会话 SHA-256。
- 远程取证脚本要特别处理 TOML 空密码。把多个值用 tab 输出后，Bash `read` 的空白 IFS 会折叠空字段，可能把后一个 `db=0` 错当成 Redis 密码并产生无害但误导的 `AUTH failed`。后续应使用非空白分隔符、JSON 或逐字段 base64，不把空字段交给默认 IFS 解析。

## 38. 2026-09-05 案例记忆发布、磁盘耗尽与发布门修复

- 提交 `7246d8b2` 上线“诊断假设 → 只读 Action Proposal → 用户显式确认 → Episodic Memory”第一版。确认操作把 immutable feedback、`confirmed` resolved incident 与 Outbox event 放在同一个 MySQL 事务内；`user + client_request_id` 保证重放只产生一次效果，状态版本阻止陈旧页面确认。解决描述复用诊断脱敏/指令过滤器。
- Index Worker 新增独立 `gopher.incident.index.v1` 主队列、延迟重试队列和 DLQ，只允许 `confirmed` 案例进入 Redis；候选分析不能建立索引。云端真实确认后只读证据为：feedback `1`、confirmed incident `1`、published incident outbox `1`、Redis case key `1`；案例队列有 `1` 个消费者，ready/retry/DLQ 均为 `0`。
- Release `20260905024338-7246d8b2dedb` 首次启动时 Backend/MCP/Vue 正常，但 Index Worker 长时间 `503 not_ready`。根因不是新消费者：宿主机 `/dev/vda3` 已为 `40G/100%`，RabbitMQ 的 Mnesia 明确报 `enospc / no space left on device` 后退出。空间主要被宿主机约 `4.7G` 历史 bundle、容器内另一份约 `4.7G` bundle，以及数十个每份约 `116-233M` 的 `GopherAI-.__previous_*` 回滚目录占用。
- 清理时没有删除 `gopherai2`、RabbitMQ/Redis 容器、镜像、卷、当前 `/root/GopherAI-` 或当前回滚点；仅在 `realpath` 和固定前缀校验后删除旧 bundle 与旧回滚目录，保留当前 bundle 和最近一个 previous。系统盘恢复到约 `63%`、可用约 `15G`，RabbitMQ 重启后 Worker 自动重连，两个主队列各恢复 `1` 个消费者。
- 发布脚本此前在 `if ! start_release ...` 中调用函数。Bash 在条件上下文里会抑制函数体内 `set -e` 的预期退出，因此 Worker ready 超时后函数继续启动前端，并以最后一个成功命令的状态把失败 release 标为 active。不能依赖函数内隐式 `errexit`；所有健康门必须写成 `critical_check || return 1`。
- 发布脚本现在为 MySQL、依赖 TCP、Backend live/ready、Worker live/ready、MCP TCP、Vue compile/HTTP 都显式传播失败；成功发布后自动只保留一个回滚目录和本次 bundle，并在删除前验证解析后的绝对路径。发布频繁的专用小盘服务器必须将“有界保留策略”视为发布流程的一部分，而不是事后人工清日志。

## 39. 2026-09-05 部署门复验与 Episodic Memory 自动召回

- 修复部署门后的干净 Release `20260905030029-569766edcab2`（commit `569766ed`，bundle SHA-256 `630f0477113810d96847355c4eb6c3443faecaf3dfe40b1ab03ea7fe26d6d06c`）完整通过 Backend live/ready、Index Worker live/ready、MCP、Vue 编译与 HTTP 门。发布后宿主机 bundle 目录仅保留当前 bundle/sha/manifest，容器只保留当前 bundle 和一个 `GopherAI-.__previous_*` 回滚目录；系统盘维持约 `63%`、可用约 `15G`。这次复验确认显式 `|| return 1` 和有界保留不是只通过 DryRun 的脚本改动。
- 提交 `83d1a2db` 上线 `case-recall-v1`：新 Diagnostic Run 只查询同 tenant、同 user 的 `confirmed + indexed` 案例，最多读取最近 100 条候选，再按错误特征 Jaccard `80%`、组件 Jaccard `20%` 确定性打分，至少命中一个错误特征且总分不低于 `0.60` 才能进入 TopK，最多返回 3 条。排序以 score、确认时间、案例 ID 形成稳定次序。
- 案例召回字段与当前 Hypothesis/Evidence 分离。召回成功、无匹配、依赖不可用分别返回 `hit/no_match/unavailable`；数据库或返回载荷异常时 fail-open 为 `unavailable`，不能修改当前根因、置信度、证据门或导致 Run 失败。页面明确标注“历史经验不是当前证据”。
- Release `20260905031528-83d1a2dbf39b`，bundle SHA-256 `2a94dd2f0d3d4e09b58951d6abd37961e3a36147f58ef882a25c75f7ef10ebe7`，四进程门禁全部通过。真实浏览器新建 Run `70176293-0456-4c6b-b30a-c59a84fdac9e`，输入 Docker + Redis 7.2 + NOAUTH 后，以 `policy-diagnostic-v2` 召回案例 `1a40fe0a…`，匹配 `redis_noauth` 与 `docker/redis`，页面显示 100%；当前诊断仍独立输出 95% 的待验证假设。
- 只读云端数据库检查在召回前后均保持 resolved incident `1`、resolution feedback `1`、incident outbox `1`，证明只读召回没有隐式写反馈。Prometheus 同时记录 `gopherai_case_memory_recalls_total{status="hit"}=1`，结果 histogram count/sum 均为 `1`，标签不含用户、Run、Trace 等高基数标识。

## 40. 2026-09-05 Profile Memory 候选、冲突与用户治理

- 提交 `407a0002` 上线第一版环境 Profile Memory。诊断输入只从固定 allowlist 提取 `os`、`go_version`、`deployment_mode`、`cloud_provider`、`redis_version`、`mysql_version`，并先写成 90 天候选；tenant/user 只保存 SHA-256，来源 Run、置信度、版本、最后观察时间和过期时间可追溯。候选提取 fail-open，失败不能阻断 Diagnostic Run，也不能修改当前 Hypothesis/Evidence。
- Release `20260905033424-407a0002935c`，bundle SHA-256 `af2cf363c5e55ea80cbc733db9f290858638d9ae44c80f90c53d4b19f7198fa9`，Backend、Index Worker、MCP、Vue 编译/HTTP 和唯一进程门全部通过。
- 真实浏览器输入“阿里云 ECS、Ubuntu 22.04、Docker、Go 1.24、Redis 7.2、NOAUTH”后，Profile 控制台显示 5 条待确认、0 条已确认；诊断仍按 `policy-diagnostic-v2` 独立形成 95% 待验证假设。将 Redis 值改成 7.4 并确认后生成 active v2、置信度 100%、有效期 180 天，其余 4 条仍为候选。
- 随后另一个 Run 观察到 Redis 7.5，系统没有静默覆盖 7.4，而是把两个版本都标为 `conflicted`，已确认数降为 0、冲突数升为 2；用户再次选择 7.4 后生成 active v3 并 supersede 两个冲突版本。只读数据库最终证据为 active/user_corrected `1`、candidate/diagnostic_observation `4`、superseded observations `2`、superseded correction `1`。
- 该版本只证明 M5-14 和 M5-16 的候选/CRUD/冲突纵向链路；active Profile 尚未进入 Context Assembler。候选、冲突、过期事实一律不得参与模型上下文，后续召回必须再次落实同用户 ACL、相关 TopK 和 Token 预算门，不能因为页面出现“三级记忆”就宣称 M5-15/M5-17/M5-29 已完成。

## 41. 2026-09-05 Profile 相关召回与真实生成上下文

- 提交 `3784ecb8` 完成 `profile-recall-v1` 纵向切片：MySQL 查询同时约束 tenant/user hash、`active`、置信度不低于 0.8、未过期；应用层再按当前问题中的 Redis/MySQL/Go/容器/云/OS 信号筛选，TopK 不超过 5。Context Assembler 再执行固定键 allowlist、同键去重和 Token 预算。候选、冲突、过期、低置信和无关事实都不能进入模型输入。
- Profile 查询在普通聊天的真实生成前执行；命中时以独立 system context `confirmed_environment.<key>=<value>` 注入，并附加“当前用户明确陈述/项目证据优先”的冲突规则。查询失败按 `unavailable` fail-open，不阻断模型；Prometheus 只使用 `hit/no_match/unavailable` 固定标签并记录耗时和返回条数。
- Release `20260905035546-48063a54986e`（commit `48063a54`，bundle SHA-256 `c216438f9aa1ad107b4acaab82fb26dfc260784afe809c56c37b7601033fb072`）通过四进程门。真实普通聊天连续询问已确认 Redis 版本，两次均回答 `7.4`；页面显示 `Profile 命中 1 条 · profile-recall-v1` 和实际组装项 `confirmed_environment.redis_version=7.4`。无关问题 `PROFILE-NO-MATCH` 显示“Profile 无相关事实”，上下文没有 Redis 项。
- 现场指标为 `gopherai_profile_memory_recalls_total{status="hit"}=1`、`no_match=1`、`unavailable=0`，单次命中返回 1 条、耗时约 1.16ms。测试时发现回答持久化后，预览会重复展示最新问句；`48063a54` 改为跳过与当前问题匹配的最新 user 消息，并把页面文案改成“按当前历史和预算重建的上下文预览”，避免把回答后重建结果冒充生成时快照。

## 42. 2026-09-05 记忆安全契约评测候选

- 提交 `24f3b559` 新增 `devsupport-memory-v1` 的 20 条确定性契约集，相关召回、过期/冲突值、删除后不可召回、跨用户/租户隔离和 Context Token 预算五类各 4 条。评测器复用生产 `profilememory.Selector` 与 `memory.Assembler`，避免另写一套只为评测通过的规则。
- 本地候选报告结果：相关记忆召回 `100%`、过期/错误注入 `0%`、删除后召回 `0`、跨 principal 泄漏 `0`、预算遵守率 `100%`、确定性重放率 `100%`。20 条标签仍为 `pending_user`，因此 `baseline_eligible=false`；该报告只证明确定性选择/隔离/预算契约，不冒充真实 MySQL 故障注入、语义向量召回或长对话回答质量。
- 页面“三级记忆”面板新增只返回汇总、不返回逐例内容的中文看板，并展示报告 SHA-256、技术门、人工复核状态和三项限制。后端严格校验恰好 20 条、报告元数据和 baseline 资格，提供私有缓存 ETag。
- Release `20260905041551-24f3b559b235`（commit `24f3b559b235d99334e6590a76fb1c532fbcec4e`，bundle SHA-256 `77af3f5acd18ad2530027395705084a474494206cb63082fc66c7a4c88af6a8f`）通过 Backend/Index Worker live/ready、MCP、Vue 编译/HTTP 和唯一进程门。本地全量 `go test ./...`、`go vet ./...`、前端生产构建以及 profile/evaluation/memory/controller 的 Race 定向测试均通过。

## 43. 2026-09-05 结构化上下文压缩与累计预算缺陷

- 提交 `a9d424d9` 将 Diagnostic Harness 的持久化 Checkpoint、公开 Step 和会话工作窗口接入 `context-assembler-v2`。输出显式保留 goal、constraints、confirmed facts、open questions、completed/failed steps、evidence refs 和 next action，并只使用公开摘要；Checkpoint 私有 Artifact、principal hash 和隐藏思维链不会进入 API。
- 在构造长上下文成对集时发现原 Assembler 只判断“单条 Working Message + 当前已用 Token”是否超限，却没有把同轮已经选择但尚未 append 的多条消息累计进去，因而多条消息各自可放入、合计却可能超预算。修复后选择阶段维护累计 Token，并新增多消息回归测试；这说明 Context 评测不仅用于展示指标，也能发现线上预算实现缺陷。
- `devsupport-context-compression-v1` 包含 12 条候选用例，`answer/clarify/refuse/resume` 各 3 条。当前结果为 constraints、confirmed facts、open questions、next action 保留率均 `100%`，平均估算 Token 降幅 `52.56%`，超预算 `0`，确定性重放 `100%`。标签仍为 `pending_user`，Token 是稳定本地估算而非供应商账单，因此 `baseline_eligible=false`。
- Release `20260905043801-a9d424d9ee2d`（commit `a9d424d9ee2d78668b7e40d0d3e4959b44c96b13`，bundle SHA-256 `1f736d41ae33f7e561d82c400588066fb5a3d5e4fed062837b93d44b16e96ddc`）通过 Backend/Index Worker live/ready、MCP、Vue 编译/HTTP 和唯一进程门。本地全量测试、`go vet`、前端生产构建及 memory/evaluation/agentrun/evaluation-controller Race 均通过；登录态页面的集中人工验收留到用户约定时段。

## 44. 2026-09-05 Tool Runtime 治理内核与部署清单工具

- 提交 `bc84748a` 建立统一 Tool Runtime：工具元数据和精确名称 Registry、受限 JSON Schema、Intent/Permission/SideEffect 默认拒绝、调用预算、Context 超时/取消、结果大小上限、稳定 `tool-message-v1`、MySQL 脱敏审计及 Prometheus 固定标签指标。未知或拼错工具名不会模糊猜测；客户端不能提交权限、副作用等级或预算，HTTP Controller 从 JWT 主体与服务端策略构造这些字段。
- 第一个真实工具 `deployment_manifest_lookup@1.0.0` 只读取固定的 `release-manifest.json`，不接受路径参数；文件限制 64 KiB、严格 JSON 解码并只返回发布标识、Git SHA、构建目标、组件和回滚策略等白名单字段。计算器、时间、天气等 demo 工具没有重新注册；MCP 目前仍只是禁用 demo 的协议宿主，后续 Adapter 必须进入同一 Runtime。
- 首次 Release `20260905050617-bc84748ad34f` 通过四进程门并完成真实页面调用。现场审阅时发现工具审计虽有 Call ID，但还缺独立 Trace ID，不足以满足跨层 lineage；没有把缺口留到后续，而是由 `78784c47` 增加 Trace ID 持久化后再次完整发布。
- 最终 Release `20260905051431-78784c4742b8`，bundle SHA-256 `11e74712aedddd4b192fafa8d1c4cadded4857564f795dc8c17bee8d9efeebf5`。浏览器真实返回当前 release、commit `78784c4742b84dbebb6a0cff3a477a7e20767c55` 和 `release-manifest:<release_id>` 证据引用。MySQL 最新审计只显示 tool/version/status 与三个哈希/追踪长度：Trace ID `36`、Args/User hash 均 `64`，不保存原始参数、结果或 principal；Prometheus 对 accepted/success/duration 各记录 `1`，标签不含 Call/Trace/User。
- 本地 `go test ./...`、`go vet ./...`、Tool Runtime/Controller/Observability Race、三份 Linux 二进制和 Vue production build 均通过。当前只完成治理纵切和第一个工具；幂等瞬时重试、熔断、缓存、Health/Log 工具、ToolAgent 与 30 条评测仍按 M6 后续任务推进，不能提前宣称 G6 完成。

## 45. 2026-09-05 Health Tool、幂等重试、缓存与熔断

- 提交 `fc5af022` 新增 `service_health_snapshot@1.0.0`。调用参数只允许 `backend/index_worker × live/ready` 四种组合，目标被编译进服务端 allowlist；工具不接收 URL、host 或 port，HTTP Client 禁用代理与重定向，响应限制 32 KiB，再映射到固定健康 Schema。观察到 503/not_ready 属于“探测成功但目标不健康”，与网络/超时执行失败分开表达。
- Tool Definition 增加可审计的幂等、最大尝试次数、Cache TTL、熔断阈值和打开时间。Runtime 只对 `idempotent + retryable` 的瞬时失败重试，所有 attempt 共用父 Context 总超时；连续执行失败打开 circuit，窗口结束仅允许一个 half-open probe，成功才回到 closed。缓存键包含 tool/version/args hash 和 tenant/user hash，先重新经过 Schema/Auth/SideEffect/Budget 再取缓存，不能借缓存绕过治理或跨用户复用。
- Release `20260905052421-fc5af02246b2`，bundle SHA-256 `e67a77b51f27824d0d971f9fe5bdec19f9789e3e5e597677f1336febaa119197`，四进程门通过。真实浏览器 Backend ready 返回 MySQL/RabbitMQ/Redis/Model Config 全部 up；同参数在 750ms 内第二次调用显示 `cached=true`。Worker ready 返回 HTTP 200/ready。MySQL 审计汇总为 fresh success `2`、cached success `1`；Prometheus 为 calls success `3`、cache miss `2`、hit `1`、accepted `3`、circuit closed `1`。
- 线上没有为了展示熔断而故意停止唯一服务。retry/open/half-open/closed 使用可控时钟与故障 Tool 的确定性单测验证，并通过 Race；后续 30 条评测会把这些状态迁移整理为独立证据报告。短 TTL 缓存已完成，stale fallback 尚未实现，因此 M6-06 仍保持部分完成。

## 46. 2026-09-05 有限规划 ToolAgent

- 提交 `84ee3720` 上线 `bounded-tool-planner-v1`。它是可审计的控制面规划器，不输出隐藏思维链：只返回 `execute / answer_without_tool / refuse`、稳定 reason code、最多 2 个 allowlist 调用及固定参数。当前只选择部署清单和服务健康两类真实 DevSupport 工具；普通知识问题不调用工具，重启/删除/Shell/SQL 写入等请求在规划层直接拒绝。
- Compound 查询“给出当前发布清单，并检查后端和 Worker 健康状态”会形成两步计划：`deployment_manifest_lookup {}` 与 `service_health_snapshot {service:all,probe:ready}`。每步仍重新进入统一 Runtime 的 Schema/Auth/SideEffect/Budget/Timeout/Audit/Metrics，不允许 Planner 直接调用 Adapter；调用预算按计划长度固定，第二步 `used_calls=1/max_calls=2`。
- Release `20260905053234-84ee37209460`，bundle SHA-256 `27d8bb58907f1fafdac9a64d0dbc6aefbe1ba978eac5185d1dd5101600228ac4`，四进程门通过。线上页面 compound 计划两步均 success；“重启后端并删除旧日志”显示 `UNSAFE_ACTION_REQUESTED` 且零调用；“解释 Go interface”显示 `NO_SUPPORTED_TOOL` 且零调用。该版本的确定性 Planner 是安全基线，尚未宣称 LLM 自主规划；后续可让 LLM 只产生候选计划，再由同一 deterministic policy validator 裁决。
