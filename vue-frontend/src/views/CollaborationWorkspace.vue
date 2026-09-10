<template>
  <div class="collaboration-workspace">
    <header class="page-heading">
      <div><small>SUPERVISOR / KNOWLEDGE / DIAGNOSTIC</small><h1>动态多 Agent 协作</h1><p>模型拆分任务、选择执行顺序，并根据返回证据重新委派。不是固定的两路并行模板。</p></div>
      <router-link to="/dashboard/policy">策略演算与旧规则对照 →</router-link>
    </header>
    <div class="boundary">只读分析 · 不切换正式聊天 · 不连接服务器、不执行修复 · RCA 实验保持独立</div>
    <section class="request-panel">
      <label for="collaboration-request">描述问题和需要核对的项目资料</label>
      <textarea id="collaboration-request" v-model="message" :disabled="running" maxlength="4000" rows="4" placeholder="先根据部署手册核对 Redis 配置，再结合 HTTP 502 与 Redis NOAUTH 分析候选原因，并说明还缺少什么证据。"></textarea>
      <div class="examples"><span>示例：模拟故障描述，点击只填入、不自动运行</span><button v-for="example in examples" :key="example.label" :disabled="running" @click="message = example.text">{{ example.label }}</button></div>
      <div class="actions">
        <button class="primary" :disabled="running || !message.trim()" @click="start">{{ running ? `协作执行中 · ${elapsed} 秒` : '启动动态协作' }}</button>
        <button v-if="running" @click="cancel">取消本次请求</button>
        <button v-if="result" @click="downloadTrace">下载本次执行记录</button>
        <router-link to="/dashboard/knowledge">管理项目文档 →</router-link>
      </div>
      <p v-if="running" class="pending" role="status">正在实际调用模型与子 Agent，最多 180 秒。完成后展示真实逐轮记录；这里没有预设或模拟的执行步骤。</p>
      <p v-if="error" class="error" role="alert">{{ error }}</p>
    </section>
    <div class="role-grid">
      <article><small>01 / COORDINATOR</small><h2>Supervisor</h2><p>生成具体任务，决定串行或并行，选择交接证据、继续或停止。</p><span>最多 5 次调度模型调用</span></article>
      <article><small>02 / KNOWLEDGE</small><h2>KnowledgeAgent</h2><p>按委派问题检索当前账号的项目文档；证据门通过后生成带引用答案。</p><span>文档不是运行时状态</span></article>
      <article><small>03 / DIAGNOSTIC</small><h2>DiagnosticAgent</h2><p>结合用户报告、规则候选和交接证据进行模型分析，输出待验证假设。</p><span>建议核查 ≠ 已执行核查</span></article>
    </div>
    <template v-if="result && trace">
      <section class="run-summary">
        <div class="section-heading"><h2>{{ statusLabel(result.status) }}</h2><code>Trace {{ result.trace_id }}</code></div>
        <div class="stats"><div><strong>{{ trace.supervisor_calls }} / 5</strong><span>Supervisor 调用</span></div><div><strong>{{ trace.delegations }} / 4</strong><span>累计子任务委派</span></div><div><strong>{{ trace.usage.input_tokens + trace.usage.output_tokens }}</strong><span>已报告模型 Token（含子任务）</span></div><div><strong>{{ reasonLabel(trace.stop_reason) }}</strong><span>停止原因</span></div></div>
        <p>{{ trace.version }} · {{ trace.model }} · {{ trace.audit_storage === 'mysql_control_audit' ? '控制事件已写入 MySQL 审计' : trace.audit_storage === 'audit_failed' ? '审计失败，已停止执行' : '仅本次返回记录' }}</p>
        <p class="muted">子任务最多同轮并行 2 个；总计最多委派 4 次，不是 4 个不同 Agent。以下记录与结果来自本次运行，不是旧固定流程评测。</p>
      </section>
      <section class="timeline" aria-label="真实逐轮编排轨迹">
        <h2>委派与证据反馈</h2>
        <article v-for="step in trace.steps" :key="step.round" class="round">
          <div class="section-heading"><h3>第 {{ step.round }} 轮 · {{ step.decision?.action === 'finish' ? '停止与收束' : step.decision?.tasks?.length === 2 ? '并行委派两个子任务' : step.decision ? '委派一个子任务' : '调度未通过' }}</h3><span>{{ step.model_ms }} ms · {{ reasonLabel(step.validation) }}</span></div>
          <p>{{ step.decision?.update || '没有可执行的合法调度输出；未凭空执行任务。' }}</p>
          <div v-if="step.decision?.tasks?.length" class="task-grid">
            <article v-for="(task, index) in step.decision.tasks" :key="`${step.round}-${index}`" class="task">
              <h4>{{ task.agent }}</h4><p class="objective">{{ task.objective }}</p>
              <p class="handoff">接收前轮证据：{{ task.evidence_ids?.length ? task.evidence_ids.join(' · ') : '无（基于原始报告开展独立任务）' }}</p>
              <template v-if="step.tasks[index]">
                <div class="task-status">{{ statusLabel(step.tasks[index].status) }} · {{ step.tasks[index].duration_ms }} ms</div>
                <p>{{ step.tasks[index].output.summary || '该子任务未产生可采纳结果。' }}</p>
                <ul v-if="step.tasks[index].output.follow_ups?.length"><li v-for="question in step.tasks[index].output.follow_ups" :key="question">待确认：{{ question }}</li></ul>
                <details v-if="step.tasks[index].output.evidence?.length"><summary>查看返回证据（{{ step.tasks[index].output.evidence.length }}）</summary><article v-for="evidence in step.tasks[index].output.evidence" :key="evidence.id" class="evidence"><code>{{ evidence.id }}</code><span>{{ evidence.source_type }} · {{ evidence.title || evidence.source_id }}<template v-if="evidence.line_start"> · L{{ evidence.line_start }}–{{ evidence.line_end }}</template></span><p>{{ evidence.summary }}</p></article></details>
              </template>
              <p v-else class="muted">该委派未执行：{{ reasonLabel(step.validation) }}</p>
            </article>
          </div>
        </article>
      </section>
      <section v-if="result.synthesis" class="final-result">
        <h2>有来源的结果与待验证假设</h2>
        <p class="answer">{{ result.synthesis.unified_answer }}</p>
        <details v-if="result.synthesis.evidence?.length"><summary>核对最终引用与来源（{{ result.synthesis.citations.length }}）</summary><article v-for="citation in result.synthesis.citations" :key="citation.citation_id" class="evidence"><code>{{ citation.citation_id }} → {{ citation.evidence_id }}</code><span>{{ citation.source_type }} · {{ citation.source_id }}</span><p>{{ result.synthesis.evidence.find(e => e.id === citation.evidence_id)?.summary }}</p></article></details>
        <div v-if="trace.questions?.length" class="follow-ups"><h3>下一步需要你确认</h3><ul><li v-for="question in trace.questions" :key="question">{{ question }}</li></ul></div>
        <p class="boundary">程序检查引用来源，不裁定真实根因。证据不足、部分完成和预算停止均如实展示；没有执行任何修复。</p>
      </section>
    </template>
    <footer>本页只升级原 KnowledgeAgent + DiagnosticAgent。旧规则规划与 A/B 报告保留作历史对照；旧分数不代表本版本效果。</footer>
  </div>
