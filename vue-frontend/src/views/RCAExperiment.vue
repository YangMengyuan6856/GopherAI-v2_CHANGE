<template>
  <main class="rca-page">
    <header class="hero panel">
      <div>
        <small class="eyebrow">RCAEVAL RE2-OB / KNOWN-FAULT EVALUATION</small>
        <h1>已知故障自主排查评测</h1>
        <p>给 Agent 一段匿名观测窗口，由模型自主选择指标、日志、调用链或历史案例工具，随着新证据更新判断，最终给出最可能的故障服务、故障类型和待确认项。</p>
      </div>
      <div class="boundary">
        <strong>只读 · 不执行修复</strong>
        <span>{{ catalog?.cases?.length || 36 }} 例：参考 / 开发 / 评测各 {{ splitCount('holdout') }} 例</span>
        <span>{{ catalog?.supported_services?.length || 4 }} 个根因服务 × 3 类已知故障</span>
        <span>单 Agent · 单并发 · 有界预算</span>
      </div>
    </header>

    <div v-if="error" role="alert" class="warning">{{ error }}</div>

    <section class="panel score-panel">
      <div class="heading">
        <div><small class="section-no">01 / FIXED EVALUATION</small><h2>固定评测结果</h2></div>
        <span class="status-chip">失败保留在分母</span>
      </div>
      <template v-if="agentReport">
        <div class="stats five">
          <article><strong>{{ reportMetrics.completed }} / {{ reportMetrics.attempted }}</strong><span>执行完成率</span></article>
          <article><strong>{{ reportMetrics.service_top1 }} / {{ reportMetrics.attempted }}</strong><span>故障服务 Top-1</span></article>
          <article><strong>{{ reportMetrics.joint_correct }} / {{ reportMetrics.attempted }}</strong><span>服务 + 类型联合正确</span></article>
          <article><strong>{{ reportMetrics.evidence_valid }} / {{ reportMetrics.attempted }}</strong><span>引用合同有效</span></article>
          <article><strong>{{ reportMetrics.execution_failed }}</strong><span>执行失败</span></article>
        </div>
        <div class="run-budget">
          <span>模型调用 <b>{{ reportMetrics.model_calls }}</b></span>
          <span>工具调用 <b>{{ reportMetrics.tool_calls }}</b></span>
          <span>Tokens <b>{{ count(reportMetrics.input_tokens + reportMetrics.output_tokens) }}</b></span>
          <span>平均耗时 <b>{{ averageElapsed }}s</b></span>
          <span>模型 <b>{{ agentReport.cases[0]?.model || 'unknown' }}</b></span>
        </div>
        <div class="audit-hashes mono muted">
          <span>DATA {{ agentReport.dataset_sha256?.slice(0, 12) }}</span>
          <span>PROMPT {{ agentReport.prompt_sha256?.slice(0, 12) }}</span>
          <span>AGENT {{ agentReport.implementation_sha256?.slice(0, 12) }}</span>
          <span>SPLIT {{ agentReport.split }}</span>
        </div>
        <div class="table-scroll">
          <table>
            <thead><tr><th>评测案例</th><th>执行</th><th>服务定位</th><th>服务 + 类型</th><th>证据合同</th><th>轨迹</th></tr></thead>
            <tbody>
              <tr v-for="row in agentReport.cases" :key="row.id">
                <td>{{ row.title }}</td>
                <td :class="row.valid ? 'ok' : 'bad'">{{ row.valid ? '完成' : row.stop_reason }}</td>
                <td :class="row.valid && row.score.service_top1 ? 'ok' : 'bad'">{{ row.valid && row.score.service_top1 ? '正确' : '未命中' }}</td>
                <td :class="row.valid && row.score.joint_correct ? 'ok' : 'bad'">{{ row.valid && row.score.joint_correct ? '正确' : '未命中' }}</td>
                <td :class="row.evidence_valid ? 'ok' : 'bad'">{{ row.evidence_valid ? '通过' : '未通过' }}</td>
                <td><button @click="viewRecorded(row.id)" :disabled="busy">查看记录</button></td>
              </tr>
            </tbody>
          </table>
        </div>
        <p class="muted">这是公开数据集上的固定、有限样本评测，不代表生产准确率。真值由 Agent 停止后的独立评分器读取，不进入模型上下文；每例只计一次，不挑选最好结果。</p>
      </template>
      <p v-else class="muted">{{ agentReportError || '正在读取固定评测报告…' }}</p>
    </section>

    <section class="panel">
      <div class="heading">
        <div><small class="section-no">02 / LIVE REPLAY</small><h2>选择一例现场演示</h2></div>
        <span v-if="catalog" class="mono muted">{{ catalog.agent_version }} · {{ catalog.dataset_sha256.slice(0, 16) }}</span>
      </div>
      <div class="control-row">
        <label class="case-select">评测观测窗口
          <select v-model="selected" :disabled="busy" @change="loadObservation">
            <option v-for="item in cases" :key="item.id" :value="item.id">{{ item.title }} · {{ item.id }}</option>
          </select>
        </label>
        <button class="primary" :disabled="busy || !observation" @click="run">
          {{ busy ? (loadingRecord ? '读取已保存轨迹…' : `Agent 排查中 · ${elapsed}s`) : '启动自主排查 Agent' }}
        </button>
      </div>
      <ol class="flow">
        <li><b>输入</b><span>匿名指标、日志与调用链窗口</span></li>
        <li><b>规划</b><span>模型按新证据自主选择下一只读工具</span></li>
        <li><b>治理</b><span>权限、参数、预算、引用和停止条件校验</span></li>
        <li><b>输出</b><span>候选根因、证据、不确定性和后续核查</span></li>
        <li><b>评分</b><span>运行结束后独立读取标准答案</span></li>
      </ol>
      <p v-if="busy && !loadingRecord" role="status" class="accent">正在调用真实云端模型，最长约 180 秒。离开页面会取消请求，不会回退成规则答案。</p>
    </section>

    <section v-if="observation" class="panel">
      <div class="heading">
        <div><small class="section-no">03 / OBSERVATION</small><h2>当前观测，不含标准答案</h2></div>
        <label>查看服务证据
          <select v-model="evidenceService"><option v-for="item in observation.services" :key="item.name" :value="item.name">{{ item.name }}</option></select>
        </label>
      </div>
      <p class="mono muted">{{ time(observation.start) }} → {{ time(observation.end) }}</p>
      <div class="stats">
        <article><strong>{{ count(observation.metric_rows) }}</strong><span>指标时间点</span></article>
        <article><strong>{{ count(observation.log_rows) }}</strong><span>日志行</span></article>
        <article><strong>{{ count(observation.trace_rows) }}</strong><span>Trace span</span></article>
        <article><strong>{{ observation.services.length }}</strong><span>可观测服务</span></article>
      </div>
      <div class="table-scroll">
        <table>
          <thead><tr><th>指标</th><th>参考窗口</th><th>当前窗口</th><th>变化倍数</th><th>12 桶趋势</th></tr></thead>
          <tbody><tr v-for="metric in metricRows" :key="metric.id"><td>{{ metric.column }}</td><td>{{ value(metric.reference) }}</td><td>{{ value(metric.current) }}</td><td :class="metric.ratio >= 2 ? 'accent' : ''">{{ metric.ratio.toFixed(2) }}×</td><td><svg viewBox="0 0 150 28"><polyline :points="spark(metric.sparkline)" /></svg></td></tr></tbody>
        </table>
      </div>
      <details v-if="service"><summary>日志与调用链摘要</summary><div class="two-columns"><div><h3>日志</h3><p>当前 {{ service.logs.current_count || 0 }} 条；错误关键词 {{ service.logs.keyword_matches || 0 }} 条。</p><pre v-for="(line, index) in service.logs.examples || []" :key="index">{{ line.timestamp }} {{ line.message }}</pre></div><div><h3>调用链</h3><p>P95：{{ value(service.traces.reference_p95) }} → {{ value(service.traces.current_p95) }}</p><p>当前 {{ service.traces.current_spans || 0 }} spans；非零状态 {{ service.traces.nonzero_status_count || 0 }}。</p><code>{{ service.traces.sample_operation || '无样例 operation' }}</code></div></div></details>
      <details><summary>数据边界与来源承诺</summary><p class="muted">服务/故障/重复次数编码、注入时间与标准答案均未进入该观测；原始 Parquet 只在本地预处理，ECS 仅保存有界摘要。</p><p v-for="source in observation.sources" :key="source.sha256" class="mono wrap">{{ source.name }} · {{ count(source.bytes) }} bytes · SHA256 {{ source.sha256 }}</p></details>
    </section>

    <section v-if="result" class="panel result-panel" aria-live="polite">
      <div class="heading"><div><small class="section-no">04 / AGENT TRACE</small><h2>{{ result.recorded ? '已保存的真实排查轨迹' : '本次真实排查轨迹' }}</h2></div><span :class="['status-chip', result.evaluation_valid ? 'ok-chip' : 'bad-chip']">{{ result.evaluation_valid ? '执行完成' : '执行未完成' }}</span></div>
      <p v-if="result.recorded" class="notice">记录时间 {{ result.recorded_at }}。这是固定评测时保存的调用，不是刚刚重新生成。</p>
      <p class="result-summary">{{ diagnosis.summary }}</p>
      <div class="run-budget"><span>模型 <b>{{ result.run.agent.model }}</b></span><span>停止原因 <b>{{ result.run.agent.stop_reason }}</b></span><span>模型调用 <b>{{ diagnosis.model_calls }}</b></span><span>工具调用 <b>{{ result.run.tool_calls.length }}</b></span><span>耗时 <b>{{ (result.run.elapsed_ms / 1000).toFixed(2) }}s</b></span></div>
      <div class="contract-line"><span :class="result.evidence_valid ? 'ok' : 'bad'">引用合同：{{ result.evidence_valid ? '通过' : '未通过' }}</span><span>输入 / 输出 {{ count(result.run.agent.input_tokens) }} / {{ count(result.run.agent.output_tokens) }} tokens</span><button @click="downloadRun">下载轨迹 JSON</button></div>
      <p v-if="!result.run.agent.completed" class="warning">本次模型错误、超时或预算停止被保留为失败，不会调用规则生成一个看似成功的答案。</p>

      <div class="timeline">
        <article v-for="step in result.run.agent.steps" :key="step.round" class="step-card">
          <div class="step-index">{{ String(step.round).padStart(2, '0') }}</div>
          <div class="step-body">
            <div class="heading"><h3>{{ step.decision?.action === 'finish' ? '提交最终候选' : '选择下一步检查' }}</h3><small class="mono">{{ step.model_ms }}ms · {{ step.validation }}</small></div>
            <p v-if="step.decision">{{ step.decision.update }}</p>
            <p v-if="step.decision?.tool" class="accent">{{ toolName(step.decision.tool.name) }} → {{ step.decision.tool.service }} · {{ step.tool_status }}</p>
            <details v-if="step.observation?.length"><summary>查看本轮返回的 {{ step.observation.length }} 项新证据</summary><pre v-for="evidence in step.observation" :key="evidence.id">{{ evidence.id }} · {{ evidence.service }}
{{ JSON.stringify(evidence.data, null, 2) }}</pre></details>
          </div>
        </article>
      </div>

      <article v-if="diagnosis.candidates.length" class="final-card">
        <h3>{{ diagnosis.candidates[0].service }} · {{ diagnosis.candidates[0].cause }}</h3>
        <div class="two-columns"><div><h4>引用证据</h4><ul><li v-for="evidence in diagnosis.candidates[0].evidence" :key="evidence.id">{{ evidence.statement }}<small class="evidence-ref">[{{ evidence.id }}]</small></li></ul></div><div><h4>不确定性</h4><ul><li v-for="item in diagnosis.candidates[0].differences" :key="item">{{ item }}</li></ul></div></div>
        <h4>建议继续确认（未执行）</h4><ul><li v-for="item in diagnosis.candidates[0].follow_ups" :key="item.check">{{ item.check }}</li></ul>
      </article>
      <p v-for="question in result.run.agent.questions" :key="question" class="amber">仍需补充：{{ question }}</p>

      <div class="answer-block"><button @click="reveal = !reveal">{{ reveal ? '收起独立评分' : '展开独立评分并核对答案' }}</button><div v-if="reveal" class="answer-detail"><p>标准答案：<strong>{{ result.score.answer.service }}</strong> / <strong>{{ faultName(result.score.answer.fault) }}</strong></p><p>故障服务 Top-1：<b :class="result.score.service_top1 ? 'ok' : 'bad'">{{ result.score.service_top1 ? '正确' : '错误' }}</b>；服务与类型联合：<b :class="result.score.joint_correct ? 'ok' : 'bad'">{{ result.score.joint_correct ? '正确' : '错误' }}</b></p><small class="muted">评分器在 Agent 完全停止后运行；答案文件未注册为工具，也没有拼入提示词。</small></div></div>
    </section>

    <section class="panel method-panel">
      <div class="heading"><div><small class="section-no">05 / EVALUATION CONTRACT</small><h2>这次评测究竟证明什么</h2></div><span class="mono">RCAEval / RE2-OB / MIT</span></div>
      <div class="contract-grid">
        <article><b>参考集 · repetition 1</b><p>{{ splitCount('reference') }} 个历史案例，为 history 工具提供过去的故障模式。</p></article>
        <article><b>开发集 · repetition 2</b><p>{{ splitCount('development') }} 个同配方不同运行，用于调试提示词、预算和输出合同。</p></article>
        <article><b>评测集 · repetition 3</b><p>冻结实现后顺序运行 {{ splitCount('holdout') }} 例，失败不剔除，由独立评分器统计。</p></article>
      </div>
      <p>它验证的是：系统能够在已知故障范围内，自主选择排查步骤，读取每轮新证据，利用历史案例作为先验，并输出可追踪的辅助排查结论。它不证明未知故障发现、自动修复成功率或生产环境因果确认。</p>
      <a href="https://huggingface.co/datasets/phamquiluan/RCAEval" target="_blank" rel="noopener noreferrer">公开数据集来源 ↗</a>
    </section>
  </main>
