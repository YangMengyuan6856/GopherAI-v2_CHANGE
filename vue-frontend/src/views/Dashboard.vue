<template>
  <div class="dashboard-page">
    <section class="dashboard-hero">
      <div>
        <span class="eyebrow">AGENT OPERATIONS WORKSPACE</span>
        <h1>研发与运维智能工作台</h1>
        <p>聊天、知识检索、诊断、工具治理、记忆与评测各自拥有独立工作区；所有高风险能力仍受显式边界和审计约束。</p>
      </div>
      <div class="hero-state">
        <span>SYSTEM STATE</span>
        <strong><i></i> OPERATIONAL</strong>
        <small>ROUTING POLICY · SHADOW SAFE</small>
      </div>
    </section>

    <section class="signal-grid" aria-label="系统概览">
      <article><small>CAPABILITY DOMAINS</small><strong>08</strong><span>独立能力工作区</span></article>
      <article><small>TOOL GOVERNANCE</small><strong>10</strong><span>统一治理检查点</span></article>
      <article><small>EVAL CATALOG</small><strong>320</strong><span>固定契约回归用例</span></article>
      <article><small>POLICY MODE</small><strong>SAFE</strong><span>Shadow / Recommend-only</span></article>
    </section>

    <div class="section-heading">
      <div><span>01</span><strong>能力控制台</strong></div>
      <small>SELECT MODULE / ROUTE ISOLATION ENABLED</small>
    </div>

    <section class="module-grid">
      <router-link v-for="module in modules" :key="module.path" :to="module.path" class="module-card">
        <div class="module-code">{{ module.code }}</div>
        <el-icon><component :is="module.icon" /></el-icon>
        <div class="module-copy">
          <small>{{ module.english }}</small>
          <h2>{{ module.title }}</h2>
          <p>{{ module.description }}</p>
        </div>
        <div class="module-footer">
          <span>{{ module.badge }}</span>
          <b>OPEN →</b>
        </div>
      </router-link>
    </section>
  </div>
</template>

<script>
import {
  ChatDotRound, Checked, Connection, Cpu, DataAnalysis,
  Document, Guide, Monitor, Operation
} from '@element-plus/icons-vue'

export default {
  name: 'DashboardView',
  setup() {
    const modules = [
      { code: 'M-01', path: '/dashboard/chat?new=1', title: '新聊天', english: 'UNIFIED CHAT', description: '统一对话入口，展示实际路由与意图 Shadow，支持流式回答。', badge: 'ONLINE', icon: ChatDotRound },
      { code: 'M-02', path: '/dashboard/knowledge', title: '证据检索', english: 'RAG EVIDENCE', description: '文档版本管理、混合检索、父子上下文与有依据回答。', badge: 'RAG', icon: Document },
      { code: 'M-03', path: '/dashboard/diagnostics', title: '故障诊断', english: 'DIAGNOSTIC HARNESS', description: '可暂停、可恢复、有预算的只读排障 Agent 工作流。', badge: 'AGENT', icon: Monitor },
      { code: 'M-04', path: '/dashboard/memory', title: '三级记忆', english: 'CONTEXT MEMORY', description: 'Working、Episodic 与 Profile 的来源、冲突和修正控制台。', badge: 'CONTEXT', icon: Connection },
      { code: 'M-05', path: '/dashboard/tools', title: '受治理工具', english: 'TOOL RUNTIME', description: '工具注册、参数校验、权限、预算、熔断与审计一体化。', badge: 'GOVERNED', icon: Cpu },
      { code: 'M-06', path: '/dashboard/policy', title: '策略演算', english: 'POLICY SIMULATOR', description: '固定分桶、多 Agent 规划门与案例增强的 Shadow 预演。', badge: 'SHADOW', icon: Operation },
      { code: 'M-07', path: '/dashboard/evaluation', title: '评测与闭环', english: 'EVALUATION LOOP', description: '离线评测、异常检测、反馈池和 Recommend-only 控制器。', badge: 'OBSERVE', icon: DataAnalysis },
      { code: 'M-08', path: '/dashboard/review', title: '人工验收', english: 'HUMAN GATE', description: 'Full 320 标签复核与 Judge 30 人工校准的独立闸门。', badge: 'REVIEW', icon: Checked },
      { code: 'M-09', path: '/dashboard/interview', title: '面试导览', english: 'LIVE WALKTHROUGH', description: '用 3–5 分钟串联场景、证据、Agent、治理与反馈闭环。', badge: 'GUIDE', icon: Guide }
    ]
    return { modules }
  }
}
</script>