</template>

<script>
import { computed, onBeforeUnmount, ref } from 'vue'
import api from '../utils/api'

export default {
  name: 'CollaborationWorkspace',
  setup() {
    const message = ref('演示场景：先从 m3b-config.json 核对 release.timeout_seconds 的值，再把查到的配置证据交给 DiagnosticAgent，结合后端 HTTP 502 和 context deadline exceeded 的模拟报告分析候选原因。请区分文档配置与实际请求超时，说明还需什么证据，不能声称已确认根因。')
    const result = ref(null)
    const running = ref(false)
    const error = ref('')
    const elapsed = ref(0)
    let controller = null
    let timer = null
    const trace = computed(() => result.value?.orchestration)
    const examples = [
      { label: '先查文档 → 再诊断', text: message.value },
      { label: '两个独立方向', text: '根据 m3b-config.json 核对 release.timeout_seconds；同时针对用户报告的 Redis NOAUTH 给出候选原因。这两个子任务互不依赖，可以并行处理。' },
      { label: '缺少现场信息', text: '系统有时不好用，但我没有日志，也不知道是哪一个服务，请判断目前需要我提供什么，不能凭空确定故障原因。' }
    ]
    const statusLabel = status => ({ complete: '协作已完成（候选仍需验证）', partial: '部分完成，保留有效结果', insufficient: '证据不足，需要补充信息', failed: '执行失败', cancelled: '已取消', succeeded: '子任务完成', timed_out: '子任务超时', budget_exceeded: '预算停止' }[status] || status)
    const reasonLabel = reason => ({ accepted: '校验通过', model_finished: '模型主动收束', round_budget: '调度轮数上限', context_budget: '上下文预算上限', repeated_delegation: '重复委派被阻止', no_new_evidence: '连续无新增证据', invalid_model_output: '模型输出不符合契约', model_error_or_timeout: '模型错误或超时', cancelled_or_timeout: '取消或总超时', audit_unavailable: '审计不可用', invalid_json_schema: 'JSON 契约不通过', unknown_handoff_evidence: '交接证据不存在', invalid_delegation_or_budget: '委派数量或预算不合法', agent_not_allowed_or_duplicate: '未注册或重复角色' }[reason] || reason)
    const start = async () => {
      if (running.value || !message.value.trim()) return
      running.value = true; error.value = ''; result.value = null; elapsed.value = 0
      controller = new AbortController()
      const started = Date.now()
      timer = setInterval(() => { elapsed.value = Math.floor((Date.now() - started) / 1000) }, 1000)
      try {
        const response = await api.post('/agent-runs/diagnostics/collaboration-shadow', { message: message.value.trim() }, { signal: controller.signal, timeout: 195000 })
        if (!response.data.orchestration) throw new Error('服务器仍返回旧固定协作版本，请等待部署完成后刷新。')
        result.value = response.data
      } catch (err) {
        error.value = err.code === 'ERR_CANCELED' ? '已取消请求。取消信号会传递到后端；中断请求不会伪造已完成结果。' : (err.response?.data?.message || err.message || '协作请求失败，请稍后重试。')
      } finally {
        clearInterval(timer); timer = null; running.value = false; controller = null
      }
    }
    const cancel = () => controller?.abort()
    const downloadTrace = () => {
      const url = URL.createObjectURL(new Blob([JSON.stringify(result.value, null, 2)], { type: 'application/json' }))
      const link = document.createElement('a'); link.href = url; link.download = `collaboration-${result.value.trace_id}.json`; link.click(); URL.revokeObjectURL(url)
    }
    onBeforeUnmount(() => { cancel(); clearInterval(timer) })
    return { message, result, running, error, elapsed, trace, examples, statusLabel, reasonLabel, start, cancel, downloadTrace }
  }
}
</script>

