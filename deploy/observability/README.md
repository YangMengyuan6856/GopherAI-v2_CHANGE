# GopherAI Prometheus runtime

This directory is the versioned source of truth for the M8-12 scrape and
recording-rule contract.

- `prometheus.yml` scrapes only the Backend and Index Worker over container
  loopback. Prometheus itself listens on `127.0.0.1:9092` and is not public.
- `recording-rules.yml` publishes 17 bounded aggregates over 5m, 10m, 15m,
  and 30m windows. Request, trace, run, user, tenant, document, and free-text
  dimensions are intentionally excluded.
- `recording-rules.test.yml` proves representative target, request, RAG, and
  online-evaluation calculations with deterministic input series.

The deployment script runs `promtool check config` and `promtool check rules`
before stopping the active release. Install the pinned Ubuntu runtime once with
`scripts/deploy/bootstrap-prometheus-aliyun.ps1`; deployment then owns the
process, PID file, readiness, target-health gate, 72-hour retention, and 128 MB
TSDB size ceiling.

Passing these checks proves scrape/rule health only. It does not prove that a
production window has enough samples for anomaly detection or policy advice.
