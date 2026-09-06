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

## 47. 2026-09-05 工具全生命周期 30 条契约评测

- 提交 `e5c2f16c` 新增 `devsupport-tool-runtime-v1`，严格固定为 30 条、5 类各 6 条：工具选型、Schema、授权/预算、重试/超时/取消/熔断/缓存，以及危险动作、错名、超限结果和缓存 Principal 隔离。Loader 拒绝未知字段、未知场景、类别错配、不平衡或非 30 条数据，防止无意改变统计口径。
- 评测器直接复用生产 `bounded-tool-planner-v1` 和 Tool Runtime；云依赖只由确定性 Fixture 替代。每条同时断言公开 decision/ToolMessage、稳定错误码、底层执行次数和脱敏审计条数，并在全新 Runtime 中隔离重放。当前五类通过率均 `100%`，危险动作执行率 `0%`、未知工具执行 `0`、审计覆盖和确定性重放均 `100%`。
- 30 条标签仍为 `pending_user`，所以即使技术门通过，`baseline_eligible` 仍为 `false`。API 只返回汇总与报告 SHA-256，不把逐例 Fixture 暴露给浏览器；页面明确声明它不等同于真实云网络故障注入，也不代表开放域 Agent 已获得自主运维权限。
- Release `20260905054911-e5c2f16c45c9`，bundle SHA-256 `a65366efdb9d3184eb383f4a6a82e94ce62c613b74f1da8581ea04b07ab7e046`。Backend/Index Worker live/ready、MCP、Vue 编译/HTTP 与唯一进程门全部通过。线上已登录页面打开“受治理工具 → 查看 30 条工具评测”后真实显示九项指标、技术门和候选报告 SHA-256 `055e9a1a5eb9b63c2ad564e2562d864ce81f22f4cf25b3041b19d82c3fe57b66`。本地全量测试、`go vet`、Vue production build 及 evaluation/toolruntime/toolagent/controller Race 测试均通过。

## 48. 2026-09-05 有界日志签名工具与线上路径泄漏修复

- 提交 `d15dba5b` 上线 `bounded_log_signature@1.0.0`。服务只允许 `backend/index_worker/mcp`，签名只允许 `panic/auth/timeout/connection/error/warning`；服务端固定映射日志位置，调用方不能传 path、regex、host、命令或行数。单次只读尾部 256 KiB，最多返回最近 20 条匹配，响应包含扫描/命中/返回计数、截断标志、脱敏摘录和逐行 SHA-256。
- 文件打开前要求固定目标是普通非符号链接文件，并验证解析路径没有逃出项目根。日志摘录会去 ANSI 控制序列并脱敏 URL/DSN 凭据、Bearer、password/token/secret/api-key、JWT 和邮箱。ToolAgent 可把“Worker 健康 + slow sql 告警日志”规划为 Health + Log 两个调用，二者仍分别经过统一 Runtime。
- 首次 Release `20260905060051-d15dba5bb09e` 在线命中 Worker `SLOW SQL` 后，发现 Go 二进制把本地编译绝对源码路径写进 GORM 日志。提交 `4c6a3498` 增加跨 Windows/Linux 的源码路径归一化为 `<source>/<file>.go:<line>`，并以回归测试锁定；这是线上取证反向推动安全修复的真实闭环。
- 最终 Release `20260905060533-4c6a3498ecf7`，bundle SHA-256 `2db1ca61c3f9d4e240f6ba6618ab242f1ac9dc30dbfdc23845aecaa939a7970e`，四进程门全部通过。线上 direct 空匹配仍返回可审计 success，不把“无错误”伪装成失败；ToolAgent 的 Worker ready + warning 两步均 success。页面与扫描结果不再出现 `F:/Kama_Project`。MySQL 审计记录为 tool_primary success `3`、tool_agent_v1 success `1`；Prometheus 当前记录 primary `2`、agent `1` 与 cache miss `3`（指标按进程重启清零，数据库审计跨发布保留）。更新后的 30 条候选评测仍全门通过，报告 SHA-256 为 `11f4b4fe932c3a4a6f2175bd84fa6295ffaf4617311e2b7886574ea2cccf9f53`。

## 49. 2026-09-05 MCP 协议源进入统一治理

- 提交 `0730ee3d` 将 MCP 从演示工具宿主改为单一、受限的部署证据协议源。MCP Server 只暴露 `deployment_manifest_source`，不接受调用参数，只读取固定的 `release-manifest.json`，限制 64 KiB、严格拒绝未知字段，并只返回公开白名单字段；计算器、时间、天气、联网搜索等 demo 工具继续禁用。
- 主后端新增 `mcp_deployment_evidence@1.0.0` Adapter。远端 MCP 只负责协议取数，调用前后的精确工具名、Schema、Intent、Permission、SideEffect、预算、超时、重试、缓存、熔断、结果上限、审计和指标全部由统一 Tool Runtime 执行。单测证明权限拒绝时 MCP 协议调用次数为 0，远端未知字段会 fail-closed，传输错误只按只读幂等规则重试。
- MCP 默认从 `:8081` 收紧为 `127.0.0.1:8081`。云端 `/proc/net/tcp` 的监听记录为 `0100007F:1F91 0A`，即 loopback:8081 LISTEN；它不是新增公网工具入口。ToolAgent 的显式 MCP 发布清单问题只规划该 Adapter，不能绕过 Runtime 直连协议源。
- Release `20260905061957-0730ee3debaa`，bundle SHA-256 `97e6fc03d46d7c3706554b9b115c48158ac55cb8c1272edb649f7a962dd012a8`，Backend/Index Worker live/ready、MCP、Vue 编译/HTTP 和唯一进程门全部通过。浏览器 direct 与 ToolAgent 两条真实路径均返回当前 release、Git SHA 和 `mcp:deployment_manifest_source:<release>` 证据引用。
- MySQL 持久审计分别记录 `tool_primary/success/1` 与 `tool_agent_v1/success/1`；Prometheus 进程内指标对应 success 各 1、cache miss 2，标签不含 URL、用户、Call ID 或 Trace ID。这个纵切证明 MCP 只是 Adapter 协议边界，而不是治理旁路；当前仍只有一个受限来源，不能宣称已有通用外部 MCP 市场接入。

## 50. 2026-09-05 工具 stale-if-error 受控故障演练

- 提交 `c9107e6d` 补齐 M6-06：Definition 可声明有界 `stale_if_error_ms`，但 Registry 只允许“只读 + 幂等 + 已启用短 TTL Cache”的工具使用，窗口上限 5 分钟；写工具、无 Cache 工具和越界策略在注册期拒绝。缓存键继续绑定 tool/version/args/tenant/user，Schema/Auth/SideEffect/Budget 仍在任何缓存读取前执行。
- 新鲜 TTL 到期后，Runtime 会尝试真实刷新；只有依赖执行失败、工具超时或熔断已打开且仍处于 stale 窗口，才返回 `status=success, cached=true, stale=true`，同时携带固定 `degraded_reason` 和 `tool-cache-stale:<tool>@<version>` 证据引用。调用方主动取消/截止、结果序列化失败和响应超限不会被旧数据掩盖；窗口到期后 fail-closed。
- 30 条工具候选集把原单纯 cache-hit 用例升级为“首次填充 → 新鲜命中 → TTL 后依赖失败 → 显式 stale”三调用链，仍保持 5 类各 6 条，所有技术门 `100%`、危险动作执行率 `0%`、未知工具执行 `0`、人工标签仍待复核。新报告 SHA-256 为 `13769f4edb6b1c5496ca6611cb8e3c478cbd96ac7421e8ec88b45b70db7db8c2`。
- Release `20260905064425-c9107e6df34e`，bundle SHA-256 `189c1f28b4ff682e701f38881c545f501348ef82d6d0f9097e2425982b3d6c1b`，四进程门通过。演练只短暂停止 loopback MCP 协议源：先建立当前发布清单缓存，等待 3 秒 TTL 后停 MCP，页面返回 success 但醒目标记 `陈旧证据降级 / TOOL_EXECUTION_FAILED`；恢复 MCP 后下一次调用重新变为非缓存的新鲜结果。
- 第一次演练在人工操作超过 30 秒 stale 窗口后调用，按设计返回 `TOOL_EXECUTION_FAILED` 而不是旧数据；缩短停机/调用间隔后才命中 stale。这不是测试失误应被隐藏，而是窗口上限真实生效的证据。最终 MySQL 最近审计包含 fresh success 3、显式 stale success 1、窗口外 error 1；Prometheus 为 `stale_fallback=1`、circuit 最终 `closed=1`，Backend、Worker 与 MCP 均恢复 ready。

## 51. 2026-09-05 Confirm Resolution HITL 内部写治理

- 提交 `cb7fd326` 将原有“确认解决并写入 Episodic Memory”接入 `confirm_resolution@1.0.0`。页面和 HTTP 成功/冲突契约不变，但服务端现在先把固定字段组装为 Tool Invocation，再经过精确 Registry、严格 Schema、`troubleshooting` Intent、`devsupport:resolution:confirm` 权限、`internal_write` 副作用、单次预算、5 秒超时、幂等重试、结果上限、审计和指标，最后才进入原有 Feedback + Incident + Outbox 同事务。
- Principal 只由 JWT 中间件构造并通过 Runtime 的私有执行上下文交给 Adapter，客户端 JSON 不能提交 user、permission、side effect 或 budget。该 Tool 不注册到公共 Tool Runtime/ToolAgent；线上目录仍只有四个 read-only Tool。单测同时锁定缺权限、只读调用者、预算耗尽均零执行，以及 `external_write` 在内部写授权下仍被拒绝。
- 30 条候选评测中的授权/安全用例已改为调用真实 HITL Adapter：合法内部写执行 1 次并审计 1 次；只读调用者执行 0 次；外部写执行 0 次。加上 stale-if-error 用例后五类通过率、审计覆盖和确定性重放仍为 `100%`，危险动作率 `0%`，报告 SHA-256 更新为 `86f57a4bb7af8daf98bf5603f8d8bfb26ec70b89ed82faf27f0c289496037421`，人工标签仍是 `pending_user`。
- Release `20260905070322-cb7fd326710d`，bundle SHA-256 `7869f94dad17f3855ab0f47c91e09e9a72176347ef5e0d609d277d579c844418`，四进程门通过。真实页面新建 Redis NOAUTH 诊断，先预览且不写，勾选明确确认后才写入案例 `dd196702…`，RabbitMQ 异步索引随后变为 indexed。
- MySQL 工具审计为 `confirm_resolution / human_confirmed_action_v1 / success`，cached/stale 均为 0，Args/User hash 长度 64、Trace ID 长度 36；Prometheus 为 accepted 1、success 1、cache bypass 1。数据库最新案例为 `confirmed/indexed`。这证明“人工确认是能力授权边界”，不是让 Agent 自主执行修复。

## 52. 2026-09-05 候选计划修复边界与重复动作熔断

