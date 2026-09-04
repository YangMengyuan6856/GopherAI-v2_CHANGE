# GopherAI DevSupport evaluations

`devsupport-rag-core-v1.jsonl` is the first 20-case versioned RAG release slice required by M3-18. It covers exact facts, paraphrases, multi-fact questions, cross-section questions and tenant-isolation decoys. Its labels remain `pending_user` until a human reviews them; technical metrics may run before that, but the report cannot be frozen as an interview or release baseline.

The evaluator uses the production Dense + BM25 + RRF retriever, Evidence Gate, Citation Verifier and KnowledgeAgent, but writes its fixture to the dedicated Redis index `gopher:eval-rag-core-v1:v1:kb:chunks:idx`. It drops that exact index before and after the run, so evaluation documents cannot enter a user's knowledge base.

Run from Windows after the SSH deployment chain is available:

```powershell
powershell -ExecutionPolicy Bypass -File scripts/eval/run-rag-core.ps1
```

The script cross-builds the evaluator locally, uploads the binary and versioned inputs, runs the 20 cases inside `gopherai2`, and downloads both JSON and Markdown reports into `evals/reports/`. It never compiles the repository on the small cloud server.

M3 metrics are Recall@5, nDCG@5, MRR, deterministic citation precision, conservative evidence-reference coverage, unauthorized recall, answer resolution and error rate. Claim-level semantic citation coverage and LLM-as-a-Judge are intentionally left for M8; the report states this boundary so core proxy metrics are not presented as full AI-quality scores.
