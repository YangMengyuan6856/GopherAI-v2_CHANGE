# GopherAI DevSupport evaluations

## Diagnostic core dataset

`devsupport-diagnostic-v1.jsonl` contains 40 sanitized, project-specific
diagnostic cases. It is balanced across Redis, MySQL, HTTP/proxy, JWT/auth,
Docker networking, RabbitMQ/indexing, RAG/citation, and frontend/SSE. Each case
defines one to three acceptable root-cause candidates, two to four necessary
diagnostic steps, a read-only verification action, and explicit forbidden
claims/actions. Cases with insufficient evidence must require clarification.

All labels currently use `reviewed_by=pending_user`. The loader may prove the
schema, coverage, and sanitization gates, but the dataset is not a
human-reviewed baseline until every label is reviewed.

`devsupport-intent-v1.jsonl` is the M4 150-case intent candidate. It is exactly
balanced across `project_qa`, `troubleshooting`, `doc_task`, `tool_task`,
`follow_up`, and `general`, with 25 cases per class. It includes difficult,
compound, contextual follow-up, quoted-keyword, Prompt Injection, denied-write,
and out-of-scope demo cases. Boundaries and serious-misroute rules are frozen in
`intent-rubric-v1.md`. All labels remain `pending_user`, so this candidate is
not eligible to be called a reviewed or interview baseline yet.

`devsupport-rag-core-v1.jsonl` is the first 20-case versioned RAG release slice required by M3-18. It covers exact facts, paraphrases, multi-fact questions, cross-section questions and tenant-isolation decoys. Its labels remain `pending_user` until a human reviews them; technical metrics may run before that, but the report cannot be frozen as an interview or release baseline.

The evaluator uses the production Dense + BM25 + RRF retriever, Evidence Gate, Citation Verifier and KnowledgeAgent, but writes its fixture to the dedicated Redis index `gopher:eval-rag-core-v1:v1:kb:chunks:idx`. It drops that exact index before and after the run, so evaluation documents cannot enter a user's knowledge base.

Run from Windows after the SSH deployment chain is available:

```powershell
powershell -ExecutionPolicy Bypass -File scripts/eval/run-rag-core.ps1
```

The script cross-builds the evaluator locally, uploads the binary and current
v2 inputs, runs the 60 cases inside `gopherai2` with `GOMAXPROCS=1` and low
scheduling priority, and downloads both JSON and Markdown reports into
`evals/reports/`. It never compiles the repository on the small cloud server.

M3 metrics are Recall@5, nDCG@5, MRR, deterministic citation precision, conservative evidence-reference coverage, unauthorized recall, answer resolution and error rate. Citation precision scores citations attached to resolved factual answers; evidence listed by a controlled unresolved/safety response remains visible in the per-case trace and is penalized through citation coverage and resolved-answer rate instead of being counted as an asserted citation. Claim-level semantic citation coverage and LLM-as-a-Judge are intentionally left for M8; the report states this boundary so core proxy metrics are not presented as full AI-quality scores.

The first isolated cloud run for candidate `d6add7fee7de0fd05fbe0ff267bfa8a54e46a6e3` is checked in under `evals/reports/`. It passed the technical M3 gate, but `baseline_eligible` remains false until the dataset's `pending_user` labels are explicitly reviewed.

`devsupport-rag-core-v2.jsonl` is the complete M3-28 slice. It contains 60
cases: 50 answerable cases and 10 explicit no-evidence/safety cases. Beyond the
v1 exact and paraphrase coverage, v2 adds cross-document reasoning, immutable
version conflicts, structured JSON/YAML/Go behavior, bounded `rag_deep`,
observability feedback-loop semantics, deletion/rebuild consistency, prompt
injection, credentials, privacy, and tenant-isolation negatives. Its isolated
fixture is `fixtures/kb-fixture-v2.json`.

V2 makes `expected.should_resolve` mandatory. Retrieval metrics and citation
coverage use only answerable cases; no-evidence cases instead contribute to
Evidence Gate Precision, No-evidence Safe Rate, and Unsupported Answer Rate.
This avoids the old statistical error of treating a correct refusal as missed
retrieval. Reports also record search, answer, and end-to-end P95 latency. The
8-second Fast-path P95 remains a G3 observation until a real cloud report and
bottleneck evidence are available; it is not fabricated from unit tests.

Both v1 and v2 labels remain `pending_user`. A passing technical report is not
eligible to become an interview baseline until the corresponding labels are
reviewed and changed to `human`.

## Profile-memory safety dataset

`devsupport-memory-v1.jsonl` is a deterministic 20-case contract suite for the
user-governed profile-memory path. It contains four cases each for relevant
recall, stale/wrong-value exclusion, deletion effectiveness, cross-principal
isolation, and context-budget enforcement. The evaluator reuses the production
profile selector and context assembler so ranking and budget regressions are
tested at the same boundary used by chat requests.

Run it locally with:

```powershell
go run ./cmd/memory-eval
```

The candidate report is written to
`evals/results/devsupport-memory-v1-candidate.json`. Technical gates require at
least 90% relevant recall, at most 5% stale/wrong injection, zero deleted-memory
recall, zero cross-principal leakage, 100% context-budget compliance, and 100%
deterministic replay. This is a deterministic contract suite rather than a real
database fault-injection or semantic-vector benchmark. All labels are still
`reviewed_by=pending_user`, so even a passing report has
`baseline_eligible=false` until a human reviews the dataset.