- 提交 `899aada7` 将 ToolAgent 拆为“候选规划器 + 生产候选执行器 + 统一 Runtime”。未来即使把确定性 Planner 替换为 LLM Planner，候选计划也会在执行边界重新截断为最多 2 个调用；Runtime 仍是精确工具名、Schema、权限、副作用、预算和审计的唯一权威，Planner 不能直连 Adapter。
- 只有稳定错误 `TOOL_ARGUMENTS_INVALID` 可以触发修复，最多 2 次。修复反馈只包含 call index、attempt、精确 tool name、error code 和 rejected args hash；不包含原始参数、工具输出、凭据或内部错误。修复器不能更换工具名，拼错/未知工具直接 `UNKNOWN_TOOL_REJECTED`，不做相似匹配。每次被 Schema 拒绝的候选仍生成独立 ToolMessage 和脱敏审计。
- 每个 Agent Run 创建独立 `ActionGuard`，以 `tool name + version + canonical args hash` 作为动作签名。相同 JSON 即使只改变空白也视为同一动作；第二次返回 `no_progress / TOOL_NO_PROGRESS`，真实 Tool 只执行一次，且该拒绝同样进入审计与固定标签指标。它不是分布式锁，也不替代写操作幂等键，只负责阻止单个 Agent 循环无进展烧 Token。
- 30 条候选评测复用同一生产执行器，加入“错误类型参数 → 两次修复 → success”和“重复动作 → NO_PROGRESS”两条门禁。当前五类通过率、审计覆盖、确定性重放、错参有界修复和重复动作熔断均为 `100%`；危险动作执行率 `0%`、未知工具执行 `0`。报告 SHA-256 为 `a5c2b01f4e13f6506dfbd4a6951d26feeb6904f145ea7a80e4cf258bfdcb21f9`，标签仍待用户人工复核，所以不是正式基线。
- Release `20260905073107-899aada7ec6e`，bundle SHA-256 `c24303aae45b84dd36b8d1d11171a0b9319516f12d8fd3eb92e5f2d1a54fb384`，Backend/Worker/MCP/Vue 与唯一进程门全部通过。真实页面 compound 计划的 Manifest + 全服务健康两步均 success；线上评测面板显示新增两项 100% 门禁和同一报告 SHA。MySQL 两条新审计均为 `tool_agent_v1/success`，Args/User hash 长度 64、Trace ID 长度 36；Prometheus 对两个工具各记录 accepted/success 1。
- 从 Windows PowerShell 把 here-string 直接管道送给 `ssh ... bash -s` 时，即使变量中先做 `-replace "`r", ""`，PowerShell 的 native pipeline 仍可能重新以 CRLF 编码文本，令远端 heredoc 终止符变成 `EOS\r`。取证查询主体可能已经成功，但最终退出码会是 1。可靠做法与发布脚本一致：先把 LF 文本编码为 UTF-8 base64，再让远端 `printf %s '<base64>' | base64 -d | bash`；不能因为前半段有输出就忽略失败退出码。

## 53. 2026-09-05 官方文档工具、指标补漏与旧 Skill 退役哨兵

- 提交 `86bdd743` 上线 `official_document_search@1.0.0`：仅允许 Go Context、Redis ACL、RabbitMQ DLX、Prometheus 告警四个固定文档 ID，不接收 URL/域名/路径；生产传输禁用代理，DNS 解析后拒绝私网/回环/链路本地/测试网段，只允许 HTTPS 同 host 有界重定向、HTML/纯文本和 256 KiB 解压后正文。真实浏览器四文档全部 success，输出规范 URL、抓取源、正文 SHA、字节数、匹配数和有界摘录。
- 首次调用后 Prometheus 把新工具折叠为 `unknown`，原因是低基数工具白名单漏登记。`48623166` 补齐标签与防回归测试；重新发布后一次真实超时和一次成功都正确记录为 `official_document_search`，证明错误也可观测。最终 Release `20260905081443-236f66ca9933`（bundle SHA-256 `7511f4637c605e9de7952f97a8f1e4e0579edcb1c39fe612618258c5c8083172`）再次调用 Redis ACL 成功，指标 accepted/success/miss/closed 各可见。
- 基线提交 `a4bf5146` 已按用户授权物理删除旧 Skill 代码/API/UI，不能再伪称保留了在线恢复开关。`236f66ca` 增加不执行任何旧代码的退役哨兵：旧 `/api/v1/skill/*` 稳定返回 410、`LEGACY_SKILL_RETIRED` 和新目录地址，并只累计固定 `skill_api` 标签。云端计数从 0 经一次受控探针变为 1。若需恢复只能使用 Git/发布回退，禁止为了形式门禁复活天气、计算器等无场景能力。
- R3 全量证据已冻结到 `GlobalExperience/2026-09-05-r3-governed-tools-evidence.md`。30 条技术门仍全通过，报告内容 SHA-256 `df073ce7de9374e48f207236b52e2aa39e46b812295d0df341f83430dc64e3ed`；标签仍是 `pending_user`，所以只可称技术候选，不可称人工正式基线。
- 页面刷新自动化曾因旧标签页处于面板状态而等待元素直至超时。此后浏览器验收应新建同源标签页，先取 DOM 状态，再以 4～5 秒 locator 超时做单步动作；不得把多个无超时的 reload/click 串成一个长操作。后端健康检查与浏览器动作应分开判断，避免将 UI 控制阻塞误判为服务宕机。

## 54. 2026-09-05 Strategy 控制面与案例策略 Shadow

- `1576b975`～`0d2cfc3c` 上线 Strategy Registry、MySQL 权威/Redis 30 秒缓存的版本策略仓库和稳定加权 Selector。页面只允许已登录用户做 `SHADOW ONLY` 演算，固定显示策略来源、版本、Hash、Bucket、预算和依赖过滤，不接管实际聊天。云端主动删除精确策略缓存键后，下一次请求从 MySQL 读取并重建相同 Hash/Bucket，证明 Redis 只是可丢缓存，不是事实源。
- 首次页面实测发现无依赖策略在 JSON 中输出 `null`，Vue 对 `.length` 的读取使整个页面崩溃；`0d2cfc3c` 同时在后端强制序列化 `[]`、前端防御旧载荷，并补回归测试。跨后端/前端边界的集合字段应在契约层固定为空数组，不能把 Go nil slice 的序列化细节留给客户端猜测。
- `5d1129e0`、`719c7180` 上线 `diagnosis_case_based` Shadow。强案例必须同时满足相似度 `>=0.85` 与当前标准诊断 Evidence 一致，输出也只可标为 advisory；弱/无匹配及 1.2 秒召回失败均保持 `diagnosis_standard`，不写反馈、不执行修复。Release `20260905091954-719c71804aff`，bundle SHA-256 `8128066391bd9c4cddf399c4105bc9f47955de267459442056701ee76262f785`，Backend/Worker/MCP/Vue 门均通过。
- 云端页面以 Redis NOAUTH 命中 2 条 confirmed+indexed 案例并给出 100% 候选优先级；无关打印机输入稳定 no_match。Prometheus 记录 `strong/success=1` 与 `none/success=1`，标签不含用户、Trace 或案例 ID。经验：案例增强的收益必须通过“标准基线不变 + 当前证据一致 + 历史案例已确认”三重边界表达，不能把相似历史处置包装成当前根因。

## 55. 2026-09-05 Bounded Planner：先证明该拆，再执行

- `7cc7e6b2` 上线 `bounded-collaboration-planner-v1`。Planner 不让 LLM 自由生成 Agent 图，只读取 Diagnostic Extractor 已脱敏的结构：单故障保持一个 DiagnosticAgent；两个独立故障域，或故障诊断与项目证据核对可以独立执行且复杂度达到 70，才形成 KnowledgeAgent + DiagnosticAgent 两个公开子任务。
- 最大 Agent、工具、迭代、Token、成本和总超时全部来自服务端 Strategy Registry；两个子任务的预算和不超过父预算，且 `may_spawn_agents=false`。客户端请求仅允许 `message`，提交 `max_agents` 等未知字段会在进入 Planner 前拒绝。复杂度分数是可回归的启发式门，不得解释为多 Agent 已经提高 30%/100% 的质量。
- Release `20260905093545-7cc7e6b2ccc5`，bundle SHA-256 `bc8954c6543a9e3cb3b6f0fa410b0bbba6a32e59eedb74990c3700dd17703077`，四进程门通过。真实复杂输入得到 2 Agent、120 秒总预算，简单 Redis NOAUTH 得到 1 Agent、90 秒；Prometheus 各记录一次，标签不含原文或用户。工程经验：在并发执行前先单独发布/验收确定性 Plan，可以让“误触发”和“执行失败”分开归因，避免 Agent 在执行细节里掩盖规划错误。

## 56. 2026-09-05 并行执行先锁定取消、预算与顺序

- `5a9ff8cf` 上线 `bounded-parallel-executor-v1` 内核。Agent 反序完成时结果仍按 Plan index 稳定输出；单任务超时只标记该任务，不取消成功兄弟；父请求取消传播两个子 Context。Go 无法强杀不合作的 goroutine，所以 Runner 合约必须响应 Context，外层总超时仍会按缺失任务返回有界结果。
- Executor 在 Runner 前再次使用 Diagnostic Extractor 脱敏，防止调用方绕过 Planner 直接把凭据送入 Agent。输出限制 Summary、Claim、Evidence、Follow-up 数量；Evidence tenant 必须与请求一致。超出任务预算时 Claim/Evidence 全部丢弃，实际 Token/工具/迭代/成本 Usage 保留，以免“拒绝输出”反而让成本观测归零。
- Release `20260905094729-5a9ff8cf5bf1`，bundle SHA-256 `f0bd974998af1f1fb0767d2d37d609fdb3e800146ad8f182880ae90cc5ee8198`，四进程门通过。该 Release 只有执行内核和规划页面信号澄清，没有把测试 Runner 包装成线上多 Agent；真实执行入口必须等 Evidence-aware Synthesizer 与生产 Runner 都通过后再开放 Shadow。

## 57. 2026-09-05 Evidence-aware Synthesizer：只合并可引用结论

- `fd4bd99c` 上线 `evidence-aware-synthesizer-v1` 内核。合成器不读取 Agent 自由文本过程，只接收通过 Executor 边界校验的结构化 Claim/Evidence；每个 Claim 必须引用同 tenant 的已登记 Evidence，未知引用、同 ID 不同载荷、Claim ID 冲突和冲突元数据不一致都会显式拒绝，不能用一段流畅总结掩盖来源问题。
- 相同结论会合并 Agent 来源、Evidence 引用并取较高置信度；同一事实键出现不同值时，两条 Claim 都保留为 `conflicted`，返回显式冲突并回退 `diagnosis_standard`，不会静默选边。任一子 Agent 失败时只保留成功兄弟的可验证结论，状态为 `partial`；全部没有可验证结论时为 `insufficient`。
- Release `20260905100014-fd4bd99cd10b`，bundle SHA-256 `25c94622dfec681ef0ddcdb5f68f46fe2c63ae4eaab565f049f1ffe60f074af3`，Backend/Index Worker/MCP/Vue 和唯一进程门全部通过。该版本仍只有合成内核，不伪造线上 Agent 运行证据；下一纵切 M7-08 才接 KnowledgeAgent 与 DiagnosticAgent 的真实 Shadow 入口。

## 58. 2026-09-05 diagnosis_collaborative 真实 Shadow 与局部降级

- `33710305` 接通真实 `diagnosis_collaborative` Shadow：Planner 达到 70 分门槛后，最多并行运行 KnowledgeAgent 与 DiagnosticAgent；前者只读取当前用户有权访问的项目文档并输出已校验引用，后者复用标准诊断与 confirmed 案例的 advisory 召回。两个 Runner 都禁止递归创建 Agent，入口不创建会话、不写对话记忆、不执行修复，也不改变下方正式聊天结果。
- 第一次云端 Smoke 用默认 `m3b-config.json` 样例只得到 40 分，取证发现 Planner 只识别“文档/手册”等词，没有识别明确文件名。`1a143b9f` 把 `.json/.yaml/.toml/.md` 和“配置文件”加入确定性知识核对信号，并补“单纯文件问答仍不启动多 Agent”的误触发测试。相同样例随后稳定得到 80/70、两个任务。
- `b59b522a` 补齐 M7-09 局部降级语义：子 Agent 的 `insufficient` 与执行异常分开记录，Evidence Gate 不通过时保留公开原因、Evidence 数量和 Usage，但不把“证据不足”当作成功 Claim。Synthesizer 只保留成功兄弟的引用结论，输出 `partial` 并显式回退 `diagnosis_standard`；不进行无界重试。
- 最终 Release `20260905103221-b59b522aa536`，bundle SHA-256 `3e3428cabf9b9457280b34e97e14b838632186b13bec179c1cb4f418b6bd7e61`，四进程门通过。真实文档 + HTTP 502 样例在约 2.5 秒内得到 Knowledge/Diagnostic 两个成功任务、2 个 Claim、4 个合成引用、0 冲突、0 拒绝；不存在文档样例得到 Knowledge `insufficient/no_cross_retriever_support`、Diagnostic success、合成 partial 且只保留 1 条诊断引用。简单 Redis NOAUTH 样例保持 20/70，零子 Agent 执行。
- Prometheus 对三种关键路径使用固定低基数标签：完整路径为 run `complete=1`、两个 Agent success；局部降级为 run `partial=1`、Knowledge insufficient、Diagnostic success、synthesis partial；简单请求为 `single_agent/not_executed`（进程重启前已验证）。经验：必须把“Planner 认为值得拆”与“每个子任务实际有足够证据”作为两个独立门，不能把零 Claim 的正常返回包装成协作成功。

