export const evaluationCases = catalog => (catalog || []).filter(row => row.split === 'holdout')

// Recompute the public summary from the recorded rows. A stale or forged
// aggregate cannot make a failed run disappear from the denominator.
export function summarizeAgentReport(report) {
  const cases = Array.isArray(report?.cases) ? report.cases : []
  const metrics = {
    attempted: cases.length,
    completed: 0,
    execution_failed: 0,
    service_top1: 0,
    joint_correct: 0,
    evidence_valid: 0,
    model_calls: 0,
    tool_calls: 0,
    input_tokens: 0,
    output_tokens: 0,
    elapsed_ms_total: 0
  }
  for (const row of cases) {
    metrics.model_calls += Number(row.model_calls || 0)
    metrics.tool_calls += Number(row.tool_calls || 0)
    metrics.input_tokens += Number(row.input_tokens || 0)
    metrics.output_tokens += Number(row.output_tokens || 0)
    metrics.elapsed_ms_total += Number(row.elapsed_ms || 0)
    if (!row.valid) {
      metrics.execution_failed++
      continue
    }
    metrics.completed++
    if (row.score?.service_top1) metrics.service_top1++
    if (row.score?.joint_correct) metrics.joint_correct++
    if (row.evidence_valid) metrics.evidence_valid++
  }
  return { ...report, cases, metrics }
}