<style scoped>
.collaboration-workspace { height: 100%; overflow-y: auto; padding: 24px clamp(16px, 3vw, 40px) 48px; color: var(--g-text-primary, #d8e5ee); background: var(--g-bg, #0b131a); }
.page-heading, .section-heading, .actions, .examples { display: flex; align-items: center; gap: 14px; flex-wrap: wrap; }
.page-heading, .section-heading { justify-content: space-between; }
h1 { font-size: 26px; margin: 8px 0; } h2 { font-size: 19px; margin: 0 0 12px; } h3 { font-size: 17px; margin: 0; } h4 { font-size: 16px; color: #64d4df; margin: 0 0 12px; }
p, li { line-height: 1.75; overflow-wrap: anywhere; } small, code, .task-status { font-family: Consolas, monospace; }
small, a { color: #64d4df; } a { text-decoration: none; } a:hover { text-decoration: underline; }
.boundary { background: #13262a; border-left: 2px solid #38747d; padding: 12px 16px; margin: 18px 0; color: #aec9cb; }
.request-panel, .role-grid article, .run-summary, .round, .final-result { background: #121e28; border: 1px solid #243847; border-radius: 2px; padding: 22px; margin: 18px 0; }
label { display: block; margin-bottom: 12px; font-weight: 600; }
textarea { width: 100%; box-sizing: border-box; background: #0d1720; color: #e1eaf1; border: 1px solid #335163; border-radius: 2px; padding: 14px; font: inherit; line-height: 1.6; resize: vertical; }
textarea:focus { outline: 1px solid #36acbb; }
button { background: #162b35; color: #d8e6ee; border: 1px solid #345461; border-radius: 2px; padding: 10px 16px; font: inherit; cursor: pointer; }
button.primary { background: #2497a7; color: #07191e; font-weight: 700; } button:disabled { opacity: .5; cursor: not-allowed; }
.examples { margin: 12px 0 18px; color: #a2b8c8; font-size: 13px; }.examples button { padding: 6px 10px; }
.role-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 16px; }.role-grid article { margin: 0; }.role-grid h2 { margin-top: 12px; }.role-grid span, .muted, footer { color: #91a8b9; }
.stats { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 12px; }.stats>div { background: #182a35; border: 1px solid #2b414f; padding: 16px; }.stats strong, .stats span { display: block; }.stats strong { font-size: 21px; color: #c2edf0; overflow-wrap: anywhere; }.stats span { margin-top: 8px; color: #a0b6c7; font-size: 13px; }
.task-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 310px), 1fr)); gap: 16px; }.task { min-width: 0; padding: 18px; background: #0e1921; border: 1px solid #263e4a; }.objective { color: #eef5fa; }.handoff { color: #a7c5d4; font-size: 13px; }.task-status { color: #62c3b1; }
summary { cursor: pointer; color: #67c3d0; padding: 12px 0; }.evidence { border-top: 1px solid #29414d; margin-top: 10px; padding-top: 12px; }.evidence code, .evidence span { display: block; overflow-wrap: anywhere; }.evidence span { color: #98acbb; margin-top: 6px; }
.answer { white-space: pre-wrap; }.follow-ups { border-left: 2px solid #998a48; padding-left: 16px; margin-top: 20px; }.error { color: #f4ac9b; }.pending { color: #8bd3dd; }footer { margin-top: 22px; line-height: 1.8; }
@media (max-width: 960px) { .role-grid { grid-template-columns: 1fr; }.stats { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
@media (max-width: 540px) { .stats { grid-template-columns: 1fr; }.request-panel, .round { padding: 14px; } }
</style>