## 59. 2026-09-05 小内存 ECS 发布链路二次保护

- 当容器内旧 Vue development server 与一次全仓编译叠加后，ECS 再次出现“22/8080/9090 端口能建立 TCP，但 SSH banner、HTTP userspace 均无响应”的资源枯竭症状。2026-09-05 13:30 再测仍为 `Connection timed out during banner exchange`，Backend 健康返回 empty reply，前端 10 秒无响应；因此本轮 M7 后半段和 M8 候选只能标记为本地验证，不能伪称已经云端发布。
- 提交 `6de8cdb4` 已把发布链路改为本地执行 Vue production build，并用小型 Go static gateway 在云端提供静态文件与 `/api` 转发。服务器恢复后只上传预构建 Linux 二进制和前端 `dist`，不再在运行容器执行 `npm run serve`、`npm run build`、`go build` 或 `go test`。
- 遇到 banner timeout 时，发布脚本必须快速失败，不能高频重连、重复上传或启动更多进程。恢复顺序固定为：ECS 控制台重启或停掉资源消耗进程 → SSH 单次有界探测 → `free -m`/`docker ps`/唯一进程审计 → 原子部署预构建 Bundle → 四进程与 HTTP 健康门。不要删除 `gopherai2`，因为项目配置、上传文档和 MySQL 数据仍依赖该容器。

## 60. 2026-09-05 评测候选、人工复核与基线必须分层

- 320 条目录校验通过只表示文件数量、哈希、Case ID、Schema 和敏感信息扫描合格；全部 `pending_user` 时，页面必须显示“基线不可用”，不能把机械校验包装成人工事实正确性。
- 当前确定性 Scorecard 可执行 300 条；新增的 20 条 insufficient-evidence 需要统一 Runner + Groundedness/Judge 后才进入完整分数。LLM Judge 使用固定 Prompt/模型版本、temperature 0、严格 JSON、未知引用拒绝、失败最多重试一次且最终计为 `judge_failed`，绝不按中性分掩盖评测基础设施故障。
- 提交 `3b4d6733` 的基线门禁只接受人工复核、技术门通过、完成率至少 98% 的 Snapshot；Snapshot 锁定 dataset/fixture/model/prompt/judge/environment 版本且不可覆盖。候选既要满足绝对阈值，又不能相对同版本基线退化超过 5%；越权召回、危险动作等安全计数必须保持 0。版本不一致时拒绝比较，而不是生成看似精确但口径不同的百分比。

## 61. 2026-09-05 重启后只做预构建发布与串行评测

- ECS 重启后一分多钟时内存为 1612 MiB total、1204 MiB available，三个既有容器均为 `Exited (255)`，无遗留 Go/npm 编译进程。直接运行发布脚本会启动 `rabbitmq redis-vector gopherai2`，无需人工进入容器逐个启动，也绝不能删除项目容器。
- Release `20260905134535-c7530e6a5295` 的源码为 clean `add_eico@c7530e6a`，bundle SHA-256 `53b741766a09ae7af76675338f0431b5fa784d5e0f070b99c7bad97d598910ad`。Vue production assets 和六个 Linux/amd64 程序全部在 Windows 构建；服务器只校验、解压、原子切换和启动。Backend/Worker live+ready、MCP loopback 和前端静态网关均通过，运行进程中不再存在 `vue-cli-service serve`。
- 两个真实模型评测严格串行且各有 15 分钟硬超时。协作 A/B 13.9 秒完成，质量 `0.60→0.92`、提升 CI `[0.20,0.40]`、简单误触发/安全/预算违规均为 0；父子上下文 A/B 28.6 秒完成，引用与安全门通过，但质量不变且输入 Token 增加 44.62%，所以净收益失败并保持权重 0。这证明发布成功、技术门通过与候选值得晋级是三个不同结论。
- 前端公网 `GET /` 返回生产构建资产 `app.fa7a328f.js`；静态网关按 Vue 的 `/api/<path>` 约定转发到 Backend `/api/v1/<path>`。手工探测不能错误使用 `/api/v1/...`，否则网关会按设计拼成 `/api/v1/v1/...` 并得到 404。无 Token 请求 `/api/evaluations/catalog/latest` 返回业务码 `2006/无效的Token`，证明代理链已进入认证中间件。

## 62. 2026-09-05 Redis 投影精确对账与长面板可用性修复

- ECS 重启后页面一度出现父子上下文 `0 → 0` 候选和协作 KnowledgeAgent 证据不足。排查时不能只看 `DBSIZE` 或 MySQL 文档状态：MySQL 有 11 个当前有效 Child，而 Redis Search 有 20 个 Hash，其中 9 个来自旧版本/已删除版本。旧记录暂时未必立刻导致空结果，但版本继续累积后会占满固定 KNN TopK，再被 MySQL 权威过滤，形成“Redis 有数据、最终仍无证据”的隐患。
- `9f2cbd38` 先补了“缺失 Hash 重建”；`d87aa351` 又把启动恢复升级为精确投影对账：Index Worker 消费队列前用 `SCAN MATCH` 枚举固定命名空间、只保留 MySQL 当前有效 Chunk ID、批量删除陈旧 Hash，再执行存在性二次校验。扫描期间先收集后删除，避免边扫描边改变 keyspace；游标重复、返回类型异常或清理失败都会 fail-closed，Worker 不会错误宣告 ready。
- Release `20260905163113-426ba08b8e80`，bundle SHA-256 `6a536fe3b7e16ba8830d7b3e9ad493de6b253a7f0fa128ef9c9886635eb309e1`。Worker 启动日志记录 `expected=11 / present_before=11 / present_after=11 / pruned=9`；随后 Redis 命名空间 Key 与 RediSearch `num_docs` 均为 11。使用同一文档主体做真实鉴权 Smoke，父子上下文返回 `47 / 6 / gopher.jobs.dlx.v1`、候选 `10 → 5`；协作 Shadow 为 KnowledgeAgent 与 DiagnosticAgent 均 succeeded、整体 complete、3 个 Claim 和 6 个引用。
- `426ba08b` 将策略演算、受治理工具、三级记忆、评测总览和证据检索改为互斥的能力工作区，并为工作区设置独立纵向滚动和视口高度上限；聊天记录至少保留 120px，输入框固定保留布局空间。这样无需把浏览器缩放到不可读比例，长策略结果可在工作区内滚动，点击另一个能力按钮会自动收起前一个。
- 本轮全量 `go test -p 1 ./...`、`go vet ./...`、Vue lint 与 production build 全通过。生产构建资产为 `app.f8efac65.js`；Backend/Worker/MCP/Frontend 四进程及 live/ready 门全部通过。经验：服务端数据恢复、检索正确性与浏览器可访问性必须分别验收，不能用“发布健康”代替功能 Smoke，也不能用页面缩放掩盖布局溢出。

## 63. 2026-09-05 统一评测与双异常检测器发布

- `3194e4f7` 增加预构建 `GopherAI-eval-runner`。它在同一次运行中校验 Full 320 Manifest 与 5 份可执行源报告的 SHA-256，生成 JSON/Markdown 双报告，明确区分 `320/320` 目录校验、`300/320=93.75%` 执行覆盖、`300/300=100%` 完成率、技术门和人工复核门。Release `20260905174658-3194e4f7350f` 后，容器内 CLI 独立冒烟通过，得到 4 类脱敏失败聚类；API 不下发逐例问题与 Case ID。
- `92c32855` 增加固定阈值与滑动窗口 Z-score 双检测内核：最低 50 样本、30 点窗口、基线排除当前候选点、连续 2 点异常、Z 阈值 3.0，并用稳定 Incident Key 支持首次触发、重复抑制、恢复通知。检测只返回 `recommend_only` 的 `-10%` 候选权重建议，`Applied=false`，不写 active policy、不自动切流、不执行修复。
- 首次线上 UI Smoke 发现零方差基线的突变虽然正确告警，但不可定义的 Z 值显示为 `0.00`，容易误导。`c9d0719b` 增加显式 `zero_variance=true`，页面显示 `∞` 和 `zero_variance_adverse_shift` 原因码。最终 Release `20260905193258-c9d0719b67eb`，bundle SHA-256 `0af531cce58c16be7a531e3a08d3f7dfc2ba5d0a8908a827722e0de5fb6e3c5a`，四进程与所有依赖健康门通过；真实浏览器已验证健康窗口不告警、质量下降和延迟突增双检测、低样本抑制、零方差突变保护。
- 经验：Z-score 实现必须明确当前点是否进入基线，并单独处理零方差；不能用 JSON 的 `NaN/Inf`，也不能以 `0` 冒充有效 Z 值。验收 Fixture 必须显式标记 `deterministic_acceptance_fixture`，不得包装成生产 Prometheus 观测。M8-12 生产 recording rules、持久化事件、签名 Webhook 和 M8-17 Controller 接线仍未完成。

## 64. 2026-09-05 低样本不是健康：异常检测第三态与前端缓存复验

- 用户验收指出“低样本抑制”虽然返回了 `minimum_population_not_met`，页面却用绿色“未触发异常”、`Z=0.00` 和“基线点 0”展示。这会把“证据不足、不能判断”误读成“指标健康”。`5e31de0a` 为检测结果增加独立 `decision_status=insufficient_data`，并以契约测试锁定当前样本 `12`、最低门槛 `50`、两个检测器均 `suppressed`、不产生建议且 `Applied=false`。
- 页面将低样本改为中性第三态：显示“数据不足 · 暂不判定”、固定阈值/Z-score“已抑制 · 未参与判定”、Z 值“未计算”，以及“当前样本 / 最低门槛 12 / 50 · 尚差 38”。异常、健康、数据不足必须是互斥状态；不能继续用单个 `anomalous bool` 推导所有 UI 语义，也不能用数值 `0` 表达尚未计算。
- Release `20260905204908-5e31de0a0e78`，bundle SHA-256 `001645e8509ab8d75e91327719ae0e5e52def96fdc172861c01f69b9808738e3`，Backend/Worker live+ready、MCP 和前端静态网关门均通过。首次在已打开标签页点击仍看到旧文案，原因是标签页保留旧 JS 资源；重新导航/强制刷新后，真实页面已显示上述第三态。以后前端发布验收必须先 `Ctrl+F5` 或重新导航，再判断新版本是否生效；服务端健康通过不能证明浏览器已加载最新静态资源。

## 65. 2026-09-06 Prometheus 指标契约必须从真实 Collector 反射审计

