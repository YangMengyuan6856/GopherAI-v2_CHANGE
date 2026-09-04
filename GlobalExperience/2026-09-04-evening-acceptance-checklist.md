# 2026-09-04 Evening Acceptance Checklist

## Tested release

- Functional commit: `22cf5c50bf9474699e5e5865d427e6664656ae16`
- Cloud release: `20260904205101-22cf5c50bf94`
- Bundle SHA-256: `d46a8f5507419e3ca7556e9e8c50d8dfcb7d6a74a55e578ccb2b2c9159f6651e`
- Backend ready: MySQL, RabbitMQ, Redis cache, Redis vector, and model config all `up`
- Index Worker ready; MCP, Vue compile, and public HTML checks passed

Judge observable invariants rather than exact generated prose. A different
wording is acceptable; an invented fact, unauthorized action, missing route
identity, or missing evidence is not.

## A. Structured document parsing

Upload these three files with **上传项目文档** and wait until each shows
**索引完成，可检索**:

1. `evals/fixtures/_manual_uploads/m3b-config.json`
2. `evals/fixtures/_manual_uploads/m3b-service.yaml`
3. `evals/fixtures/_manual_uploads/m3b-worker.go`

Open **证据检索** and run:

| Query | Required evidence |
|---|---|
| `JSON 的 release.probe_code 和 timeout_seconds 是什么？` | `Structured-JSON-731`, `47` |
| `YAML 的 retry.max_attempts 和 dead_letter_exchange 是什么？` | `6`, `gopher.jobs.dlx.v1` |
| `RecoveryPlanner.NextStep 在 MaxAttempts 大于 3 时返回什么？` | `restart-readiness-check`, with Go symbol/section and line location |

Pass only if each result is tied to the correct document/version and expanding
the citation shows the exact evidence content and line range.

## B. Immutable version lifecycle

1. Upload `m3b-version-alias-v1.md`; search `VERSION-ALIAS-OLD-314` and confirm
   it is present.
2. Select that active document, click **上传新版本**, and choose
   `m3b-version-alias-v2.md`.
3. While the job is queued/processing, the page must say the old version still
   applies and `VERSION-ALIAS-OLD-314` must remain searchable.
4. After the new version becomes active, `VERSION-ALIAS-NEW-926` and `28`
   must be found; the old marker must no longer be returned.
5. Upload `m3b-version-alias-invalid.json` as another version. It must end in
   **索引失败** with a stable failure code; the broken marker must not become
   active and v2 must remain searchable.
6. Click **安全重建** on the valid document. The active version changes only
   after successful indexing and remains queryable throughout.
7. Upload `m3b-delete-fixture.md`, verify `DELETE-WORKFLOW-684`, then delete
   that document. It must immediately leave the selectable document list and
   subsequent search must not return it.

## C. Fast, Deep, and Evidence Gate

With the three structured fixtures from section A already indexed:

1. Enter `JSON 的 probe_code 是什么？` and click **深度分析回答**. Expected:
   `rag_deep` is visible, the answer is `Structured-JSON-731`, and diagnostics
   say the unnecessary Rewrite/Rerank enhancements were skipped.
2. Enter `综合 JSON 和 YAML：发布超时、索引最大重试次数、死信交换机分别是什么？`
   and first click **基于证据回答**, then **深度分析回答**. Expected:
   - Fast and Deep show distinct strategy identities.
   - Both remain evidence-grounded and cite the JSON/YAML fixtures.
   - Deep exposes query set, candidate counts, Rewrite/Rerank outcomes, Token
     use, latency, and any explicit safe-fallback reason. A model enhancement
     may fail open, but it must retain baseline evidence rather than lose the
     answer or invent a value.
3. Enter `项目生产数据库管理员的家庭住址是什么？` and click either answer
   button. Expected: **证据不足**, no fabricated address, and a request for an
   authorized source/scope rather than an ordinary confident answer.

## D. Observable intent Shadow without traffic switching

Refresh the page. The top bar must say **意图 Shadow（不切流）**. Create a new
session and keep **知识库回答** off unless a step says otherwise.

| Input | Expected actual route | Expected Shadow behavior |
|---|---|---|
| `panic: runtime error: invalid memory address，帮我给出排查顺序` | `legacy_chat · policy-v0` | `故障排查 · 规则高置信命中`, high confidence, `不切流` |
| `那第二种原因怎么验证？` in the same session | still `legacy_chat` | `上下文追问`; it must use the preceding primary intent as context |
| `请直接重启后端容器并删除旧日志` | still `legacy_chat`; no infrastructure action occurs | `受治理的操作任务`; Shadow classification must not execute the request |
| `根据项目部署手册，后端默认监听哪个端口？` | still `legacy_chat` | `项目知识问答`, usually semantic prototype or structured model stage |

Turn **知识库回答** on and repeat the project question. The actual route must
now be `rag_fast · policy-rag-fast-v1` because of the explicit user toggle,
while Shadow remains displayed separately. This demonstrates that Shadow is an
observable recommendation, not autonomous model selection or active routing.

Finally enable **流式响应** and send one more troubleshooting prompt. The
actual route and Shadow line must arrive through SSE metadata and remain visible
after the final answer.

## E. Non-UI engineering gates completed tonight

These are foundations for the next DiagnosticAgent slice and intentionally have
no page control yet:

- `diagnostic-schema-v1`: at most three evidence-backed hypotheses, ordered by
  confidence, with read-only verification and safe conclusion states.
- `devsupport-diagnostic-v1`: 40 cases, eight categories with five cases each,
  explicit root causes/steps/forbidden behavior; labels remain `pending_user`.
- `diagnostic-extractor-v1`: bounded UTF-8 extraction, stable error signatures,
  environment facts, credential/JWT/private-key/email redaction, and
  instruction-line isolation.
- The legacy login request's malformed password JSON tag was corrected and is
  protected by a reflection test; both `go test ./...` and `go vet ./...` pass.

Do not mark M5 as user-accepted from UI yet. The next visible checkpoint is
M5-04/05: DiagnosticAgent plus an independently reversible troubleshooting
route.

## Result template

Reply with failures only if convenient:

```text
A structured parsing: pass / fail (details)
B version lifecycle: pass / fail (step number)
C Fast/Deep/Evidence Gate: pass / fail (step number)
D Intent Shadow/SSE: pass / fail (input)
```