</template>

<script>
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import api from '../utils/api'
import { evaluationCases, summarizeAgentReport } from '../utils/rcaEvaluation.mjs'

export default {
  name: 'RCAExperiment',
  setup() {
    const catalog = ref(null), agentReport = ref(null), agentReportError = ref(''), error = ref('')
    const selected = ref(''), observation = ref(null), evidenceService = ref(''), result = ref(null)
    const reveal = ref(false), busy = ref(false), loadingRecord = ref(false), elapsed = ref(0)
    const endpoint = '/experiments/rca'
    const controller = new AbortController()
    const options = { timeout: 190000, signal: controller.signal }
    let timer = null

    const cases = computed(() => evaluationCases(catalog.value?.cases))
    const service = computed(() => observation.value?.services.find(item => item.name === evidenceService.value))
    const metricRows = computed(() => ['cpu', 'mem', 'latency-90', 'workload', 'socket'].map(key => service.value?.metrics[key]).filter(Boolean))
    const diagnosis = computed(() => result.value?.run?.diagnosis || { candidates: [], summary: '' })
    const reportMetrics = computed(() => agentReport.value?.metrics || {})
    const averageElapsed = computed(() => reportMetrics.value.attempted ? (reportMetrics.value.elapsed_ms_total / reportMetrics.value.attempted / 1000).toFixed(2) : '0.00')
    const splitCount = name => Number(catalog.value?.split_counts?.[name] || 0)

    const time = stamp => new Date(stamp * 1000).toLocaleString('zh-CN', { timeZone: 'UTC', hour12: false }) + ' UTC'
    const count = value => Number(value || 0).toLocaleString('zh-CN')
    const number = value => Number.isFinite(value) ? Number(value.toPrecision(5)).toLocaleString('zh-CN') : '缺失'
    const faultName = key => ({ cpu: 'CPU 压力', mem: '内存压力', delay: '网络延迟' }[key] || key)
    const toolName = key => ({ rca_inspect_overview: '查看全局概览', rca_inspect_metrics: '检查详细指标', rca_inspect_logs: '查询日志摘要', rca_inspect_traces: '查询调用链摘要', rca_inspect_history: '检索历史案例' }[key] || key)
    const spark = values => {
      if (!values?.length) return ''
      const low = Math.min(...values), high = Math.max(...values), scale = high - low || 1
      return values.map((item, index) => `${index * 146 / Math.max(1, values.length - 1) + 2},${26 - (item - low) / scale * 24}`).join(' ')
    }
    const preferredService = data => data?.services.find(item => catalog.value?.supported_services?.includes(item.name))?.name || data?.services[0]?.name || ''
    const loadObservation = async () => {
      if (!selected.value) return
      const id = selected.value
      result.value = null; reveal.value = false; observation.value = null; error.value = ''
      try {
        const response = await api.get(`${endpoint}/observations/${encodeURIComponent(id)}`, options)
        if (selected.value === id) { observation.value = response.data; evidenceService.value = preferredService(response.data) }
      } catch (requestError) {
        if (!controller.signal.aborted) error.value = requestError.response?.data?.message || '观测读取失败'
      }
    }
    const scrollToResult = async () => { await nextTick(); document.querySelector('.result-panel')?.scrollIntoView({ behavior: 'smooth', block: 'start' }) }
    const openCase = async id => { selected.value = id; await loadObservation() }
    const viewRecorded = async id => {
      if (busy.value) return
      busy.value = true; loadingRecord.value = true; error.value = ''
      try {
        await openCase(id)
        const response = await api.get(`${endpoint}/agent-report/${encodeURIComponent(id)}`, options)
        result.value = response.data
        evidenceService.value = diagnosis.value.candidates[0]?.service || preferredService(observation.value)
        await scrollToResult()
      } catch (requestError) {
        if (!controller.signal.aborted) error.value = requestError.response?.data?.message || '已保存轨迹读取失败'
      } finally { busy.value = false; loadingRecord.value = false }
    }
    const run = async () => {
      if (busy.value || !selected.value) return
      busy.value = true; error.value = ''; result.value = null; reveal.value = false; elapsed.value = 0
      timer = window.setInterval(() => { elapsed.value++ }, 1000)
      try {
        const response = await api.post(`${endpoint}/diagnose`, { case_id: selected.value, strategy: 'autonomous' }, options)
        result.value = response.data
        evidenceService.value = diagnosis.value.candidates[0]?.service || preferredService(observation.value)
        await scrollToResult()
      } catch (requestError) {
        if (!controller.signal.aborted) error.value = requestError.response?.data?.message || requestError.message || '自主排查失败'
      } finally { busy.value = false; window.clearInterval(timer) }
    }
    const downloadRun = () => {
      if (!result.value) return
      const url = URL.createObjectURL(new Blob([JSON.stringify(result.value, null, 2)], { type: 'application/json' }))
      const link = document.createElement('a'); link.href = url; link.download = `rca-${result.value.run.trace_id}.json`; link.click(); URL.revokeObjectURL(url)
    }

    onMounted(async () => {
      api.get(`${endpoint}/agent-report`, options).then(response => { agentReport.value = summarizeAgentReport(response.data) }).catch(requestError => { agentReportError.value = requestError.response?.data?.message || '固定评测报告暂不可用' })
      try {
        const response = await api.get(endpoint, options)
        catalog.value = response.data
        selected.value = cases.value[0]?.id || ''
        await loadObservation()
      } catch (requestError) {
        if (!controller.signal.aborted) error.value = requestError.response?.data?.message || '实验目录读取失败，请重新登录'
      }
    })
    onBeforeUnmount(() => { controller.abort(); window.clearInterval(timer) })

    return { catalog, agentReport, agentReportError, error, selected, observation, evidenceService, result, reveal, busy, loadingRecord, elapsed, cases, service, metricRows, diagnosis, reportMetrics, averageElapsed, splitCount, time, count, value: number, faultName, toolName, spark, loadObservation, viewRecorded, run, downloadRun }
  }
}
</script>