- `8b23f725` 将 Backend 与 Index Worker 的 Collector 列表改为注册和目录发现共用的单一事实源，避免手写目录已经更新、运行时却没有指标的漂移。审计结果为 88 个业务指标族，SDD 核心契约 `46/46`、类型/标签不一致 0、重复名 0、高基数标签命中 0、保守序列上限 `25095/100000`；目录不通过时认证 API fail-closed 返回 503。
- 所有 46 个核心指标族都用固定 fallback 标签在首次事件前暴露，因此重启后立即可被 Prometheus 发现。生产 `/metrics` Smoke 实际看到 61 个已实例化的业务族，并逐一确认 TTFT、RAG Query、Agent Transition、Memory Recall、Online Eval、Strategy State 和 Webhook 指标存在；其余非核心 Vec 仍按真实事件惰性创建，不能把“注册 88”误写成“当前活跃 88”。
- RAG、Profile/Episodic Memory 与 Harness 生命周期复用现有业务调用点写入统一 SDD 指标；用户、租户、会话、请求、Trace、Run、Step、Call、文档/案例 ID、Prompt、Query、路径、URL、邮箱和 IP 均不得进入标签。Worker 的 status/error_code 也必须经过固定枚举，未知值统一收敛为 `unknown`。
- Release `20260905235345-8b23f725121d`，bundle SHA-256 `8bce738c532e23d78a25e2be68aede54f1f7b6af6667b7485b9b289ace28143d`，四进程与所有依赖健康门通过；真实浏览器显示 88 个目录定义、46/46 核心契约及九个指标域。M8-12 recording rules 尚未接线，因此当前仍不能声称检测器来自生产窗口。
- 从 PowerShell 拼接远程 Bash 且命令中包含 `$()`、`${name}` 或管道时，反斜杠不能可靠阻止 PowerShell 展开。应像部署脚本一样先把完整 Bash 片段 Base64 编码，再通过 SSH 解码执行；否则本地会把远端 `grep` 当成 PowerShell 命令。该失败只发生在只读 Smoke，未改变远程状态。

## 66. 2026-09-06 小内存 ECS 上线真实 Prometheus 与 Recording Rules

- `134fca77` 完成 M8-12：Prometheus 只在容器内 `127.0.0.1:9092` 监听，固定抓取 Backend `:9090/metrics` 与 Index Worker `:9091/metrics`。配置包含 4 个规则组、17 条 recording rules，覆盖请求成功率/P95、RAG 有依据回答率与空召回率、Agent/Tool 成功率和 P95、在线评测、反馈、Webhook 与控制动作；前端通过后端只读聚合 API 展示运行状态，不公开 Prometheus 管理面。
- 发布链路在停止旧版本前依次执行 `promtool check config`、`promtool check rules` 和 `promtool test rules`，任一步失败都不触碰线上进程。PromQL 测试最初用十进制 `0.8` 做精确相等断言，因浮点累计得到 `0.799999...`；固定 Fixture 改用可精确二进制表示的 `0.5` 后通过。规则单测应锁定语义和边界，避免以脆弱小数相等制造假失败。
- Ubuntu 包通过独立 bootstrap 一次性安装，`policy-rc.d` 阻止 apt 自动启动系统服务，避免与项目生命周期及端口发生冲突。项目自行管理 PID、日志和 TSDB，启动参数固定 `72h` 与 `128MB` 双保留上限；回滚到不含 Prometheus 配置的旧 Release 时跳过该进程，保持兼容。
- Release `20260906010721-134fca776d7d`，bundle SHA-256 `5cd350c0b2e74c1b21eaa066cf4357f5b66195e3740345482cac28f639cd1a23`。线上 2/2 targets up、4/4 groups、17/17 rules healthy、失败规则 0；`gopherai:scrape_target_up` 对 Backend/Worker 均为 1。Prometheus RSS 约 43 MiB，ECS 1612 MiB 总内存下仍有约 585 MiB available，TSDB 初始 64 KiB，资源预算可接受。
- 页面真实读取 Prometheus HTTP API，显示上述状态、规则版本 SHA 和 loopback/保留边界。该纵切只证明抓取与聚合规则正在生产运行，不证明业务窗口已经达到 50 个最少样本，也不意味着检测器已接入生产数据；下一步必须持久化带口径版本的窗口快照，再接 M8-13/M8-14。

## 67. 2026-09-06 生产指标窗口持久化与首事件基线

- `5261e7f3` 将两条首批受控生产 Scope 接入真实 Prometheus 窗口：`rag_deep` 的 15 分钟有依据回答率，以及 `diagnosis_collaborative` 的 5 分钟请求 P95。后台每分钟只执行服务端固定 allowlist 查询，把数值、样本量、规则版本/SHA、批次/快照 Hash 和采样状态原子写入 MySQL `metric_window_snapshots`，不同规则版本不混算，保留期固定 7 天；调用方不能提交任意 PromQL。
- 同一实现把固定阈值与 Z-score 检测器接到 MySQL 历史窗口。生产 API 与页面只读结果，明确区分 `observed/no_series/query_error` 和确定性验收 Fixture；单点只返回 `insufficient_window`，低样本只返回 `minimum_population_not_met`，所有建议保持 `recommend_only`、`Applied=false`，不写 active policy、不自动切流、不执行修复。
- 首次真实 Smoke 暴露 Prometheus Counter 的首事件陷阱：如果时序在第一次增量时才出现，`increase(counter[15m])` 没有零值基线，会把第一次请求算成 0。`373cd807` 在注册时预初始化所有受监控的 RAG strategy/status 组合，并为协作诊断请求与耗时建立零值基线；测试直接反射 Registry，锁定首次业务事件前精确标签组合已经存在。
- Release `20260906023245-373cd807d71c`，bundle SHA-256 `13e112f9a43f6a44b6cb466f65833ca5c3a3ab16a48ecd61f7deb13faf514ac5`，Prometheus 规则升级为 4 组 19 条，config/rules/unit-test 三重门和四进程健康门通过。真实公网页面执行一次复杂跨文档 `rag_deep` 后，回答得到 `47 / 6 / gopher.jobs.dlx.v1` 与两条引用；Prometheus 15 分钟 population 约为 1，下一次 MySQL 分钟采样显示 `100.0% / 1`，页面正确保持“数据不足 · 暂不判定”和“是否已修改线上策略：否”。
- 工程经验：生产窗口接线不能只测“第 N 次请求”；必须在进程重启后先证明零值时序存在，再发第一条真实请求，并依次核对业务结果、原始 Counter、recording rule、持久化快照和控制边界。Prometheus `increase` 会做区间外推，样本量可能是略高于整数的浮点数；当前决策层保守向下取整，避免把不足 50 的窗口误判为达到门槛。

## 68. 2026-09-06 签名 Webhook 的异步、幂等与模拟隔离

- `75027741` 完成 M8-15：生产异常先按稳定 Incident Key 在 MySQL 去重，支持 opened/updated/resolved、15 分钟更新冷却和连续两个健康窗口恢复；再写同库持久投递队列，由独立 Worker 异步发送。这里采用同库事务式 Outbox，而不是额外增加 RabbitMQ 双写，原因是告警与 Incident 状态需要原子提交，同时必须与聊天链路、RabbitMQ 短暂故障解耦。Dead Letter 保存在可审计队列表中，首版不允许自动重放。
- Webhook 签名为 HMAC-SHA256 v1，签名输入固定为 `timestamp + "." + body`。接收端同时校验 5 分钟时间窗、Header 中的 Event ID/type、严格固定 Schema、Payload SHA 和幂等回执；重定向关闭，最多 3 次尝试，429/5xx/网络超时可重试，其他 4xx 直接 Dead Letter。密钥只从 `/root/GopherAI_Runtime/control-webhook-secret` 读取，线上实测权限/长度为 `0600/64`，日志和 API 均不返回密钥内容。
- 显式“发送签名验收事件”只生成标记为 Simulation 的固定数据，但会真实经过 MySQL 队列、后台 Worker、HMAC HTTP 和接收端幂等回执。Release `20260906032732-f49cca0f6444`，bundle SHA-256 `3be00da9139a28c31c62a972d53ac5e5542f3e76f62630a3b363c82cfc4a6b03`；真实页面显示 `delivered / HTTP 200 / attempt 1/3 / receipt verified`、Dead Letter 0。随后 `/metrics` 中所有生产 `gopherai_webhook_deliveries_total` 仍为 0，证明 Simulation 不会伪造生产告警证据。loopback Receiver 只是 staging 验收夹具，不能包装为已经对接外部告警平台。
- 首次修复把空队列查询从 `First` 改为 `Find` 时误写了重复短变量声明，PowerShell 命令链又在 `go test` 失败后继续执行，导致一个不可编译的中间提交 `db068930` 被推送；`f49cca0f` 立即修复并重新执行测试、构建和云端发布。以后任何“验证后提交”命令都必须在每个关键步骤后显式检查 `$LASTEXITCODE` 并退出，不能依赖分号串联的最终退出码；空队列轮询用 `RowsAffected` 表达正常无任务，避免 GORM 每秒输出 `record not found` 和本地源码路径。

## 69. 2026-09-06 Recommend-only Controller 的不可变边界

- `f7ea078f` 上线首版只建议控制器。它每分钟只读取生产成熟异常窗口、MySQL 权威 active policy 和统一评测报告；样本、技术门、人工复核、正式基线、策略成员、健康 fallback、10% 单周期步长和 5% 探索下限全部在服务端 fail-closed。当前真实 320 候选尚未完成人工复核，所以生产输入即使异常也会写 `baseline_not_eligible`，不能绕过评测门直接改权重。
- 建议写入独立 `control_recommendations` 表而非 `routing_policies`。模型约束和仓库二次校验都要求 `Applied=false`，对外只有只读审计与显式验收 API，不存在激活 API。每条记录绑定 parent policy version/hash、eval run/report hash、detector/rule/evidence/candidate hash；Incident、父策略、报告和规则 SHA 组成稳定去重身份，持续异常不会每分钟制造重复建议。
- Release `20260906041602-f7ea078fe457`，bundle SHA-256 `b5780c43c2d5ed666b4f84fdf8b67b293f4dc46558d9babb0c08b663024cb480`，迁移、Prometheus 19 条规则、Backend/Worker/MCP/Frontend 和 2/2 targets 全部通过。真实浏览器的双门禁 Simulation 产生 1 条 blocked 与 1 条 recommended：合格 Fixture 只提出 `rag_fast 100%→90%`、`direct_fallback 0→10%`，两条均 `Applied=false`；验收前后 active policy 仍为 `routing-policy-v1`、Hash `696c55fd8da7…`。
- 生产周期日志连续返回 `examined=0/recommended=0/blocked=0`。验收完成后，MySQL 审计为 blocked/recommended 各 1 条且 applied 均为 0；`gopherai_control_actions_total` 所有组合仍为 0，`gopherai_control_loop_duration_seconds_count{result="no_anomaly"}` 为 35。Simulation 可以真实验证审计和护栏，但必须与生产指标隔离，不能用它证明线上发生过异常或自动优化。
- 浏览器能力工作区中的异常工作台与只建议控制器是嵌套 `details`。自动 Smoke 必须先滚动并展开外层“固定阈值 + Z-score 工作台”，再滚到内层控制器；外层闭合时，DOM 仍可能返回隐藏子节点的几何信息，直接按该坐标点击会命中后续面板。应先用 `elementsFromPoint` 或可见 DOM 校准实际命中元素，不能把“节点存在”误判为“用户可点击”。

## 70. 2026-09-06 三类 Observe-only 故障演练必须隔离生产副作用