<style scoped>
.dashboard-page {
  height: 100%;
  overflow-y: auto;
  padding: 28px clamp(18px, 3vw, 42px) 48px;
  background:
    linear-gradient(rgba(0, 229, 255, .025) 1px, transparent 1px),
    linear-gradient(90deg, rgba(0, 229, 255, .025) 1px, transparent 1px),
    var(--g-bg);
  background-size: 32px 32px;
}

.dashboard-hero {
  min-height: 170px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 32px;
  padding: 28px 30px;
  border: 1px solid var(--g-border-strong);
  border-radius: 2px;
  background: linear-gradient(110deg, rgba(0, 229, 255, .07), transparent 52%), var(--g-panel);
  box-shadow: inset 3px 0 0 var(--g-primary);
}

.eyebrow, .hero-state, .section-heading small, .module-code, .module-copy small, .module-footer {
  font-family: var(--g-font-mono);
  letter-spacing: .08em;
}

.eyebrow { color: var(--g-primary); font-size: 11px; }
.dashboard-hero h1 { margin: 10px 0 8px; font-size: clamp(25px, 3vw, 38px); }
.dashboard-hero p { max-width: 760px; color: var(--g-text-secondary); line-height: 1.75; }

.hero-state {
  min-width: 220px;
  display: grid;
  gap: 8px;
  padding-left: 20px;
  border-left: 1px solid var(--g-border);
  color: var(--g-text-muted);
  font-size: 10px;
}

.hero-state strong { color: var(--g-success); font-size: 15px; }
.hero-state i { display: inline-block; width: 7px; height: 7px; margin-right: 7px; border-radius: 50%; background: var(--g-success); box-shadow: 0 0 8px var(--g-success); }

.signal-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 10px;
  margin: 14px 0 30px;
}

.signal-grid article {
  min-height: 106px;
  display: grid;
  align-content: center;
  gap: 4px;
  padding: 16px 18px;
  border: 1px solid var(--g-border);
  border-radius: 2px;
  background: var(--g-panel);
}

.signal-grid small { color: var(--g-text-muted); font: 10px var(--g-font-mono); letter-spacing: .06em; }
.signal-grid strong { color: var(--g-primary); font: 26px var(--g-font-mono); }
.signal-grid span { color: var(--g-text-secondary); font-size: 12px; }

.section-heading { display: flex; align-items: center; justify-content: space-between; margin-bottom: 12px; }
.section-heading div { display: flex; gap: 10px; align-items: center; }
.section-heading div span { color: var(--g-primary); font: 12px var(--g-font-mono); }
.section-heading small { color: var(--g-text-muted); font-size: 9px; }

.module-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 12px; }
.module-card {
  min-height: 224px;
  display: grid;
  grid-template-columns: auto 1fr;
  grid-template-rows: auto 1fr auto;
  gap: 14px 16px;
  position: relative;
  padding: 20px;
  overflow: hidden;
  border: 1px solid var(--g-border);
  border-radius: 2px;
  color: inherit;
  background: var(--g-panel);
  text-decoration: none;
  transition: transform .18s ease, border-color .18s ease, box-shadow .18s ease;
}

.module-card::after { content: ''; position: absolute; top: -1px; right: -1px; width: 22px; height: 22px; border-top: 2px solid var(--g-primary); border-right: 2px solid var(--g-primary); opacity: .45; }
.module-card:hover { transform: translateY(-3px); border-color: var(--g-primary); box-shadow: 0 12px 34px rgba(0, 0, 0, .28), 0 0 18px rgba(0, 229, 255, .06); }
.module-card .el-icon { grid-column: 1; grid-row: 2; width: 42px; height: 42px; border: 1px solid var(--g-border-strong); color: var(--g-primary); background: rgba(0, 229, 255, .05); font-size: 22px; }
.module-code { grid-column: 1 / -1; color: var(--g-text-muted); font-size: 10px; }
.module-copy { grid-column: 2; grid-row: 2; }
.module-copy small { color: var(--g-primary); font-size: 9px; }
.module-copy h2 { margin: 5px 0 8px; font-size: 19px; }
.module-copy p { color: var(--g-text-secondary); font-size: 13px; line-height: 1.65; }
.module-footer { grid-column: 1 / -1; display: flex; justify-content: space-between; padding-top: 13px; border-top: 1px solid var(--g-border); color: var(--g-text-muted); font-size: 9px; }
.module-footer b { color: var(--g-primary); }

@media (max-width: 1100px) { .module-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
@media (max-width: 760px) {
  .dashboard-hero { align-items: flex-start; flex-direction: column; }
  .hero-state { width: 100%; padding: 14px 0 0; border-left: 0; border-top: 1px solid var(--g-border); }
  .signal-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .module-grid { grid-template-columns: 1fr; }
  .section-heading small { display: none; }
}
</style>