<style scoped>
.rca-page{height:100%;overflow-y:auto;padding:24px clamp(14px,3vw,38px) 44px;background:var(--g-bg);color:var(--g-text-primary);font-size:14px;line-height:1.65}.panel{background:var(--g-panel);border:1px solid var(--g-border);border-radius:2px;padding:22px;margin-bottom:18px}.hero{display:flex;justify-content:space-between;gap:30px;border-top:2px solid var(--g-primary)}.hero h1{font-size:29px;margin:7px 0}.hero p{max-width:900px;color:var(--g-text-secondary)}.eyebrow,.section-no,.mono{font-family:var(--g-font-mono);font-size:12px}.eyebrow,.section-no,.accent,a{color:var(--g-primary)}.boundary{display:grid;align-content:center;gap:6px;min-width:260px;padding-left:24px;border-left:1px solid var(--g-border);font:12px var(--g-font-mono);color:var(--g-text-secondary)}.boundary strong{color:var(--g-success);font-size:15px}.heading{display:flex;justify-content:space-between;align-items:center;gap:14px;flex-wrap:wrap;margin-bottom:14px}.heading h2{font-size:20px;margin:3px 0}.heading h3{font-size:15px;margin:0}.status-chip{padding:4px 9px;border:1px solid var(--g-border-strong);background:#0e1b24;font:11px var(--g-font-mono);color:var(--g-text-secondary)}.ok-chip{color:var(--g-success);border-color:#1f5d50}.bad-chip{color:#ef8b7b;border-color:#6a3933}.stats{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:10px;margin:16px 0}.stats.five{grid-template-columns:repeat(5,minmax(0,1fr))}.stats article{display:grid;gap:4px;padding:13px;border:1px solid var(--g-border);background:#101b25}.stats strong{font:21px var(--g-font-mono);color:var(--g-primary)}.stats span{font-size:12px;color:var(--g-text-secondary)}.run-budget{display:flex;flex-wrap:wrap;gap:8px 22px;padding:11px 13px;margin:12px 0;border-left:2px solid var(--g-primary);background:#0d1821;color:var(--g-text-secondary);font:12px var(--g-font-mono)}.run-budget b{color:var(--g-text-primary)}.table-scroll{overflow-x:auto}table{width:100%;border-collapse:collapse;text-align:left;font-size:12px}th{background:#101b25;color:var(--g-text-secondary);font-weight:500}td,th{padding:10px 12px;border-bottom:1px solid var(--g-border);white-space:nowrap}td{font-family:var(--g-font-mono)}.ok{color:var(--g-success)}.bad{color:#ef8b7b}.muted{color:var(--g-text-secondary);font-size:12px}.control-row{display:flex;align-items:flex-end;gap:12px;flex-wrap:wrap}.case-select{flex:1;min-width:280px}label{display:grid;gap:6px;color:var(--g-text-secondary);font-size:12px}select,button{min-height:38px;max-width:100%;padding:7px 12px;border:1px solid var(--g-border-strong);border-radius:2px;background:#101b25;color:var(--g-text-primary);font:inherit}button{cursor:pointer}button:hover,select:focus{border-color:var(--g-primary)}button:disabled,select:disabled{opacity:.45;cursor:not-allowed}button.primary{background:var(--g-primary);color:#061216;font-weight:700}.flow{display:grid;grid-template-columns:repeat(5,minmax(0,1fr));gap:1px;padding:0;margin:18px 0;background:var(--g-border)}.flow li{display:grid;gap:5px;padding:12px;background:#0e1821;list-style:none}.flow b{font:12px var(--g-font-mono);color:var(--g-primary)}.flow span{font-size:11px;color:var(--g-text-secondary)}svg{width:150px;height:28px;display:block}polyline{fill:none;stroke:var(--g-primary);stroke-width:1.8}.two-columns{display:grid;grid-template-columns:1fr 1fr;gap:22px}.two-columns>div{min-width:0}details{margin-top:15px;border-top:1px solid var(--g-border);padding-top:11px}summary{cursor:pointer;color:var(--g-primary);font-size:13px}pre{white-space:pre-wrap;overflow-wrap:anywhere;max-height:360px;overflow-y:auto;padding:10px;border:1px solid var(--g-border);background:#09131b;font:11px var(--g-font-mono)}code{font-family:var(--g-font-mono);overflow-wrap:anywhere}.wrap{overflow-wrap:anywhere}.result-panel{border-left:2px solid var(--g-primary)}.result-summary{font-size:17px}.notice,.warning{padding:13px;margin-bottom:15px;border:1px solid #715528;background:#282216;color:#e4b55d}.contract-line{display:flex;align-items:center;flex-wrap:wrap;gap:14px;margin:14px 0;color:var(--g-text-secondary);font:12px var(--g-font-mono)}.timeline{display:grid;gap:10px;margin-top:16px}.step-card{display:grid;grid-template-columns:48px 1fr;border:1px solid var(--g-border);background:#101b25}.step-index{display:grid;place-items:center;border-right:1px solid var(--g-border);color:var(--g-primary);font:15px var(--g-font-mono)}.step-body{min-width:0;padding:14px}.final-card{margin-top:17px;padding:17px;border:1px solid var(--g-border-strong);background:#0d1d25}.final-card h3{color:var(--g-primary)}h4{margin:12px 0 7px}ul{margin:0;padding-left:19px}li{margin-bottom:8px;color:var(--g-text-secondary)}.evidence-ref{display:block;color:var(--g-text-muted);font:10px var(--g-font-mono);overflow-wrap:anywhere}.amber{color:#e4b55d}.answer-block{margin-top:18px}.answer-detail{margin-top:10px;padding:14px;border:1px solid var(--g-border-strong);background:#14252b}.contract-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:10px}.contract-grid article{padding:14px;border:1px solid var(--g-border);background:#101b25}.contract-grid b{color:var(--g-primary)}.contract-grid p{margin-bottom:0;color:var(--g-text-secondary);font-size:12px}.method-panel a{text-decoration:none;font-family:var(--g-font-mono)}
.audit-hashes{display:flex;flex-wrap:wrap;gap:8px 18px;margin:-4px 0 12px}
@media(max-width:1100px){.stats.five{grid-template-columns:repeat(3,minmax(0,1fr))}.flow{grid-template-columns:repeat(3,minmax(0,1fr))}}@media(max-width:760px){.hero{flex-direction:column}.boundary{min-width:0;padding:12px 0 0;border-left:0;border-top:1px solid var(--g-border)}.stats,.stats.five,.flow,.two-columns,.contract-grid{grid-template-columns:1fr}.panel{padding:16px}.hero h1{font-size:23px}.step-card{grid-template-columns:36px 1fr}}
</style>