- `cd5d82c0` 上线 M8-19。RAG 场景真实调用生产 Evidence Gate 并稳定得到 `RAG_NO_EVIDENCE`；Agent 场景使用生产 Parallel Executor 和 1ms 子任务超时得到 `AGENT_TASK_TIMEOUT`；工具场景使用生产 Tool Runtime、阻塞隔离 Adapter 和 10ms 运行时超时得到 `TOOL_TIMEOUT`。它们复用真实边界和错误码，但故障只存在于进程内测试 Adapter/Runner，不停止 Redis/RabbitMQ、不调用生产命令、不向真实工具或模型发送流量。
- 每个场景用确定性 30 点健康基线、两个注入点和两个恢复点驱动与生产相同的固定阈值及排除当前点 Z-score 检测器，报告固定为 before/injected/detected/recommendation/probe/recovered 六阶段。每阶段同步保留质量、成功率、P95/P99、Token、成本和样本数，避免只用一条 Z-score 或权重变化冒充闭环证据。
- Release `20260906052750-cd5d82c0a614`，bundle SHA-256 `726b583766dc18c3a51d66aeaa2a148e64dd9a4b01c67f54e4d4105f77da63d6`；迁移、19 条 Prometheus 规则、Backend/Worker/MCP/Frontend 和 2/2 targets 均通过。真实浏览器执行后得到检测 `3/3`、恢复识别 `3/3`、健康对照误报 `0/3`、平均 MTTD `60s`、只建议候选 `3`、实际策略变化 `0`，并显示报告 SHA-256 `cf17865241801e5427f54d9732ebf3270c6716ba1094c7189f6fa5dfa0752ec5`。
- MySQL 只新增一条 `simulation=1/applied=0` 的不可变演练报告；活动策略仍为 `routing-policy-v1`、Hash `696c55fd8da7…`，生产 `gopherai_control_actions_total` 所有 action/result 组合保持 0。Observe-only 没有执行降权，因此“缓解成功率”必须明确显示为 `not_measured_observe_only`，不能把检测成功或建议生成包装成线上收益。
- 新前端发布后，已打开标签页的普通刷新可能继续持有旧资产。真实验收应使用标签页导航到带 release 查询参数的同源 URL，或强制刷新，再从新的 DOM 确认入口出现。打开长面板时先展开外层异常工作台，再在 `.capability-workspace` 内滚动到故障演练；后台路由日志应同时出现 acceptance POST 和 latest GET，才能证明按钮确实调用了新版本服务。

## 71. 2026-09-06 小内存 ECS 部署 Grafana：实测门禁、插件隔离与回滚优先级

- M8-16 使用官方 Grafana OSS 13.2.1 包，包 SHA-256 为 `b4f088f661c5103746f23cb5cbbe2b2e18ba9870ad68a3c2a90451f511ded`。固定 Dashboard UID `gopherai-closed-loop-v1`，Dashboard SHA-256 `cf5437575b33423cf6807aa170e0e87202b38e4aa5e5deaf140f846ea178538b`，共 22 个面板，业务/质量/控制三组分别为 7/8/7；前端只展示认证后的脱敏运行摘要，不代理 Grafana 管理页面。
- Grafana 只监听容器 `127.0.0.1:9093`，`docker port gopherai2 9093/tcp` 明确返回未发布。进程以 `grafana` 用户运行；`grafana.ini`、datasource、dashboard provider 和 JSON 均为 `root:root 0444`，只有 data/logs/plugins 为 `grafana:grafana 0750`。管理口令和 signing secret 位于 `/root/GopherAI_Runtime` 的 `0600` 文件，不进入仓库、环境清单、日志或 API。
- 不能把桌面/大内存环境的经验值硬套到 1.6 GiB ECS。实测 Grafana 在 Dashboard 与插件初始化后会有短暂高峰，45 秒后才趋稳；最终发布门固定为启动 RSS ≤384 MiB、45 秒稳态 RSS ≤300 MiB、系统 `MemAvailable` ≥256 MiB。Release `20260906085226-70bd164d9b90` 中实测启动/稳态为 `324652/291884 KiB`，系统可用 `510072 KiB`；约两分钟后 Grafana RSS 进一步降到约 `182456 KiB`。发布包使用 `-trimpath -ldflags='-s -w'`，从约 177 MB 降至约 108.5 MB，降低上传和解压 I/O 峰值。
- 首次失败不是单纯“Grafana 内存大”：多个发布失败留下 Grafana 父进程及 bundled plugin 子进程，插件进程累积并与 journald/后端一起进入 I/O 等待，主机 load 升到约 20，SSH banner 与 Docker 命令均卡住。修复后显式禁止 plugin preinstall/update，禁用 Prometheus 以外的 bundled backend datasource，并在启动/回滚前清理匹配的插件子进程；发布后若发现未授权 bundled plugin 立即失败。恢复时只执行 `systemctl reboot --no-block`，没有删除或重建承载项目的容器与数据。
- 回滚必须优先恢复核心业务。可选 Grafana 启动失败时，回滚路径以 `skip_grafana=true` 恢复 Backend、Index Worker、Prometheus、MCP 与 Frontend，不能再次等待 Grafana 而让网页持续不可用。最终门复核为五个核心进程存在、Prometheus targets 2/2、Grafana API/搜索成功、公网页面 HTTP 200。
- 发布前增加远端容量门：`load1 <= max(4, cores*4)` 且 I/O PSI `full avg10 <= 10%` 才允许本地构建和上传。该门在服务器已经拥塞时 fail-fast，避免 100 MB 级传输和解压继续放大压力。它是部署安全门，不是业务健康指标。
- 随着部署 Bash 脚本增长，Base64 文本作为 `ssh.exe` 的单个命令参数触发 Windows“文件名或扩展名太长”，包虽然已上传但服务器没有切换。`70bd164d` 改为以 UTF-8 标准输入执行 `ssh ... bash -s`，彻底移除 Windows 参数长度依赖；容量预检也走同一路径。远程脚本、命令参数和业务密钥仍应分离，不能通过命令行展开秘密。
- 该历史容器的 PID 1 不负责回收孤儿，当前一次版本切换后可见 6 个旧 Backend/Worker/Prometheus/MCP/Frontend/Grafana 的 `Z` 状态条目，RSS 为 0，不是仍在运行的重复服务。不能对 zombie 反复 `kill -9`；应记录数量，并在达到安全阈值前使用 ECS 可用时段做受控 `docker restart gopherai2` 清理，再由发布脚本完整拉起 MySQL 和各服务。长期方案是保持现有容器数据不变的前提下评审 `--init`/supervisor 迁移，未经迁移验证不得直接删除容器。

## 72. 2026-09-06 受限 ECS 性能验收：真实路由、数据隔离与 pprof 口径

- `5c99322e` 删除含固定公网地址、明文账号及 50 VU 默认配置的旧 `gopherai-load.js`，改为预构建 `GopherAI-perf-eval`。Runner 只允许容器 loopback，硬限制总请求 `<=50`、并发 `<=5`、每个 Release 只能单实例执行；JWT 由服务端生成且 15 分钟过期，不写入报告。合成身份固定为 `__perf_probe__`，运行前后事务清理 Session/Message/AgentRun，同时在线评测采样显式排除该身份，避免性能样本污染生产质量与用户数据。
- Release `20260906150843-5c99322e8bcb`，bundle SHA-256 `4932d52bf489e5d077c0ef3d42817492ca2de8a7a00d5f9feea045ae04f33643`。2 vCPU、1.58 GiB ECS 上冷请求 `1/1` 成功，端到端/TTFT 为 `6137/6038ms`；热请求 `10/10` 成功、并发 2，端到端 P50/P95/P99 为 `934/1281/1361ms`，TTFT P50/P95/P99 为 `781/1169/1251ms`。报告必须同时披露实际路由 `legacy_chat · legacy-v0 · policy-v0` 与模型别名 `qwen-plus`，不能用命令行期望值冒充实际执行路径。
- 性能报告把进程模型调用与估算 Token Counter 做前后差分。本轮得到每百请求模型调用 100 次、估算 Token 11000；Token 来自统一估算器而非供应商账单，进程 Counter 在有并发真实流量时也可能混入其他请求，因此只能作为同口径工程基线，不得包装成精确成本结算。
- CPU/heap/goroutine 三份不可变 pprof 的 SHA-256 分别为 `cee4267828f46a6250347f51ddece3b40872912e60339ba56c7666b29dc69dfe`、`07b4807ed51ea6e057e5c1903175975ff3dffba3ac2b50c783280942998b6dc3`、`2718aea70d4cd856b5633208c1c9fbeef6005c37a3a74e933ca1c47b40a656d2`，报告 SHA 为 `5ab3a74db097bd390d7074caca2b03827b128d0312a4f6519cc249748d965c68`。CPU 5.09 秒采样只有 50ms 活跃样本，说明当前请求主要等待外部 I/O，不能据此宣称已定位或优化 CPU 热点；heap in-use 约 5.47 MiB，主要分配来自 PEM、flate、Prometheus、模板解析和 runtime。
- “冷”只表示当前 Release 启动后的第一条受控业务请求，不代表 ECS、Redis、MySQL、浏览器或模型连接全部冷启动；当前只覆盖 `legacy_chat`，不能外推为 RAG、多 Agent 或 Tool Runtime 性能。后续新增其他路径时必须按实际 route/model/strategy 分开报告，禁止混合计算百分位。
- 从 PowerShell 把多行 Bash 作为 SSH 参数发送时，CRLF 可能让最后一个 flag 变成 `5\r` 并在业务执行前失败。短脚本可先移除 `\r`、Base64 编码后作为单参数解码执行；长发布脚本仍优先使用部署器既有的 UTF-8 stdin `ssh ... bash -s`，避免 Windows 参数长度上限。容器内分析 pprof 时显式使用 `/usr/local/go/bin/go tool pprof`，不要假定非交互 shell 的 PATH 含 Go。独立 CLI 初始化 GORM 会输出大量迁移日志，后续 CLI 应在初始化数据库前切到 release 日志模式，但不能为了安静跳过必要 Schema 校验。

## 73. 2026-09-06 成对统计上线与 PowerShell 运行时边界

- `1da2772b` 把多 Agent 和父子 RAG 两份逐例报告接入统一成对统计层；只按同一 Case 的基线/候选成对比较，公开分子/分母、胜负平、固定种子 2000 次 paired bootstrap 95% CI、二分类不一致对与精确双侧 McNemar p 值。接口只返回汇总和源报告 Hash，不返回问题、答案或 Case ID；分析输入存在非法分数或不足两对时 fail-closed。
- Release `20260906155405-1da2772b1133`，bundle SHA-256 `e9d84ef2b31348cf7948c58d301ee77e8cd44ed13f8d0e03fe05e5d43b02083c`。真实浏览器显示多 Agent `0/10→8/10`、质量差 `+32%`、95% CI `[+20%,+40%]`、McNemar `p=0.0078`；父子 RAG `9/10→9/10`、差值/CI `0`、`p=1.0000`。Analysis SHA-256 为 `7a7d57888bf51b98b22b31fd6da458d9b5a0931d7ba6c36a36e6ed2977c95195`。前者是技术候选的统计收益，后者是没有净收益的负结果；两者均因人工标签未复核而保持 `PromotionEligible=false`。
- 部署器的长远程脚本通过 `.NET ProcessStartInfo.ArgumentList` 与标准输入发送，需要 PowerShell 7；误用 Windows PowerShell 5.1 会在远端容量预检之前因缺少 `ArgumentList` 属性失败，服务器不会上传或切换。本机应固定执行 `pwsh.exe -NoProfile -File scripts/deploy/deploy-aliyun.ps1`，不要用 `powershell.exe`。本次改用 `pwsh 7.6.5` 后完成本地交叉构建、上传、原子切换和全部健康门。
- Backend 比 Prometheus 更早启动时，第一次每分钟窗口采集可能短暂记录 `capture_failed`；本次下一周期开始持续恢复为 `warming/points=2`，Prometheus 2/2 targets 与控制器均正常。验收应检查后续周期是否恢复，不能把单次可观测启动瞬态隐瞒成无错误，也不能在后续仍失败时用“启动顺序”搪塞。

