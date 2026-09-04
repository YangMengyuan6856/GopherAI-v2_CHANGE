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