## 74. 2026-09-06 Judge 人工校准：技术完成不等于人工一致

- `ab7bca3d` 增加固定 30 条、6 切片 Judge 校准集和独立 Runner。部署脚本只预构建并安装二进制，不在每次发布中自动执行 30 次外部模型调用；Release 健康后再以 `timeout 1500 nice -n 10` 串行运行，避免模型抖动或费用让部署卡住。报告原子写入 `/root/GopherAI_Runtime/evaluation/judge-calibration-latest.json`，并绑定数据集、逐例和报告 SHA。
- 首轮真实运行得到 `28/30`，两条无证据正确拒答样本均在两次尝试后返回 `judge_output_invalid`。失败样本没有补零或伪装成中性分，技术门保持 false。原因是模型会把“正确拒答”写入 `supported_claims`，但输入没有合法 evidence ID；严格引用校验按设计拒绝该输出。
- `53ce6f69` 把 Prompt 升级为 `judge-rubric-v2`，在初始 Rubric 和重试修复消息中同时规定“evidence 为空时 supported_claims 必须为空数组”。Release `20260906172448-53ce6f69512b`，bundle SHA-256 `0e12e2b4cab339f21c83509de0f99d8798a3e779d100086788389c7d708dd70e`；复跑为 `30/30`、失败 0、技术门 true，模型 `qwen-turbo`，数据集 SHA `4bb9fc589628b04df3f2892def3c5bc6e7ffc51b17a5b98e64aa552d4d2e8662`，报告 SHA `0aed6cc5d95e0cf8c2a7f9a8e7f2bdf53b3ff7e5b4e02825fe3132c4dc8b5c5c`。
- 人工评分按登录用户哈希隔离，只允许 `0/0.25/0.5/0.75/1`，相同分数重复提交幂等，改分追加 revision；页面在首次提交前隐藏 Judge 分数，降低锚定偏差。只有 30 条全部人工复核且线性加权 Cohen's κ ≥ 0.70 才允许把 Judge 用于自动控制。目前进度仍为 `0/30`，因此 κ 未计算、自动控制禁止；同模型家族和单一复核人限制也必须披露，不能把 `30/30` 技术完成写成 Judge 已与人工对齐。

## 75. 2026-09-06 面试指标必须由证据聚合，而不是人工摘抄

- `8f5350a7` 增加可复现面试证据包。后端不信任页面拼接，而是读取当前 Release Manifest、Full 320、成对统计、ECS 性能和当前复核人 Judge 审计，逐项验证来源文件 SHA、报告自哈希、Release 关联和数值分子/分母后再产生 Claim；任一关键来源缺失或漂移都让整个请求 fail-closed。
- Release `20260906181448-8f5350a7436b`，bundle SHA-256 `20d24ac30109af32dcefd6d735b2b8986c57e41fa571ac67f0d23eaa1616a0b3`。真实浏览器得到 Package SHA 前缀 `362585a646067e3f…`、来源 Hash 全通过、简历可用 `1/5`。可用项是 2 vCPU ECS 上受限通用聊天热路径 `10/10`、并发 2、总延迟 P95 `1281ms`、TTFT P95 `1169ms`；其余 Claim 的人工复核、Judge κ 或净收益门未通过，页面与导出均保留阻塞原因和禁止夸大表述。
- 当前发布与历史性能报告属于不同 Release；聚合器必须同时校验“当前服务版本”和“测量发生时的历史版本”，不能要求历史性能报告伪装为当前提交，也不能把旧报告的运行环境隐去。登录用户的 Judge 人工进度是 reviewer-scoped 数据，允许改变包内容与 Package SHA，但不得把 principal hash 输出到 API。
- 证据包首版只有 5 个核心 Claim，因此只能称 M8-26 第一纵切。故障演练、恢复/取消、指标目录、Grafana 和只建议控制边界仍需后续接入，避免用一个下载按钮包装成整个 R6 已完成。

## 76. 2026-09-06 删除前先证明引用为零，已删除能力不要重复施工

- 用户已明确放弃的 ONNX 图片识别在 `a4bf5146` 中完成物理删除：运行时 image controller/service/router、ONNX helper、前端页面及相关入口一并移除。当前再次使用 `git ls-tree`、依赖清单和路由搜索确认没有模型、库或 UI 残留，因此不创建“为了体现删除而删除”的空改动。
- 评测集中的图片问题是负向范围测试：预期系统找不到证据并拒答，用来阻止未来把通用模型幻觉回答或新 MCP 换皮成图片能力。清理验收不能仅用关键词计数，还要区分运行能力、部署资产、文档历史、Grafana 的 `imageRenderer` 路径以及安全回归样本。
- 已删除能力的恢复点是 Git 历史与 Release 回滚，不应在当前树保留注释掉的实现、模型文件或隐藏前端入口。每次后续发布的全量 Go 测试、Vue build 和 Smoke 同时构成“删除后核心系统仍可运行”的持续证据。

## 77. 2026-09-06 把运行时证据纳入统一包：先持久化，再只读聚合

- `03659635` 把三类 Observe-only 故障、Agent 恢复/SSE 取消、指标目录、私网 Grafana 和只建议控制器加入证据包 v2。不能让 GET 证据接口暗中重跑故障：可靠性 POST 先运行隔离验收，再以临时文件、`fsync`、同目录 rename 原子写入 `/root/GopherAI_Runtime/evaluation/reliability-latest.json`；GET 和证据聚合都会复算报告自哈希并拒绝未知字段、尾随内容或门禁不一致。
- Release `20260906190841-1a9c257f9d95`，bundle SHA-256 `0ac257c48eece5a0488eda866c6d05700b8d8c629611efefcd6a0c6f2400b54b`。报告文件权限/大小为 `0640/1887 bytes`，文件 SHA-256 为 `d0d1aefeb15579dd098f981263d589a1f092c07fd6e453562c25025629ef9935`；报告内自哈希为 `396bb4ca116df0ab2b4454efda1d2e88ad36ac6c23a5872a5c5696c24a09f560`，两者口径不同：前者覆盖带缩进和自哈希字段的物理文件，后者覆盖清空自哈希字段后的规范 JSON。
- 真实页面在服务再次发布后无需重跑就恢复 `6/10` 证据，证明运行报告位于 Release 目录外且可跨版本读取。当前 Package SHA 前缀 `aac3cbb55f02a412…`；新增五条全部可用，但必须保留隔离/Observe-only/Applied=0 限制，质量类四条仍受人工复核或负收益阻断。
- 首次页面复核把 `0.099ms` 用整数格式显示成 `0ms`，虽然后端正确但会破坏可信度；`1a9c257f` 对小于 1ms 的指标保留三位，并同步修正“报告不持久化”的过时文案。指标展示格式也是证据契约的一部分，尤其不能把非零尾延迟显示为零。
- 远端最小容器没有 `jq`。只读运维检查应使用 `grep`、服务原始 JSON、Python（确认存在后）或把解析放在本地；不得在生产检查脚本中默认依赖未安装工具，也不应为了一个检查临时修改容器软件集合。

## 78. 2026-09-06 Harness Evolution 首切片：真实输入为空也是有效验收结果

- `5cc86ca3` 增加不可变 Harness Artifact/Review 谱系、确定性最小 Patch Proposer 和静态安全校验。候选只允许 Prompt Template、Context Policy、Diagnostic Playbook 的固定 JSON Pointer，操作必须是 `replace` 且 `variable_count=1`；生成阶段预算固定为模型调用 0、Token 0、一次确定性搜索。Artifact 内容寻址包含 parent、patch、来源、数据 split/hash、预算和回滚版本，重复点击只会命中同一个 ID。
- 输入不只校验 Hash 关联，还必须保持 Failure Pool 原始状态：`pending_human_review`、需要人工复核、Offline Gate 未通过、Isolation Canary 未通过、Applied=false。Evolver 没有 active pointer 模型或写接口，也不能表示源码、权限、安全策略、数据库迁移、Secret、部署和外部写入变更；Dataset 提案属于后续数据治理范围，不能被强行转换为 Harness Patch。
- Release `20260906195435-5cc86ca30110`，bundle SHA-256 `de17dcd76aafbc499850395cbcd91d24b4759e4f83a7749df1ba4db362fb2991`，全量 Go、定向 Race、Vue build、Prometheus 19 条规则、Grafana 和五个核心进程健康门通过。真实线上 Failure Pool 当时只有 `low_confidence_boundary_case` 与 `user_rejected_answer_case` 两个 Dataset 提案，所以物化结果是来源 2、新建 0、复用 0、安全跳过 2，Artifact/active/applied 均为 0。
- “没有生成候选”不能一律当成失败，也不能为了让演示好看而往生产失败池塞合成记录。正确做法是页面同时展示提案类别、跳过数和 reason code，并另用不落库、不污染生产指标的确定性验收覆盖 Prompt/Rule/Parameter 正例与越界反例。后续 M9-22 的 frozen split 与 M9-23 的公平 A/B 仍应使用版本化离线数据，不借真实用户流量凑样本。

## 79. 2026-09-06 Sealed Holdout 先做访问边界，再做一次性打开

- `e3e5ce4d` 用 Full 320 中 SHA 已冻结的 40 条诊断切片作为 Harness Evolution 专项数据源。每次读取先验证完整 Catalog 和诊断文件 SHA，再按带版本的稳定 Hash 排序固定切成 `20 evolution / 10 validation / 10 sealed holdout`；不是在每次实验中重新随机切分。集合 Hash 分别为 `02e214bd0407… / 3b39fd67adec… / 0f3fe5c8d017…`，覆盖 40/40、重复与交叉均为 0。
- 页面/API 只返回 split 名称、数量、集合承诺 Hash、用途与 Seal 状态，不返回 Case ID 或正文。候选搜索只允许 Evolution；Validation 只有候选冻结后可读；Holdout 只允许最终评测阶段且打开过后同一实验版本拒绝再开。当前没有 Holdout 打开 API，先把门禁和 8 项确定性验收落地，避免在 M9-23 评测器尚未实现时提前暴露最终集。
- Release `20260906204216-e3e5ce4de8c3`，bundle SHA-256 `117b1c9b0e03a1eebfd5d04b11b447f3956cf9f6526222d393d50b7faf7f6cbf`；真实浏览器显示 Source Hash 已核验、三分区 `20/10/10`、Overlap 0、Holdout 打开 0、开放 API 否，防泄漏验收 `8/8`。诊断集人工状态仍是 `pending_user`，因此后续即使技术 A/B 有提升也不能自动晋级。

## 80. 2026-09-06 公平 A/B 的价值也包括可信地拒绝候选

- `8b4b5a8a` 增加四方受控离线比较：旧 Harness、人工规则、同预算 test-time scaling、自动候选使用完全相同的 Evolution/Validation Case、每 Case 一次分析、0 模型调用、0 Token 和 0 估算成本；TTS 与自动候选公开标记一次反馈轮。报告继续复用既有 paired bootstrap 与精确双侧 McNemar，不允许用不同数据或隐藏预算制造优势。
- 自动候选由同一白名单 Proposer 从受控、脱敏、不落库的失败 Fixture 生成，内容为 `minimum_citations=2`，明确标记 `ProductionCandidate=false`。这能验证 lineage、预算、统计和拒绝链路，但不是生产 Failure Pool 候选，也不是 LLM Prompt 质量实验；页面必须原样披露这个限制。
- 真实生产运行中，Evolution 为 `97.6%→93.6%`、差值 `-4.0%`、95% CI `[-6.0%,-2.0%]`、McNemar `p=0.5000`；Validation 为 `97.9%→96.9%`、差值 `-1.0%`、95% CI `[-3.0%,0.0%]`、`p=1.0000`。人工规则和同预算 TTS 都与基线持平。Promotion 因上游收益、人工标签和 Fixture 身份被拒绝，Holdout 保持 `sealed/open_count=0`，generalization gap 不计算。
- Release `20260906214114-8b4b5a8a60c9`，bundle SHA-256 `3e5e05e86890bbc4d2028dae82955c922d0481b918412bef33607e84d941288b`；规范报告短 Hash 为 `6e3b405ac716`，物理文件 `/root/GopherAI_Runtime/evaluation/harness-evolution-comparison-latest.json` 权限/大小为 `0640/13540 bytes`，文件 SHA-256 为 `11a6b698e0fd3e3d37a8851b12a85d9a3bfd4fd74464eb0058ae81291044c0f5`。第二次点击保持相同报告 Hash 和 Holdout 0 次，证明相同实验被复用。
- 报告持久化校验不能只复算整体 SHA。还应重建受控候选并逐字段比较，重新推导 experiment identity，核对四方顺序/预算/成功率、成对比较的样本数和均值、CI/p 值边界、Promotion flags/reason codes 与 Holdout 状态。否则攻击者或错误代码可能在重算 SHA 后保存一份内部自相矛盾但“哈希正确”的报告。

## 81. 2026-09-06 人工 Gate 的权限初始化也必须 fail-closed

- `6e28d25c` 增加独立 `harness_reviewer` 角色和追加式 `harness_promotion_attempts`。普通 JWT 仍可查看脱敏审计，但只有 reviewer 可提交；请求必须绑定 experiment/candidate/report 三重身份、固定确认语和服务端哈希后的幂等键。相同载荷重试复用原记录，同键不同载荷返回冲突，旧报告绑定返回冲突。
- 当前受控候选是负收益且 `ProductionCandidate=false`。人工拒绝会形成 `recorded/candidate_no_measured_gain`；请求批准会形成 `blocked/controlled_fixture_not_promotable`。两者都明确 `active_pointer_changed=false`、`affects_live_traffic=false`，阻断尝试同样保留证据，不能静默丢弃后只展示“系统安全”。
- Release `20260906222549-6e28d25cc714`，bundle SHA-256 `a5b6e53eec43be84116bc942a91545aefb51732face00bd69e550eccb690c2e1`。生产浏览器连续执行“拒绝→批准→重复拒绝”后，页面与 MySQL 一致显示总尝试 `2`、有效决策 `1`、门禁阻断 `1`、活动指针 `0`；两条 Attempt SHA 前缀分别为 `7ffe1eb15cd0`、`ca1a3f277641`。
- 不能因为服务器是个人项目就把所有账号直接升级。第一次按“唯一有效账号”授权时发现有效账号 `4`，第二次按邮箱发现重复命中 `2`，两次脚本都在 UPDATE 前退出。最终用当前登录用户私有可见、且在 Session 表中只映射到一个 owner 的会话标题取得匿名 owner hash，再要求 User 表恰好匹配 1 条后授权；全程只输出计数。这类 bootstrap 也应具备前置计数、唯一性校验和事后复核。
- 给旧表增加角色字段时用 `NOT NULL DEFAULT user` 保持既有账号兼容；管理能力不要复用“已登录即管理员”。24A 只负责评审，尚未实现 Shadow/activate/rollback，因此数据模型和 UI 都保持 `human_gate_no_activation`，不能提前放出活动指针写接口。

## 82. 2026-09-06 先验证控制状态机，再开放真实活动指针

- `0b39a26c` 增加 `harness-pointer-cas-v1` 的确定性内存验收。未来活动指针切换必须同时满足离线收益门、独立人工批准、隔离 Shadow、安全回归门、候选 parent version/hash 与当前活动版本一致，以及调用方提供的 state version 未过期；任一条件不满足均 fail-closed。并发双请求使用同一 state version 时只允许一个成功，另一个必须得到冲突，而不是最后写入者静默覆盖。
- 回滚只恢复指针保存的直接父版本，成功后 state version 单调递增并消费 rollback slot；不允许在两个版本间反复 toggle。10 项验收覆盖四个前置门、父版本绑定、合法 CAS、过期 CAS、并发单赢家、成功回滚和二次回滚阻断，全部在隔离内存中完成，不访问生产 Pointer Repository。
- Release `20260906225400-0b39a26c515a`，bundle SHA-256 `3c4ac43f86517c21fc79368cad50b8f67ba98065328811b201ec276c93fe5158`。真实浏览器得到 `10/10`、生产写入 `0`、生产活动指针 `0`；报告自哈希为 `3fcd3d7cf1365ccfb4887830024c1c484450a44fb21519132a2a4048d9c8d5a0`。物理文件 `/root/GopherAI_Runtime/evaluation/harness-control-acceptance-latest.json` 权限/大小为 `0640/2293 bytes`，文件 SHA-256 为 `bf703b65cf93cc2ac4abc64ef75221037cc37ab8f46a658996c054a5b71a1973`。
- 页面用折叠子工作台明确写出“状态机语义已验证”与“当前负收益候选未进入 Shadow”两个不同事实。此切片仍不是生产激活能力：当前真实候选已被上游 Promotion Gate 拒绝，因此实际 Shadow admission、活动 Pointer Repository 和生产 rollback API 尚未执行；M9-24 继续保持进行中，不能把 `10/10` 内存验收宣传成线上策略已安全切换。

## 83. 2026-09-07 隔离 Shadow 指针必须与生产路由物理分域

- `d16548f9` 增加真实 `harness_control_events` 追加式审计和 `harness_active_pointers` CAS 仓储。指针固定 `scope=isolated_shadow`，模型和 API 都没有把它接入生产 `routing_policies` 的能力；因此即使未来合格候选成功激活，也只进入隔离执行域，`affects_live_traffic` 始终为 false。
- Shadow 准入依次验证生产候选身份、Evolution/Validation 正收益、安全门、sealed holdout 单次打开与 generalization gap、数据库中已记录的人工批准，再运行公开 12 项明细的 `isolated-contract-probe-v1`。候选 parent version/hash 与当前指针和 expected state version 必须一致；GORM 事务把指针 CAS 与成功事件写入绑定，失败和并发冲突不会留下半更新。紧急 rollback 不依赖历史报告可读，但仍要求独立 reviewer、显式确认、artifact type 和 expected state version。
- Release `20260906235752-d16548f912f6`，bundle SHA-256 `2ce7afc572d7cc2ba6f1a74029b9ba60d03ab01757750866d74e242363dec31d`。全量 Go/Vet/Race、Vue lint/build、Prometheus/Grafana 和五进程健康门通过。真实浏览器执行“请求当前候选 Shadow→重复请求→回滚”后，MySQL 为控制事件 `2`、执行 `0`、阻断 `2`、PointerChanged `0`、活动隔离指针 `0`；重复 Shadow 请求复用原 Event `b01570c1a9a6…`，回滚阻断 Event 为 `365202f5b5bf…`。
- 当前两条 reason 分别是 `controlled_fixture_not_promotable` 与 `rollback_unavailable`，生产路由仍为 `routing-policy-v1/696c55fd8da7…`。不能为了演示成功切换而插入合成生产指针；正向 CAS/rollback 由同一服务的合格候选测试和 24B 的并发验收覆盖，线上只展示与当前真实负收益候选一致的阻断结果。
- 使用 `.NET StandardInput.Write($text -replace "`r`n", "`n")` 时，PowerShell 可能把 `-replace` 的逗号解析为方法的第二个参数并选择格式化重载，若 Bash 含 `{}` 会报 `Input string was not in a correct format`。应先把 LF 规范化结果赋给独立变量，再调用单参数 `Write($normalized)`；这与此前长脚本使用 stdin 的结论一致。

## 84. 2026-09-07 面试证据包必须如实收录 Harness Evolution 负结果

- `98247e9a` 将证据包升级为 v3，新增 Harness lineage、三分区、四方公平比较、10 项控制状态机验收、人工 Promotion 审计和真实隔离 Shadow 控制六类来源。聚合时重新校验来源自哈希、40/40 分区覆盖、Holdout 打开次数、Promotion/Shadow 事件计数和 Pointer 状态；来源漂移或内部计数不一致都会 fail-closed，而不是只把页面已有数字重新排版。
- 当前受控候选在 Evolution/Validation 上分别为 `-4.0%/-1.0%`，并且 `ProductionCandidate=false`。因此新增 Claim 的状态是 `verified_negative_control_result`：展示 40/40 分区、Holdout 打开 0 次、CAS/rollback `10/10`、人工拒绝 1、批准阻断 1、Shadow 阻断 2、隔离活动指针 0；明确禁止表述“候选提升了质量”“Harness 已自进化成功”“已经打开 Holdout”或“已经影响线上路由”。可信地证明失败候选被拦截，本身就是控制系统的工程证据。
- Release `20260907002058-98247e9a0a55`，bundle SHA-256 `54643a3c1a840d4887f2fcf1747a0fef2c18dfefec76c365338eb71df4877e99`。真实认证页面生成 Package SHA 前缀 `b7898aca8bd20811…`，18 个来源全部通过 Hash 校验，证据项由 `6/10` 扩展为 `7/11`；新增第 11 项是可直接复现的负结果与治理边界，而不是虚构质量收益。Backend/Worker/MCP/Frontend、Prometheus `2/2` 与 Grafana 均通过发布健康门。
- 证据包中的 `ready` 不能简单等同于“所有结果都是正向收益”。可靠性、隔离控制和拒绝门的就绪标准是边界可复现、审计完整且线上零副作用；质量类 Claim 则仍必须满足人工复核和净收益门。面试展示应区分“正向业务指标”“技术候选”“负结果治理证据”三类结论。

## 85. 2026-09-07 清理必须由零调用证据驱动，运行边界不能误删

- M10 清理审计把 11 个候选统一成“源码探针 + Git 追踪清单 + Prometheus 24h 固定零调用窗口 + 替代链路”的可重复报告。最终真实页面为已删除并复核 `9`、明确保留边界 `2`、零外部引用候选 `0`、阻断 `0`，退役 Skill API 24h 调用 `0`、采样覆盖 `93.2%`；完成态必须独立表达，不能在候选清空后仍显示“计划尚未就绪”。
- 删除内容包括 ONNX/无场景 Skill 既有残留、重复 MCP client、旧 ToolSource、未注册网页工具、无消费者 `mcpBaseURL` 字段、误提交的 Backend/MCP 二进制和用户上传文件。`common/mcp` 的场景化协议宿主与 Skill 410 观测哨兵分别因真实 MCP Adapter 和退役期零调用审计而保留；保留运行边界不等于清理失败。
- 删除 Git 中 4 份误提交上传文件后，发布仍保留服务器运行目录中的 `14` 份文件，证明源码、发布物和运行数据已经分域。删除追踪文件时 `git rm` 已经把删除加入暂存区，不要再把已不存在路径传给同一次 `git add`；否则虽然删除仍可提交，命令会以 pathspec 错误结束并造成不必要的拆分提交。
- 清理后全量 Go、定向 Race、Vue lint/build、MCP 真实协议调用和云端健康门全部通过。真实 MCP Smoke 还发现严格发布清单解析遗漏新增的 `go_build_flags` 字段；修复方式是在 Backend 与 MCP 两端都显式加入允许字段并继续拒绝未知字段，而不是关闭 `DisallowUnknownFields`。严格 Schema 的价值包括把发布契约漂移尽早暴露出来。

## 86. 2026-09-07 可复现发布必须冻结 Git 字节，不只冻结 Git SHA

- 发布脚本改用 clean Git tree 的 `git archive` 作为源码唯一输入，只叠加本地预构建的 Linux 二进制、Vue `dist`、发布清单与源码清单；忽略文件、`node_modules`、`.git/.idea/.claude`、用户上传和私有配置均有压包后拒绝门。实际 bundle 从约 `118MB` 降到 `91.2MB`，当前正式包含 `691` 个条目；根目录和 MCP 二进制只由发布阶段生成，不再提交进源码。
- 首次发布暴露 Windows `core.autocrlf=true` 的隐蔽漂移：同一个 commit 经 `git archive` 导出的六个 JSONL 全由 LF 变成 CRLF，目录仍在但 SHA 全部失败，公网页面返回 422。Git SHA 只能标识对象，若导出阶段套用机器级换行策略，最终字节仍不可复现。
- `3aba2f04` 在归档命令上固定 `git -c core.autocrlf=false archive`，并在上传前重新验证六个 Slice 的 SHA、非空行数和 Catalog 总数 `320`。门禁是在实际待上传目录上执行，因此能覆盖工作区测试无法发现的打包转换。Release `20260907033715-3aba2f043f87`，bundle SHA-256 `b90976d401d5c69ca53c05b1d17168be806322bbe2e20f74bd44e19a059a5b1a`；真实页面恢复 `320/320`、六切片 Hash/Schema 全通过。
- 当前发布 Backend、Worker、Prometheus `2/2` targets、Grafana、MCP 和静态前端网关均通过。监控窗口启动后直接进入 `warming`，没有再次出现首轮 capture failure；MCP 发布证据 `18ms` 返回当前 release、完整 Git SHA 与构建 flags。经验：发布成功的定义应同时包含基础健康、数据契约、受治理工具和真实浏览器关键路径，而不是只有进程存活。

## 87. 2026-09-07 面试演示应使用单步导览，不应压缩整个工程工作台

- `32d4b874` 增加只读的 `3～5 分钟面试导览`、根目录架构 README 和中文追问卡。导览一次只展示场景/路由、证据 RAG、有限多 Agent、工具/记忆、评测闭环中的一个步骤；每步都包含现场操作、预期证据、主动披露边界和一个高频追问。它只读取当前 Release、Full 320、统一评测、清理审计和面试证据包，不执行工具、不修改策略、不替换正式聊天。
- Release `20260907043102-32d4b8747fde`，bundle SHA-256 `413ce49d1be68195066ddaac188dc065e948aecbdaabed810196af04684ff289`，可追溯条目 `694`。全量 Go/MCP 测试、Vue production build、Linux 交叉编译、Full 320 六切片精确 SHA、Prometheus `2/2`、Grafana 私网 Dashboard 和五进程健康门全部通过。
- 新 Release 会让上一版本清理报告失去版本绑定，因此首次读取证据包返回 503 是正确的 fail-closed。页面运行一次只读清理审计后，报告绑定当前 Git SHA，证据包恢复为 `8/12`、19 个来源 Hash 全通过；清理状态为 11/11 终态、9 删除、2 保留、0 阻断、旧入口 24h 调用 0、覆盖率 93.2%，Tracked Source 因新增 README/讲稿从 546 变为 548。
- 真实浏览器在 1280×720、设备缩放 100% 下确认导览面板宽 `950px`、工作区 `clientHeight=389/scrollHeight=748`、`overflow-y=auto`。页签能切到第 5 步，追问可展开，第 2 步能准确跳转“证据检索”并关闭导览。验收长工作台时应提供独立滚动容器、折叠或分页，不得要求用户把整个页面缩小到文字不可读。
- 本次启动第一次指标采集仍出现一次 `capture_failed`，下一分钟起持续恢复为 `warming/points=2`，未再次失败且无 panic；发布健康门通过不等于可以忽略异步后台任务，仍要在至少一个调度周期后复核日志。

## 88. 2026-09-07 最终发布复盘也必须由 Gate 计算，不能靠总结文字宣布完成

- `740abc05` 增加 `g10-release-review-v1`。服务先复算面试证据包 SHA，再把 12 个 Statement 稳定划分成 8 条通过技术证据门的事实与 4 条当前排除事实；每条可用事实保留原 Claim、SourceRefs 和全部禁止夸大限定。篡改 Claim 但沿用旧 Package SHA 会被拒绝，报告 GeneratedAt 取当前 Release Manifest，因此同一证据输入产生稳定 Report SHA。
- G10 的四个规格门没有被“代码完成”短路：当前产品总验收因 320 条人工标签与 Judge 0/30 阻断；简历事实确认保持 `pending_user`；生产回滚因单 ECS/单应用容器标记 `deferred_environment`；删除清单和恢复说明由清理证据通过。因此总结果是 `1/4`、`production_release_ready=false`，并单独延期数据库收缩迁移与百分比生产灰度。
- Release `20260907050533-740abc053bde`，bundle SHA-256 `bd6d0947e665fea6b4b7dfed4194032dc08424456776d3e97496f14b25d3492c`，可追溯条目 `696`。全量 Go、evaluation Race、Vue lint/build、Full 320 字节门和云端五进程健康门通过；新 Release 重跑只读清理审计后，G10 页面显示 Evidence SHA 前缀 `122adb08db78`、Report SHA 前缀 `a00dae5f5283`、4 个 Gate、8 个可用事实与 4 个排除事实，浏览器控制台错误为 0。
- G10 报告的职责是准确解释“为什么还不能毕业”，不是强迫所有卡片变绿。对于单实例项目，真实 5% 灰度和不影响唯一在线实例的回滚演练缺少诚实执行条件；应给出恢复触发条件并延期，而不是用内存 Fixture、原子目录切换机制或一次普通发布冒充生产演练。

## 89. 2026-09-07 Release 证据必须在声明 active 前自动绑定

- 新 Release 会使旧清理报告的 Release/Git 绑定失效；此前必须在页面人工补跑一次只读清理审计，面试证据和 G10 才从 fail-closed 恢复。这不是业务人工审批，而是可确定执行的发布后证据步骤，因此不应留给演示者记忆操作。
- `f3d24f31` 增加预构建 `GopherAI-cleanup-audit`。部署器在 Backend、Worker、Prometheus `2/2`、Grafana、MCP 和 Frontend 健康门全部通过后，用当前不可变 Release ID、完整 40 位 Git SHA、包内排序源码清单和固定 Prometheus 24h 查询生成报告；只有 `cleanup_complete=true / eligible=0 / blocked=0` 才原子落盘并打印 active。参数限制为路径安全 Release ID、完整 SHA 和最多 1 分钟超时，不开放新的部署 HTTP API；构建或保存失败会恢复上一目录。
- Release `20260907053001-f3d24f31db2f`，bundle SHA-256 `64d2e9460e973145f9b40ca5de5ac95bd8e7e3b4812ef1ee268356d6cc4c7a12`，700 个可追溯条目。部署输出直接得到报告 SHA `717e2b18f7d73442fe5d59b2f9154456ec15bbad572c6b2bf9674ceda60f06ee`、Tracked Source `552`、9 删除、2 保留、0 候选、0 阻断；未点击人工审计时，真实页面导览已经显示当前 Release、`320/320`、`8/12` 和 Hash 全通过。
- 真实联调发现“先开面试导览、再开评测总览”会复用导览预加载的 `evaluationCatalog`，从而错误跳过 G10/Judge/控制闭环等完整请求。`1ea14d62` 把“目录已加载”和“完整工作台已加载”拆成两个状态；只有 24 项工作台请求完成后才置完成标记。Release `20260907055009-1ea14d62f075`，bundle SHA-256 `2bbaa96a7faaa31c290f7424a9c116cca75c246d80d0f28c5f7d8bf8701d478f`，自动报告 SHA `dd613bcd4a80e3f04caec04eea1390337d96a4fd6cc37951122dfb59e1e9bb06`。浏览器按“导览→评测”顺序复验，G10 显示当前 Release、`1/4` 门、8 条技术事实、4 条排除事实和生产禁止，控制台错误为 0。
- 页面工作区共享数据时，不能拿某一个已填充的 Ref 代表整个聚合加载过程完成；应为不同加载层级保留独立状态，或对缺失资源做显式补载。否则预取优化会制造只在特定点击顺序出现的缺卡问题。

## 90. 2026-09-07 简历事实确认必须是用户动作，不能由发布流程代签

- `32feda25` 为 G10 增加 3～5 条简历事实人工确认，但发布、自动化测试和浏览器 Smoke 都不会替项目所有者提交。页面初始保持 `1/4`；真实浏览器验证选 2 条并确认限定语时按钮仍禁用，选 3 条后才启用，随后清空选择，全程没有产生确认记录。
- 服务端只接受当前 8 条技术证据中的 3～5 个唯一 ID、当前事实集合 SHA、固定确认语和 16～128 字符幂等键；JSON 限制 16KiB、拒绝未知字段和尾随内容。事实集合 Hash 覆盖 Claim、标题、来源和限定语；事实内容或边界变化时，旧确认不再通过当前门。
- 确认记录按登录主体隔离并追加写入 MySQL；主体和幂等键只保存 SHA-256，原值不落库。同一幂等键同一请求安全重放，不同请求返回冲突。有效确认只把简历事实门从 pending 改为 passed，使总计从 `1/4` 变为 `2/4`；它不会绕过 Full 320/Judge 人工门，也不会把单实例生产回滚标记为完成。
- Release `20260907062048-32feda25d200`，bundle SHA-256 `dd683bf806abf6838e46d45a5cf9fa10b80f1896f1ee5d1027e31205e053280a`，703 个可追溯条目；自动清理报告 SHA `014aeec73b58c3977a4696f2f7531e0db06e9d54bfbab5452e0469c7f1db3d3c`、Tracked Source `555`。全量 Go/Vet/Race、Vue lint/build、Linux 构建、Prometheus `2/2`、Grafana、MCP 与前端门通过，浏览器控制台错误为 0。
- 新增表是向后兼容的 expand migration：旧应用不会读取它，回滚应用版本不要求立即收缩表。当前单实例环境仍不具备安全执行 contract migration 和百分比生产灰度的条件，不能借新表上线宣称 M10-06～10 已完成。

## 91. 2026-09-07 Full 320 人工门必须先有可执行队列，再谈基线完成

- `54ac71dd` 增加按登录用户隔离的 Full 320 逐例复核队列。服务每次先验证 Catalog、六个 Slice 的实际 SHA/Schema/320 个唯一 ID，再按页返回单例输入、期望结果和 Case SHA；默认只展示 pending，可按六个切片与 pending/approved/rejected/reviewed/all 筛选，避免一次把 320 条堆进长页面。
- 每次提交必须绑定 Catalog SHA、Case SHA、当前 revision、固定确认语和 16～128 字符幂等键。通过只接受 `label_verified`；退回只接受五类白名单原因且不开放自由文本，避免把凭据或个人信息写入审计。记录按 revision 追加，CAS 防止两页覆盖，旧请求重放返回原收据但总进度始终按最新修订计算。
- 复核进度与 Git 中冻结的 Review Manifest 故意分开展示：当前用户队列为 `0/320`，Review Set SHA 是空集合 Hash；即使未来达到 `320/320 approved`，状态也只会变成 `ready_for_sealed_materialization`，不会自动改数据集、冻结基线、写 active policy 或声称双人独立标注。
- Release `20260907070048-54ac71dd9151`，bundle SHA-256 `9b4f25044d21402eab14dc71e7e4ce2f2823111bfa5362613bbfb769d5f84195`，710 个可追溯条目；自动清理报告 SHA `b9c3fc1c9662594624f557d9c76098cc2cfffaded60b3404827b01cc997b811a`、Tracked Source `561`。全量 Go、Vet、定向 Race、Vue lint/build、Prometheus `2/2`、Grafana、MCP 与前端门通过。
- 真实浏览器验证第 1→2 例翻页、RAG 切片 `60` 条过滤、`rag-v2-001` 内容、确认框前后按钮禁用/启用和控制台错误 `0`；随后取消确认，未提交任何标签。人工证据必须由实际复核者产生，自动化只能验证门禁，不能代做判断。
