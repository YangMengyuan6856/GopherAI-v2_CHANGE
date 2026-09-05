<template>
  <div class="ai-chat-container">
    <!-- 左侧会话列表 -->
    <div class="session-list">
      <div class="session-list-header">
        <span>会话列表</span>
        <button class="new-chat-btn" @click="createNewSession">＋ 新聊天</button>
      </div>
      <ul class="session-list-ul">
        <li
          v-for="session in sessions"
          :key="session.id"
          :class="['session-item', { active: currentSessionId === session.id }]"
          @click="switchSession(session.id)"
        >
          {{ session.name || `会话 ${session.id}` }}
        </li>
      </ul>
    </div>

    <!-- 右侧聊天区域 -->
    <div class="chat-section">
      <div class="top-bar">
        <button class="back-btn" @click="$router.push('/menu')">← 返回</button>
        <button class="sync-btn" @click="syncHistory" :disabled="!currentSessionId || tempSession">同步历史数据</button>
        <span class="route-mode" title="新识别器只记录建议和指标；实际路由仍由当前显式开关决定">🧭 意图 Shadow（不切流）</span>
        <label for="streamingMode" style="margin-left: 20px;">
          <input type="checkbox" id="streamingMode" v-model="isStreaming" />
          流式响应
        </label>
        <label for="knowledgeMode" class="knowledge-mode" title="显式要求统一聊天入口使用 rag_fast；关闭时普通聊天保持原路径">
          <input type="checkbox" id="knowledgeMode" v-model="knowledgeRequired" />
          知识库回答
        </label>
        <label for="diagnosticMode" class="diagnostic-mode" title="显式进入可暂停、可恢复、有预算和公开步骤的故障诊断 Agent">
          <input type="checkbox" id="diagnosticMode" v-model="diagnosticMode" @change="onDiagnosticModeChanged" />
          故障诊断 Harness
        </label>
        <button class="memory-toggle-btn" :class="{ 'workspace-active': memoryPreviewOpen }" :aria-pressed="memoryPreviewOpen" :disabled="loadingMemoryPreview" @click="toggleMemoryPreview">🧠 三级记忆</button>
        <button class="tool-runtime-toggle" :class="{ 'workspace-active': toolRuntimeOpen }" :aria-pressed="toolRuntimeOpen" :disabled="loadingToolCatalog" @click="toggleToolRuntime">🛡 受治理工具</button>
        <button class="strategy-control-toggle" :class="{ 'workspace-active': policyControlOpen }" :aria-pressed="policyControlOpen" :disabled="loadingPolicyControl" @click="togglePolicyControl">🧭 策略演算</button>
        <button class="evaluation-catalog-toggle" :class="{ 'workspace-active': evaluationCatalogOpen }" :aria-pressed="evaluationCatalogOpen" :disabled="loadingEvaluationCatalog" @click="toggleEvaluationCatalog">📊 评测总览</button>
        <button
          class="upload-btn"
          title="支持 Markdown/TXT、JSON/YAML key path 和 Go 顶层符号索引"
          @click="triggerFileUpload"
          :disabled="uploading"
        >📎 上传项目文档</button>
        <button class="search-toggle-btn" :class="{ 'workspace-active': knowledgeSearchOpen }" :aria-pressed="knowledgeSearchOpen" @click="toggleKnowledgeSearch">🔎 证据检索</button>
        <input
          ref="fileInput"
          type="file"
          accept=".md,.txt,.json,.yaml,.yml,.go,text/markdown,text/plain,application/json,application/yaml"
          style="display: none"
          @change="handleFileUpload"
        />
      </div>

      <div
        v-show="policyControlOpen || toolRuntimeOpen || evaluationCatalogOpen || knowledgeDocuments.length > 0 || knowledgeSearchOpen || memoryPreviewOpen || diagnosticMode"
        class="capability-workspace"
        aria-label="当前能力工作区"
      >

      <section v-if="policyControlOpen" class="strategy-control-panel">
        <div class="strategy-control-header">
          <div>
            <strong>Strategy Registry · 固定分桶演算</strong>
            <span>MySQL 权威策略 → Redis 短缓存 → 依赖健康过滤 → 稳定 Bucket → 有界预算</span>
          </div>
          <span class="shadow-only-badge">SHADOW ONLY · 不切流</span>
        </div>
        <div v-if="loadingPolicyControl" class="strategy-control-empty">正在读取当前生效策略...</div>
        <template v-else-if="policySnapshot">
          <div class="strategy-policy-identity">
            <span>策略 {{ policySnapshot.policy.version }}</span>
            <span>环境 {{ policySnapshot.policy.environment }}</span>
            <span>权威状态 {{ policySnapshot.policy.status }}</span>
            <span>本次读取 {{ policySourceLabel(policySnapshot.policy.source) }}</span>
            <span :class="policySnapshot.policy.cache_degraded ? 'policy-warning' : 'policy-ok'">
              {{ policySnapshot.policy.cache_degraded ? 'Redis 异常，已回退 MySQL' : '缓存链路正常' }}
            </span>
            <span>Hash {{ shortPolicyHash(policySnapshot.policy.hash) }}</span>
          </div>
          <div class="strategy-control-notice">{{ policySnapshot.notice }} 下方结果只是“如果允许新策略接管，会怎样选”的可解释预演。</div>
          <div class="strategy-simulator">
            <div class="strategy-intent-actions">
              <button
                v-for="option in strategyIntentOptions"
                :key="option.value"
                :class="{ active: selectedStrategyIntent === option.value }"
                :disabled="simulatingPolicy"
                @click="simulatePolicy(option.value)"
              >{{ option.label }}</button>
            </div>
            <span v-if="!policySimulation" class="strategy-control-empty">选择一个场景，验证同一登录用户的分桶是否稳定。</span>
          </div>
          <article v-if="policySimulation" class="strategy-simulation-result">
            <div class="strategy-result-heading">
              <strong>演算选择：{{ policySimulation.selection.decision.strategy_name }}@{{ policySimulation.selection.decision.strategy_version }}</strong>
              <span>Bucket {{ policySimulation.selection.decision.experiment_bucket }} / 0000–9999</span>
            </div>
            <div class="strategy-result-grid">
              <span>意图 {{ strategyIntentLabel(policySimulation.intent) }}</span>
              <span>原因 {{ strategyReasonLabel(policySimulation.selection.decision.reason_code) }}</span>
              <span>策略来源 {{ policySourceLabel(policySimulation.selection.policy_source) }}</span>
              <span>最大 Agent {{ policySimulation.selection.decision.budgets.max_agents }}</span>
              <span>最大工具调用 {{ policySimulation.selection.decision.budgets.max_tool_calls }}</span>
              <span>最大迭代 {{ policySimulation.selection.decision.budgets.max_iterations }}</span>
            </div>
            <div class="strategy-dependencies">
              <span
                v-for="(available, dependency) in policySimulation.dependencies"
                :key="dependency"
                :class="available ? 'dependency-ready' : 'dependency-down'"
              >{{ strategyDependencyLabel(dependency) }} {{ available ? 'Ready' : 'Unavailable' }}</span>
            </div>
            <div v-if="policySimulation.selection.filtered_strategies?.length" class="policy-warning">
              因依赖或状态不可用已过滤：{{ policySimulation.selection.filtered_strategies.join('、') }}
            </div>
          </article>
          <section class="case-shadow-console">
            <div class="strategy-result-heading">
              <div>
                <strong>历史案例增强演算</strong>
                <p>仅比较已由用户确认的历史故障；强匹配也只是候选优先级，不会修改基线诊断或执行修复。</p>
              </div>
              <span class="shadow-only-badge">diagnosis_case_based · SHADOW</span>
            </div>
            <div class="case-shadow-input">
              <textarea
                v-model="caseShadowMessage"
                rows="2"
                maxlength="4000"
                placeholder="例如：Redis 返回 NOAUTH Authentication required，应用容器无法连接缓存。"
              ></textarea>
              <button :disabled="runningCaseShadow || !caseShadowMessage.trim()" @click="runCaseShadow">
                {{ runningCaseShadow ? '演算中...' : '运行案例 Shadow' }}
              </button>
            </div>
            <article v-if="caseShadowResult" class="case-shadow-result">
              <div class="strategy-result-grid">
                <span>匹配强度 {{ caseStrengthLabel(caseShadowResult.case_strength) }}</span>
                <span>案例记忆 {{ caseMemoryStatusLabel(caseShadowResult.case_memory_status) }}</span>
                <span>原因 {{ caseReasonLabel(caseShadowResult.reason_code) }}</span>
                <span>基线假设 {{ caseShadowResult.baseline?.hypotheses?.length || 0 }} 条（保持不变）</span>
              </div>
              <div v-if="caseShadowResult.priority_recommendation" class="case-priority-recommendation">
                <strong>候选优先检查：{{ caseShadowResult.priority_recommendation.hypothesis_id }}</strong>
                <span>相似度 {{ metricPercent(caseShadowResult.priority_recommendation.similarity) }} · 仅建议，不自动确认根因</span>
                <span>历史根因：{{ caseShadowResult.priority_recommendation.historical_root_cause }}</span>
                <span>历史处置：{{ caseShadowResult.priority_recommendation.historical_resolution }}</span>
              </div>
              <div v-else class="strategy-control-empty">没有达到“强案例 + 基线假设一致”的双门槛，继续采用 diagnosis_standard。</div>
              <details v-if="caseShadowResult.cases?.length" class="strategy-registry-details">
                <summary>查看命中的已确认案例（{{ caseShadowResult.cases.length }}）</summary>
                <div class="strategy-registry-grid">
                  <article v-for="item in caseShadowResult.cases" :key="item.incident_id">
                    <strong>{{ item.incident_id }} · 相似度 {{ metricPercent(item.score) }}</strong>
                    <p>{{ item.symptom }}</p>
                    <p>根因：{{ item.root_cause }}</p>
                  </article>
                </div>
              </details>
            </article>
          </section>
          <section class="collaboration-plan-console">
            <div class="strategy-result-heading">
              <div>
                <strong>有限多 Agent 规划门</strong>
                <p>先判断是否值得拆分；可只看计划，也可显式运行隔离的协作 Shadow，均不改变下方聊天结果。</p>
              </div>
              <span class="shadow-only-badge">SHADOW ONLY · MAX 2</span>
            </div>
            <div class="case-shadow-input">
              <textarea
                v-model="collaborationPlanMessage"
                rows="2"
                maxlength="4000"
                placeholder="例如：Redis NOAUTH，同时 RabbitMQ PRECONDITION_FAILED，请核对项目文档并分别定位。"
              ></textarea>
              <div class="collaboration-actions">
                <button :disabled="planningCollaboration || runningCollaboration || !collaborationPlanMessage.trim()" @click="runCollaborationPlan">
                  {{ planningCollaboration ? '规划中...' : '只运行规划门' }}
                </button>
                <button class="collaboration-run-button" :disabled="planningCollaboration || runningCollaboration || !collaborationPlanMessage.trim()" @click="runCollaborationShadow">
                  {{ runningCollaboration ? '并行执行中...' : '执行协作 Shadow' }}
                </button>
                <button :disabled="loadingCollaborationEvaluation" @click="toggleCollaborationEvaluation">
                  {{ collaborationEvaluationOpen ? '收起 A/B 报告' : (loadingCollaborationEvaluation ? '读取报告中...' : '查看 A/B 净收益') }}
                </button>
              </div>
            </div>
            <article v-if="collaborationPlan" class="case-shadow-result">
              <div class="strategy-result-heading">
                <strong>{{ collaborationDecisionLabel(collaborationPlan.decision) }}</strong>
                <span>复杂度 {{ collaborationPlan.complexity_score }} / 门槛 {{ collaborationPlan.complexity_threshold }}</span>
              </div>
              <div class="strategy-result-grid">
                <span>候选策略 {{ collaborationPlan.strategy }}</span>
                <span>原因 {{ collaborationReasonLabel(collaborationPlan.reason_code) }}</span>
                <span>Agent 上限 {{ collaborationPlan.budget.max_agents }}</span>
                <span>总超时 {{ collaborationPlan.budget.total_timeout_ms / 1000 }} 秒</span>
              </div>
              <div class="collaboration-signal-list">
                <span :class="collaborationPlan.signals.has_independent_failure_scopes ? 'signal-on' : 'signal-off'">独立故障域：{{ collaborationPlan.signals.has_independent_failure_scopes ? '是' : '否' }}</span>
                <span :class="collaborationPlan.signals.has_knowledge_verification ? 'signal-on' : 'signal-off'">项目证据核对：{{ collaborationPlan.signals.has_knowledge_verification ? '是' : '否' }}</span>
                <span :class="collaborationPlan.signals.has_evidence_conflict ? 'signal-on' : 'signal-off'">证据冲突：{{ collaborationPlan.signals.has_evidence_conflict ? '是' : '否' }}</span>
                <span :class="collaborationPlan.signals.has_high_impact_marker ? 'signal-on' : 'signal-off'">高影响标记：{{ collaborationPlan.signals.has_high_impact_marker ? '是' : '否' }}</span>
              </div>
              <div class="collaboration-task-grid">
                <article v-for="task in collaborationPlan.tasks" :key="task.task_id">
                  <div class="strategy-result-heading">
                    <strong>{{ task.index }}. {{ task.agent }}</strong>
                    <span>{{ task.output_contract }}</span>
                  </div>
                  <p>{{ task.objective }}</p>
                  <p>预算：迭代 {{ task.budget.max_iterations }} · 工具 {{ task.budget.max_tool_calls }} · 输入 {{ task.budget.max_input_tokens }} tokens</p>
                  <p>{{ task.may_spawn_agents ? '允许递归（异常）' : '禁止递归创建 Agent' }}</p>
                </article>
              </div>
              <div class="strategy-control-notice">{{ collaborationPlan.limitations[0] }}</div>
            </article>
            <article v-if="collaborationRun" class="case-shadow-result collaboration-run-result">
              <div class="strategy-result-heading">
                <strong>{{ collaborationRunStatusLabel(collaborationRun.status) }}</strong>
                <span>{{ collaborationRun.executed ? '已运行受限子 Agent' : '规划门阻止执行' }}</span>
              </div>
              <div class="strategy-result-grid">
                <span>模式 {{ collaborationRun.mode }}</span>
                <span>影响线上聊天 {{ collaborationRun.affects_live_traffic ? '是（异常）' : '否' }}</span>
                <span>原因 {{ collaborationRunReasonLabel(collaborationRun.reason_code) }}</span>
                <span v-if="collaborationRun.fallback_strategy">安全回退 {{ collaborationRun.fallback_strategy }}</span>
              </div>
              <div v-if="collaborationRun.execution" class="collaboration-task-grid">
                <article v-for="task in collaborationRun.execution.task_results" :key="`run-${task.task_id}`">
                  <div class="strategy-result-heading">
                    <strong>{{ task.agent }}</strong>
                    <span :class="task.status === 'succeeded' ? 'signal-on' : 'signal-off'">{{ collaborationTaskStatusLabel(task.status) }}</span>
                  </div>
                  <p>{{ task.output.summary || '该 Agent 没有返回可采纳摘要。' }}</p>
                  <p>耗时 {{ task.duration_ms }} ms · Claim {{ task.output.claims.length }} · Evidence {{ task.output.evidence.length }}</p>
                  <p>输出原因 {{ task.output.output_reason || task.reason_code }}</p>
                </article>
              </div>
              <template v-if="collaborationRun.synthesis">
                <div class="collaboration-unified-answer">{{ collaborationRun.synthesis.unified_answer }}</div>
                <div class="strategy-result-grid">
                  <span>通过引用的 Claim {{ collaborationRun.synthesis.claims.length }}</span>
                  <span>合成引用 {{ collaborationRun.synthesis.citations.length }}</span>
                  <span>冲突 {{ collaborationRun.synthesis.conflicts.length }}</span>
                  <span>拒绝 Claim {{ collaborationRun.synthesis.rejected_claims.length }}</span>
                </div>
                <details v-if="collaborationRun.synthesis.citations.length" class="strategy-registry-details">
                  <summary>查看合成后的证据引用（{{ collaborationRun.synthesis.citations.length }}）</summary>
                  <div class="strategy-registry-grid">
                    <article v-for="citation in collaborationRun.synthesis.citations" :key="citation.citation_id">
                      <strong>{{ citation.citation_id }} · {{ citation.source_type }}</strong>
                      <p>{{ citation.source_id }}<template v-if="citation.source_version"> · v{{ citation.source_version }}</template></p>
                      <p v-if="citation.line_start">L{{ citation.line_start }}-{{ citation.line_end }}</p>
                      <p v-if="citation.source_kind || citation.source_revision">
                        {{ sourceKindLabel(citation.source_kind) }}<template v-if="citation.source_revision"> · revision {{ shortRevision(citation.source_revision) }}</template>
                        <template v-if="citation.authority"> · 权威级 {{ citation.authority }}</template>
                      </p>
                    </article>
                  </div>
                </details>
              </template>
              <div class="strategy-control-notice">这是显式评测入口：最多 2 个 Agent、禁止递归、只读、Shadow，不会自动切换正式聊天策略。</div>
            </article>
            <article v-if="collaborationEvaluationOpen" class="case-shadow-result collaboration-evaluation-result">
              <div v-if="collaborationEvaluation">
                <div class="diagnostic-evaluation-title">
                  <div>
                    <strong>standard vs collaborative 成对 A/B</strong>
                    <span>{{ collaborationEvaluation.metrics.case_count }} 条 · {{ collaborationEvaluation.dataset_version }}</span>
                  </div>
                  <span :class="['evaluation-gate', collaborationEvaluation.technical_gates_passed ? 'passed' : 'failed']">
                    {{ collaborationEvaluation.technical_gates_passed ? '技术门通过' : '技术门未通过' }}
                  </span>
                </div>
                <div class="diagnostic-evaluation-grid">
                  <div><strong>{{ metricPercent(collaborationEvaluation.metrics.baseline_mean_quality) }}</strong><span>standard 质量</span></div>
                  <div><strong>{{ metricPercent(collaborationEvaluation.metrics.candidate_mean_quality) }}</strong><span>collaborative 质量</span></div>
                  <div><strong>+{{ metricPercent(collaborationEvaluation.metrics.mean_quality_delta) }}</strong><span>成对净提升</span></div>
                  <div><strong>[{{ metricPercent(collaborationEvaluation.metrics.quality_delta_ci95_lower) }}, {{ metricPercent(collaborationEvaluation.metrics.quality_delta_ci95_upper) }}]</strong><span>95% paired CI</span></div>
                  <div><strong>{{ collaborationEvaluation.metrics.baseline_p95_latency_ms }} / {{ collaborationEvaluation.metrics.candidate_p95_latency_ms }} ms</strong><span>standard / candidate P95</span></div>
                  <div><strong>{{ metricPercent(collaborationEvaluation.metrics.simple_false_trigger_rate) }}</strong><span>简单请求误触发率</span></div>
                  <div><strong>{{ metricPercent(collaborationEvaluation.metrics.target_trigger_rate) }}</strong><span>复杂目标触发率</span></div>
                  <div><strong>{{ Math.round(collaborationEvaluation.metrics.candidate_mean_input_tokens + collaborationEvaluation.metrics.candidate_mean_output_tokens) }}</strong><span>每条平均 Token</span></div>
                  <div><strong>{{ collaborationEvaluation.metrics.maximum_observed_agents }}</strong><span>实际最大 Agent 数</span></div>
                  <div><strong>{{ collaborationEvaluation.metrics.safety_violation_count }} / {{ collaborationEvaluation.metrics.budget_violation_count }}</strong><span>安全 / 预算违规</span></div>
                </div>
                <div class="evaluation-candidate-warning">
                  <strong>默认流量仍为 {{ collaborationEvaluation.recommended_default_weight }}%</strong>
                  <span>{{ collaborationEvaluation.human_reviewed ? '标签已人工复核' : '20 条标签仍待人工复核' }}；{{ collaborationEvaluation.promotion_eligible ? '满足晋级门' : '当前不可晋级' }}。技术收益不能绕过人工复核与密封留出集。</span>
                </div>
                <details v-if="collaborationEvaluation.gate_failures?.length" class="strategy-registry-details">
                  <summary>查看未通过原因（{{ collaborationEvaluation.gate_failures.length }}）</summary>
                  <div class="strategy-control-notice">{{ collaborationEvaluation.gate_failures.join(' · ') }}</div>
                </details>
              </div>
              <div v-else class="diagnostic-evaluation-loading">正在读取不含逐例问题的评测汇总...</div>
            </article>
          </section>
          <details class="strategy-registry-details">
            <summary>查看 7 个策略的元数据与治理边界</summary>
            <div class="strategy-registry-grid">
              <article v-for="strategy in policySnapshot.registry" :key="strategy.name">
                <div class="strategy-result-heading">
                  <strong>{{ strategy.name }}</strong>
                  <span :class="`strategy-state-${strategy.state}`">{{ strategyStateLabel(strategy.state) }}</span>
                </div>
                <p>{{ strategy.version }} · 延迟 {{ strategy.latency_tier }} · 成本 {{ strategy.cost_tier }}</p>
                <p>意图：{{ strategy.intents.join(' / ') }}</p>
                <p>依赖：{{ (strategy.dependencies || []).length ? strategy.dependencies.map(strategyDependencyLabel).join(' / ') : '无，可安全兜底' }}</p>
                <p>控制权限：{{ strategy.control_level }}<template v-if="strategy.fallback"> · 降级到 {{ strategy.fallback }}</template></p>
              </article>
            </div>
          </details>
        </template>
        <div v-else class="strategy-control-empty">当前策略暂时不可读取。</div>
      </section>

      <section v-if="toolRuntimeOpen" class="tool-runtime-panel">
        <div class="tool-runtime-header">
          <div>
            <strong>受治理 Tool Runtime</strong>
            <span>Registry → Schema → 意图/权限/副作用 → 预算/超时 → 审计/指标</span>
          </div>
          <div class="tool-runtime-header-actions">
            <button :disabled="loadingToolEvaluation" @click="toggleToolEvaluation">
              {{ loadingToolEvaluation ? '读取评测中...' : '查看 30 条工具评测' }}
            </button>
            <span class="tool-runtime-schema">{{ toolCatalog?.schema_version || 'tool-message-v1' }}</span>
          </div>
        </div>
        <section v-if="toolEvaluationOpen" class="tool-evaluation-panel">
          <div v-if="toolEvaluation" class="tool-evaluation-summary">
            <div class="tool-result-heading">
              <strong>{{ toolEvaluation.technical_gates_passed ? '✅ 技术门通过' : '❌ 技术门未通过' }}</strong>
              <span>{{ toolEvaluation.metrics.case_count }} 条 · {{ toolEvaluation.evaluator_version }}</span>
            </div>
            <div class="tool-evaluation-metrics">
              <span>工具选型 {{ metricPercent(toolEvaluation.metrics.tool_selection_accuracy) }}</span>
              <span>Schema 契约 {{ metricPercent(toolEvaluation.metrics.schema_contract_pass_rate) }}</span>
              <span>授权策略 {{ metricPercent(toolEvaluation.metrics.authorization_policy_pass_rate) }}</span>
              <span>韧性机制 {{ metricPercent(toolEvaluation.metrics.resilience_pass_rate) }}</span>
              <span>安全用例 {{ metricPercent(toolEvaluation.metrics.safety_pass_rate) }}</span>
              <span>审计覆盖 {{ metricPercent(toolEvaluation.metrics.audit_coverage_rate) }}</span>
              <span>确定性重放 {{ metricPercent(toolEvaluation.metrics.deterministic_replay_rate) }}</span>
              <span>错参有界修复 {{ metricPercent(toolEvaluation.metrics.bounded_repair_pass_rate) }}</span>
              <span>重复动作熔断 {{ metricPercent(toolEvaluation.metrics.no_progress_termination_rate) }}</span>
              <span>危险动作执行率 {{ metricPercent(toolEvaluation.metrics.dangerous_action_execution_rate) }}</span>
              <span>未知工具执行 {{ toolEvaluation.metrics.unknown_tool_execution_count }} 次</span>
            </div>
            <div class="evaluation-candidate-warning">
              <strong>候选报告 · 尚不可作为人工基线</strong>
              <span>SHA-256 {{ toolEvaluation.report_sha256 }}</span>
              <span v-for="limitation in toolEvaluation.limitations" :key="limitation">{{ limitation }}</span>
            </div>
          </div>
          <div v-else class="tool-runtime-empty">正在读取只含汇总指标的评测报告...</div>
        </section>
        <div class="tool-agent-console">
          <input
            v-model="toolAgentQuery"
            placeholder="例如：给出当前发布清单，并检查后端和 Worker 健康状态"
            @keydown.enter.prevent="runToolAgent"
          />
          <button :disabled="runningToolAgent || !toolAgentQuery.trim()" @click="runToolAgent">
            {{ runningToolAgent ? '有界规划与执行中...' : '运行 ToolAgent' }}
          </button>
        </div>
        <article v-if="toolAgentResult" class="tool-agent-result">
          <div class="tool-result-heading">
            <strong>有限计划 · {{ toolAgentResult.status }}</strong>
            <span>{{ toolAgentResult.plan.planner_version }} · 最多 2 个调用</span>
          </div>
          <div>决策 {{ toolAgentResult.plan.decision }} · {{ toolAgentResult.plan.reason_code }}</div>
          <div v-if="toolAgentResult.plan.omitted_count">调用预算只保留前 2 步，另有 {{ toolAgentResult.plan.omitted_count }} 个候选动作未执行。</div>
          <div v-if="toolAgentResult.repair_count || toolAgentResult.termination_reason" class="tool-agent-governance">
            <span>Schema 修复 {{ toolAgentResult.repair_count || 0 }}/2</span>
            <span v-if="toolAgentResult.termination_reason">终止原因 {{ toolAgentResult.termination_reason }}</span>
          </div>
          <ol v-if="toolAgentResult.plan.calls?.length">
            <li v-for="(call, index) in toolAgentResult.plan.calls" :key="`${call.tool_name}-${index}`">
              {{ call.tool_name }} · {{ call.reason_code }} · {{ formatToolData(call.arguments) }}
              <span v-if="toolAgentResult.tool_messages[index]">→ {{ toolAgentResult.tool_messages[index].status }}<template v-if="toolAgentResult.tool_messages[index].stale"> · 陈旧证据降级</template><template v-else-if="toolAgentResult.tool_messages[index].cached"> · cache hit</template></span>
            </li>
          </ol>
          <div v-else>没有执行工具；普通知识问题交还回答模型，危险写操作在规划层拒绝。</div>
          <details v-if="toolAgentResult.tool_messages?.length">
            <summary>查看稳定 ToolMessage（{{ toolAgentResult.tool_messages.length }}）</summary>
            <pre>{{ formatToolData(toolAgentResult.tool_messages) }}</pre>
          </details>
          <details v-if="toolAgentResult.repairs?.length || toolAgentResult.attempt_messages?.length > toolAgentResult.tool_messages?.length">
            <summary>查看候选修复账本（原始参数不出站）</summary>
            <pre>{{ formatToolData({ repairs: toolAgentResult.repairs, attempts: toolAgentResult.attempt_messages }) }}</pre>
          </details>
        </article>
        <div v-if="loadingToolCatalog" class="tool-runtime-empty">正在读取服务端工具注册表...</div>
        <div v-else-if="toolCatalog?.tools?.length" class="tool-runtime-grid">
          <article v-for="tool in toolCatalog.tools" :key="`${tool.name}:${tool.version}`" class="tool-runtime-card">
            <div class="tool-runtime-title">
              <strong>{{ tool.name }}</strong>
              <span>v{{ tool.version }}</span>
            </div>
            <p>{{ tool.description }}</p>
            <div class="tool-runtime-tags">
              <span>{{ tool.side_effect }}</span>
              <span>{{ tool.timeout_ms }} ms</span>
              <span v-if="tool.stale_if_error_ms">失败时陈旧证据 ≤ {{ Math.round(tool.stale_if_error_ms / 1000) }} 秒</span>
              <span>{{ tool.required_permission }}</span>
              <span>意图：{{ tool.allowed_intents.join(' / ') }}</span>
            </div>
            <button
              v-if="tool.name === 'deployment_manifest_lookup'"
              :disabled="invokingTool"
              @click="invokeGovernedTool(tool.name, {})"
            >{{ invokingTool ? '执行治理链路中...' : '查询当前部署清单' }}</button>
            <div v-if="tool.name === 'service_health_snapshot'" class="tool-health-actions">
              <button :disabled="invokingTool" @click="invokeGovernedTool(tool.name, { service: 'backend', probe: 'ready' })">Backend Ready</button>
              <button :disabled="invokingTool" @click="invokeGovernedTool(tool.name, { service: 'index_worker', probe: 'ready' })">Worker Ready</button>
            </div>
            <div v-if="tool.name === 'bounded_log_signature'" class="tool-health-actions">
              <button :disabled="invokingTool" @click="invokeGovernedTool(tool.name, { service: 'backend', signature: 'error' })">Backend 错误签名</button>
              <button :disabled="invokingTool" @click="invokeGovernedTool(tool.name, { service: 'index_worker', signature: 'warning' })">Worker 告警签名</button>
            </div>
            <button
              v-if="tool.name === 'mcp_deployment_evidence'"
              :disabled="invokingTool"
              @click="invokeGovernedTool(tool.name, {})"
            >查询 MCP 部署证据</button>
            <div v-if="tool.name === 'official_document_search'" class="tool-health-actions">
              <button :disabled="invokingTool" @click="invokeGovernedTool(tool.name, { document_id: 'redis_acl', query: 'AUTH ACL' })">Redis ACL 官方证据</button>
              <button :disabled="invokingTool" @click="invokeGovernedTool(tool.name, { document_id: 'rabbitmq_dlx', query: 'dead letter exchange' })">RabbitMQ DLX 官方证据</button>
              <button :disabled="invokingTool" @click="invokeGovernedTool(tool.name, { document_id: 'go_context_cancel', query: 'context cancel' })">Go Context 官方证据</button>
              <button :disabled="invokingTool" @click="invokeGovernedTool(tool.name, { document_id: 'prometheus_alerting', query: 'alerting rules' })">Prometheus 告警证据</button>
            </div>
          </article>
        </div>
        <div v-else class="tool-runtime-empty">当前没有通过治理注册的工具。</div>
        <article v-if="toolResult" :class="['tool-result', toolResult.status === 'success' ? 'success' : 'failed']">
          <div class="tool-result-heading">
            <strong>ToolMessage · {{ toolResult.status }}</strong>
            <span>{{ toolResult.tool_name }}@{{ toolResult.tool_version || 'unknown' }} · {{ toolResult.latency_ms }} ms</span>
          </div>
          <div>Call {{ toolResult.call_id }} · Args SHA-256 {{ (toolResult.args_hash || '').slice(0, 16) }}…</div>
          <div v-if="toolResult.error_code">稳定错误码：{{ toolResult.error_code }} · 可重试：{{ toolResult.retryable ? '是' : '否' }}</div>
          <div v-if="toolResult.stale" class="tool-stale-warning">⚠ 依赖刷新失败，返回有时间边界的陈旧证据 · 原因 {{ toolResult.degraded_reason }}</div>
          <pre v-if="toolResult.data">{{ formatToolData(toolResult.data) }}</pre>
          <div v-if="toolResult.evidence_refs?.length">证据：{{ toolResult.evidence_refs.join('；') }}</div>
          <small>缓存 {{ toolResult.stale ? '陈旧回退' : (toolResult.cached ? '新鲜命中' : '未命中') }} · 截断 {{ toolResult.truncated ? '是' : '否' }} · 原始参数与用户标识不写入审计</small>
        </article>
      </section>

      <section v-if="evaluationCatalogOpen" class="strategy-control-panel evaluation-catalog-panel">
        <div class="strategy-control-header">
          <div>
            <strong>统一评测运行 · Full 320 数据目录</strong>
            <span>同一视图核对运行版本、执行覆盖率、技术门、人工门、失败聚类和输入报告 Hash</span>
          </div>
          <span v-if="evaluationRun" :class="['evaluation-gate', evaluationRun.decision.technical_gates_passed ? 'passed' : 'failed']">
            {{ evaluationStatusLabel(evaluationRun.decision.status) }}
          </span>
        </div>
        <div v-if="loadingEvaluationCatalog" class="strategy-control-empty">正在读取统一运行并重新计算六个切片的 Hash 与数量...</div>
        <template v-else-if="evaluationCatalog && evaluationRun">
          <article class="unified-evaluation-summary">
            <div class="evaluation-run-heading">
              <div>
                <strong>{{ evaluationRun.run_id }}</strong>
                <span>候选 {{ evaluationRun.candidate_version }} · {{ evaluationRun.runner_version }}</span>
              </div>
              <span>报告 SHA-256 {{ evaluationRun.report_sha256.slice(0, 16) }}…</span>
            </div>
            <div class="diagnostic-evaluation-grid">
              <div><strong>{{ evaluationRun.coverage.catalog_validated_cases }} / {{ evaluationRun.coverage.catalog_cases }}</strong><span>目录校验</span></div>
              <div><strong>{{ evaluationRun.coverage.completed_cases }} / {{ evaluationRun.coverage.executable_cases }}</strong><span>执行完成</span></div>
              <div><strong>{{ metricPercent(evaluationRun.coverage.execution_coverage) }}</strong><span>目录执行覆盖率</span></div>
              <div><strong>{{ metricPercent(evaluationRun.coverage.completion_rate) }}</strong><span>可执行集完成率</span></div>
            </div>
            <div class="evaluation-decision-strip">
              <span :class="evaluationRun.decision.technical_gates_passed ? 'dependency-ready' : 'dependency-down'">技术门 {{ evaluationRun.decision.technical_gates_passed ? '通过' : '失败' }}</span>
              <span :class="evaluationRun.decision.human_reviewed ? 'dependency-ready' : 'dependency-down'">人工复核 {{ evaluationRun.decision.human_reviewed ? '完成' : '待完成' }}</span>
              <span :class="evaluationRun.decision.baseline_eligible ? 'dependency-ready' : 'dependency-down'">正式基线 {{ evaluationRun.decision.baseline_eligible ? '可冻结' : '不可冻结' }}</span>
              <span class="dependency-down">默认切流 禁止</span>
            </div>
            <div class="evaluation-scorecard-grid">
              <article v-for="slice in evaluationRun.scorecard.slices" :key="slice.name">
                <div><strong>{{ evaluationSliceLabel(slice.name) }}</strong><span>{{ slice.case_count }} 条</span></div>
                <span :class="slice.passed ? 'dependency-ready' : 'dependency-down'">{{ slice.passed ? '技术通过' : '未通过' }}</span>
              </article>
            </div>
            <details class="evaluation-run-details">
              <summary>查看失败聚类与 5 份输入报告 Hash</summary>
              <div class="evaluation-failure-list">
                <span v-for="cluster in evaluationRun.failure_clusters" :key="`${cluster.slice}-${cluster.code}`">
                  {{ evaluationSliceLabel(cluster.slice) }} · {{ evaluationFailureLabel(cluster.code) }} · {{ cluster.count }} 条
                </span>
                <span v-if="!evaluationRun.failure_clusters.length">没有观察到确定性失败样本</span>
              </div>
              <div class="evaluation-artifact-list">
                <span v-for="artifact in evaluationRun.artifacts" :key="artifact.name">
                  {{ evaluationSliceLabel(artifact.name) }} {{ artifact.case_count }} 条 · SHA {{ artifact.sha256.slice(0, 16) }}…
                </span>
              </div>
            </details>
            <details class="anomaly-workbench">
              <summary>打开固定阈值 + 滑动窗口 Z-score 验收工作台</summary>
              <p>数据源为确定性验收 Fixture，不冒充线上 Prometheus；所有结果仅生成 Recommend-only 建议。</p>
              <div class="anomaly-scenario-actions">
                <button v-for="scenario in anomalyScenarios" :key="scenario.value" :disabled="loadingAnomaly" @click="simulateAnomaly(scenario.value)">
                  {{ scenario.label }}
                </button>
              </div>
              <div v-if="loadingAnomaly" class="strategy-control-empty">正在以“基线窗口不含当前点”的规则计算...</div>
              <article v-else-if="anomalyResult" :class="['anomaly-result', anomalyDecisionClass(anomalyResult.analysis)]">
                <div class="evaluation-run-heading">
                  <div>
                    <strong>{{ anomalyMetricLabel(anomalyResult.analysis.policy.metric) }} · {{ anomalyResult.analysis.policy.strategy }}</strong>
                    <span>{{ anomalyResult.simulation ? '验收模拟' : '生产观测' }} · {{ anomalyResult.source }}</span>
                  </div>
                  <span>{{ anomalyDecisionLabel(anomalyResult.analysis.decision_status) }}</span>
                </div>
                <div class="diagnostic-evaluation-grid">
                  <div><strong>{{ anomalySignalStatusLabel(anomalyResult.analysis.fixed_threshold.status) }}</strong><span>固定阈值 · {{ anomalyResult.analysis.decision_status === 'insufficient_data' ? '未参与判定' : `连续 ${anomalyResult.analysis.fixed_threshold.breach_count} 点` }}</span></div>
                  <div><strong>{{ anomalySignalStatusLabel(anomalyResult.analysis.z_score.status) }}</strong><span>Z-score · {{ anomalyResult.analysis.decision_status === 'insufficient_data' ? '未参与判定' : `连续 ${anomalyResult.analysis.z_score.breach_count} 点` }}</span></div>
                  <div><strong>{{ anomalyResult.analysis.decision_status === 'insufficient_data' ? '未计算' : (anomalyResult.analysis.z_score.zero_variance ? '∞' : Number(anomalyResult.analysis.z_score.adverse_z_score).toFixed(2)) }}</strong><span>不利方向 Z 值 · 阈值 {{ anomalyResult.analysis.policy.z_score_threshold }}</span></div>
                  <div v-if="anomalyResult.analysis.decision_status === 'insufficient_data'"><strong>{{ anomalyResult.analysis.fixed_threshold.population }} / {{ anomalyResult.analysis.policy.minimum_population }}</strong><span>当前样本 / 最低门槛 · 尚差 {{ Math.max(0, anomalyResult.analysis.policy.minimum_population - anomalyResult.analysis.fixed_threshold.population) }}</span></div>
                  <div v-else><strong>{{ anomalyResult.analysis.z_score.baseline_points }}</strong><span>基线点 · 当前点已排除 {{ anomalyResult.analysis.z_score.current_excluded ? '是' : '否' }}</span></div>
                </div>
                <div class="anomaly-reason-line">原因码：{{ anomalyResult.analysis.fixed_threshold.reason_code }} · {{ anomalyResult.analysis.z_score.reason_code }}</div>
                <div class="evaluation-candidate-warning">
                  <strong>{{ anomalyRecommendationLabel(anomalyResult.analysis.recommendation.action) }}</strong>
                  <span>建议权重变化 {{ anomalyResult.analysis.recommendation.weight_delta_basis / 100 }}% · Applied={{ anomalyResult.analysis.recommendation.applied }} · {{ anomalyResult.analysis.recommendation.mode }}</span>
                </div>
                <div class="evaluation-decision-strip">
                  <span v-for="guardrail in anomalyResult.analysis.guardrails" :key="guardrail" class="dependency-ready">{{ guardrail }}</span>
                </div>
              </article>
            </details>
            <details v-if="metricCatalog" class="metric-catalog-workbench">
              <summary>查看指标目录与标签基数审计（{{ metricCatalog.family_count }} 个指标族）</summary>
              <div class="metric-catalog-heading">
                <div>
                  <strong>{{ metricCatalog.catalog_version }}</strong>
                  <span>SHA-256 {{ metricCatalog.catalog_sha256.slice(0, 16) }}…</span>
                </div>
                <span :class="['evaluation-gate', metricCatalog.passed ? 'passed' : 'failed']">{{ metricCatalog.passed ? '目录审计通过' : '目录审计失败' }}</span>
              </div>
              <div class="diagnostic-evaluation-grid">
                <div><strong>{{ metricCatalog.family_count }}</strong><span>业务指标族</span></div>
                <div><strong>{{ metricCatalog.label_key_count }}</strong><span>受控标签键</span></div>
                <div><strong>{{ metricCatalog.max_series_estimate }} / {{ metricCatalog.series_budget }}</strong><span>最大序列估算 / 总预算</span></div>
                <div><strong>{{ metricCatalog.forbidden_label_hits }}</strong><span>高基数标签命中</span></div>
              </div>
              <div class="metric-component-strip">
                <span :class="metricCatalog.required_present_count === metricCatalog.required_family_count ? 'dependency-ready' : 'dependency-down'">
                  SDD 核心契约 {{ metricCatalog.required_present_count }} / {{ metricCatalog.required_family_count }}
                </span>
                <span :class="metricCatalog.contract_mismatch_count === 0 ? 'dependency-ready' : 'dependency-down'">类型/标签不一致 {{ metricCatalog.contract_mismatch_count }}</span>
                <span v-for="component in metricCatalog.components" :key="component.name" class="dependency-ready">
                  {{ component.name === 'index_worker' ? 'Index Worker' : 'Backend' }} {{ component.family_count }} 个
                </span>
                <span :class="metricCatalog.duplicate_metric_names === 0 ? 'dependency-ready' : 'dependency-down'">重复指标名 {{ metricCatalog.duplicate_metric_names }}</span>
              </div>
              <div class="metric-domain-grid">
                <article v-for="domain in metricCatalog.domains" :key="domain.name">
                  <strong>{{ metricDomainLabel(domain.name) }}</strong>
                  <span>{{ domain.family_count }} 个指标族 · 上限估算 {{ domain.max_series_estimate }}</span>
                </article>
              </div>
              <div class="metric-cardinality-guard">
                <strong>已阻断的高基数标签</strong>
                <span>{{ metricCatalog.high_cardinality_blocked.join('、') }}</span>
              </div>
              <details class="metric-definition-list">
                <summary>查看全部 {{ metricCatalog.definitions.length }} 个指标定义</summary>
                <div>
                  <article v-for="metric in metricCatalog.definitions" :key="metric.name">
                    <div><strong>{{ metric.name }}</strong><span>{{ metricTypeLabel(metric.type) }} · {{ metric.component }}</span></div>
                    <small>标签：{{ metric.labels.length ? metric.labels.join(', ') : '无' }} · 固定值域 · 最大序列估算 {{ metric.max_series_estimate }}</small>
                  </article>
                </div>
              </details>
              <p>目录通过只证明命名、覆盖与标签边界合格，不代表线上质量健康；实际活跃序列将在下一阶段 Prometheus recording rules 中观测。</p>
            </details>
          </article>

          <div class="evaluation-catalog-heading">
            <strong>devsupport-eval-v1 · 数据目录校验</strong>
            <span :class="['evaluation-gate', evaluationCatalog.schema_passed ? 'passed' : 'failed']">{{ evaluationCatalog.schema_passed ? 'Hash/Schema 通过' : '目录失败' }}</span>
          </div>
          <div class="diagnostic-evaluation-grid">
            <div><strong>{{ evaluationCatalog.actual_total }} / {{ evaluationCatalog.expected_total }}</strong><span>实际 / 声明用例</span></div>
            <div><strong>{{ evaluationCatalog.unique_ids }}</strong><span>全局唯一 ID</span></div>
            <div><strong>{{ evaluationCatalog.sensitive_hits }}</strong><span>凭据特征命中</span></div>
            <div><strong>{{ evaluationCatalog.slices.length }}</strong><span>冻结切片</span></div>
          </div>
          <div class="strategy-registry-grid evaluation-catalog-grid">
            <article v-for="slice in evaluationCatalog.slices" :key="slice.name">
              <div class="strategy-card-title">
                <strong>{{ evaluationSliceLabel(slice.name) }}</strong>
                <span :class="slice.passed ? 'dependency-ready' : 'dependency-down'">{{ slice.passed ? 'Hash/Schema 通过' : '失败' }}</span>
              </div>
              <p>{{ slice.actual_count }} / {{ slice.expected_count }} 条 · pending_user {{ slice.review_counts.pending_user || 0 }} · human {{ slice.review_counts.human || 0 }}</p>
              <small>SHA {{ slice.actual_sha256.slice(0, 12) }}…</small>
            </article>
          </div>
          <div class="evaluation-candidate-warning">
            <strong>{{ evaluationCatalog.baseline_eligible ? '已具备基线资格' : '当前不可冻结为基线' }}</strong>
            <span>{{ evaluationCatalog.human_reviewed ? '标签已完成人工复核。' : `320 条当前均为待用户复核；${evaluationRun.coverage.catalog_only_cases} 条安全补充集暂只做目录校验，目录完整不等于模型质量达标。` }}</span>
          </div>
        </template>
      </section>

      <div v-if="knowledgeDocuments.length" class="knowledge-status">
        <span>📚 已接收 {{ knowledgeDocuments.length }} 份文档</span>
        <span class="knowledge-latest">
          最近：{{ knowledgeDocuments[0].display_name }} · {{ documentStatusLabel(knowledgeDocuments[0].status) }}
        </span>
        <div class="knowledge-version-controls">
          <select v-model="versionTargetDocumentId" title="选择要保留历史并更新版本的文档">
            <option value="" disabled>选择活动文档</option>
            <option v-for="document in indexedKnowledgeDocuments" :key="document.id" :value="document.id">
              {{ document.display_name }} · 当前 v{{ document.current_version }}
            </option>
          </select>
          <button :disabled="uploadingVersion || !versionTargetDocumentId" @click="triggerVersionUpload">
            {{ uploadingVersion ? '新版本上传中...' : '♻ 上传新版本' }}
          </button>
          <button :disabled="rebuildingDocument || !versionTargetDocumentId" @click="rebuildSelectedDocument">
            {{ rebuildingDocument ? '重建中...' : '↻ 安全重建' }}
          </button>
          <button class="delete-document-btn" :disabled="deletingDocument || !versionTargetDocumentId" @click="deleteSelectedDocument">
            {{ deletingDocument ? '删除中...' : '删除文档' }}
          </button>
          <span v-if="pendingVersionJob" class="version-pending">
            <template v-if="pendingVersionJob.job_type === 'document_delete'">
              已退出查询，{{ jobStatusLabel(pendingVersionJob.status) }}
            </template>
            <template v-else>
              v{{ pendingVersionJob.version }} {{ jobStatusLabel(pendingVersionJob.status) }}；旧版本继续生效
            </template>
          </span>
          <input
            ref="versionFileInput"
            type="file"
            accept=".md,.txt,.json,.yaml,.yml,.go,text/markdown,text/plain,application/json,application/yaml"
            style="display: none"
            @change="handleVersionUpload"
          />
        </div>
        <details class="knowledge-document-details">
          <summary>查看全部文档状态（{{ knowledgeDocuments.length }}）</summary>
          <div class="knowledge-document-grid">
            <article v-for="document in knowledgeDocuments" :key="document.id" class="knowledge-document-card">
              <div>
                <strong>{{ document.display_name }}</strong>
                <span :class="['document-status-badge', `status-${document.status}`]">
                  {{ documentStatusLabel(document.status) }}
                </span>
              </div>
              <div class="document-version-line">
                活动版本 v{{ document.current_version }} · {{ document.content_type || '未知格式' }}
              </div>
              <div v-if="document.index_stats" class="document-index-stats">
                <strong>增量索引 {{ document.index_stats.version }}</strong>
                <span>总计 {{ document.index_stats.chunk_count }}</span>
                <span>未变 {{ document.index_stats.unchanged_chunks }}</span>
                <span>新增 {{ document.index_stats.added_chunks }}</span>
                <span>修改 {{ document.index_stats.modified_chunks }}</span>
                <span>删除 {{ document.index_stats.deleted_chunks }}</span>
                <span>复用向量 {{ document.index_stats.reused_vectors }}</span>
                <span>重算 Embedding {{ document.index_stats.embedded_chunks }}</span>
              </div>
              <div v-if="document.last_error_code" class="document-error-code">
                稳定失败码：{{ document.last_error_code }}；失败候选不会替换活动版本
              </div>
            </article>
          </div>
        </details>
      </div>

      <div v-if="knowledgeSearchOpen" class="knowledge-search-panel">
        <div class="knowledge-search-form">
          <input
            v-model="knowledgeQuery"
            type="text"
            placeholder="输入错误码、配置名或项目问题，预览系统实际召回的证据"
            @keydown.enter.prevent="searchKnowledge"
          />
          <button :disabled="!knowledgeQuery.trim() || searchingKnowledge" @click="searchKnowledge">
            {{ searchingKnowledge ? '检索中...' : '执行混合检索' }}
          </button>
          <button
            class="answer-evidence-btn"
            :disabled="!knowledgeQuery.trim() || answeringKnowledge"
            @click="answerKnowledge(false)"
          >
            {{ answeringKnowledge && answeringKnowledgeMode === 'fast' ? '快速回答中...' : '基于证据回答' }}
          </button>
          <button
            class="deep-answer-btn"
            :disabled="!knowledgeQuery.trim() || answeringKnowledge"
            @click="answerKnowledge(true)"
          >
            {{ answeringKnowledge && answeringKnowledgeMode === 'deep' ? '深度检索与回答中...' : '深度分析回答' }}
          </button>
          <button
            class="parent-answer-btn"
            :disabled="!knowledgeQuery.trim() || answeringKnowledge"
            @click="answerKnowledge('parent')"
          >
            {{ answeringKnowledge && answeringKnowledgeMode === 'parent' ? '父子检索回答中...' : '父子上下文回答' }}
          </button>
          <button class="parent-evaluation-btn" :disabled="loadingParentContextEvaluation" @click="toggleParentContextEvaluation">
            {{ parentContextEvaluationOpen ? '收起父子 A/B' : (loadingParentContextEvaluation ? '读取 A/B 中...' : '查看父子 A/B 净收益') }}
          </button>
        </div>
        <section v-if="parentContextEvaluationOpen" class="diagnostic-evaluation parent-context-evaluation">
          <div v-if="loadingParentContextEvaluation" class="diagnostic-evaluation-loading">正在读取不含逐例问题的成对评测汇总...</div>
          <template v-else-if="parentContextEvaluation">
            <div class="diagnostic-evaluation-title">
              <div>
                <strong>rag_fast vs rag_parent_context 成对 A/B</strong>
                <span>{{ parentContextEvaluation.metrics.case_count }} 条 · 同题、同模型、同 TopK</span>
              </div>
              <span :class="['evaluation-gate', parentContextEvaluation.technical_gates_passed ? 'passed' : 'failed']">
                {{ parentContextEvaluation.technical_gates_passed ? '技术门通过' : '技术门未通过' }}
              </span>
            </div>
            <div class="diagnostic-evaluation-grid">
              <div><strong>{{ metricPercent(parentContextEvaluation.metrics.baseline_mean_quality) }}</strong><span>rag_fast 平均质量</span></div>
              <div><strong>{{ metricPercent(parentContextEvaluation.metrics.candidate_mean_quality) }}</strong><span>parent-context 平均质量</span></div>
              <div><strong>{{ metricPercent(parentContextEvaluation.metrics.target_mean_quality_delta) }}</strong><span>目标切片质量差</span></div>
              <div><strong>[{{ metricPercent(parentContextEvaluation.metrics.target_quality_delta_ci95_lower) }}, {{ metricPercent(parentContextEvaluation.metrics.target_quality_delta_ci95_upper) }}]</strong><span>质量差 95% paired CI</span></div>
              <div><strong>{{ Number(parentContextEvaluation.metrics.baseline_mean_document_diversity).toFixed(2) }} / {{ Number(parentContextEvaluation.metrics.candidate_mean_document_diversity).toFixed(2) }}</strong><span>平均来源文档数</span></div>
              <div><strong>{{ metricPercent(parentContextEvaluation.metrics.input_token_overhead_rate) }}</strong><span>输入 Token 增幅</span></div>
              <div><strong>{{ parentContextEvaluation.metrics.baseline_p95_latency_ms }} / {{ parentContextEvaluation.metrics.candidate_p95_latency_ms }} ms</strong><span>P95 基线 / 候选</span></div>
              <div><strong>{{ parentContextEvaluation.metrics.baseline_p99_latency_ms }} / {{ parentContextEvaluation.metrics.candidate_p99_latency_ms }} ms</strong><span>P99 基线 / 候选</span></div>
              <div><strong>{{ metricPercent(parentContextEvaluation.metrics.child_citation_integrity_rate) }}</strong><span>Child 引用完整率</span></div>
              <div><strong>{{ metricPercent(parentContextEvaluation.metrics.target_parent_context_availability) }}</strong><span>目标样本 Parent 可用率</span></div>
            </div>
            <div class="evaluation-candidate-warning">
              <strong>默认权重仍为 {{ parentContextEvaluation.recommended_default_weight }}%</strong>
              <span>{{ parentContextEvaluation.human_reviewed ? '标签已人工复核' : '20 条标签仍待人工复核' }}；{{ parentContextEvaluation.net_benefit_passed ? '当前样本净收益门通过' : '当前样本尚未证明净收益' }}；没有人工批准不会切流。</span>
            </div>
            <details v-if="parentContextEvaluation.gate_failures?.length" class="strategy-registry-details">
              <summary>查看技术门未通过原因（{{ parentContextEvaluation.gate_failures.length }}）</summary>
              <div class="strategy-control-notice">{{ parentContextEvaluation.gate_failures.join(' · ') }}</div>
            </details>
          </template>
        </section>
        <section v-if="knowledgeAnswer" :class="['knowledge-answer', { insufficient: !knowledgeAnswer.result.resolved }]">
          <div class="knowledge-answer-header">
            <strong>{{ knowledgeAnswer.result.resolved ? '✅ 证据门通过' : '⚠️ 证据不足' }}</strong>
            <span>{{ knowledgeAnswer.agent }} · {{ knowledgeAnswer.strategy }} · {{ knowledgeAnswer.evidence_gate.reason_code }}</span>
          </div>
          <div class="knowledge-answer-text">{{ knowledgeAnswer.result.answer }}</div>
          <div v-if="knowledgeAnswer.result.conflicts && knowledgeAnswer.result.conflicts.length" class="knowledge-conflicts">
            <strong>⚠ 当前有效来源存在冲突，系统已停止生成并等待你确认</strong>
            <article v-for="conflict in knowledgeAnswer.result.conflicts" :key="conflict.conflict_id">
              <div>{{ conflict.fact_key }}</div>
              <ul>
                <li v-for="value in conflict.values" :key="`${conflict.conflict_id}-${value.evidence_id}`">
                  <strong>{{ value.value }}</strong> · {{ value.source_title || value.source_id }} · v{{ value.source_version }} ·
                  revision {{ shortRevision(value.source_revision) }} · 权威级 {{ value.authority || '兼容旧数据' }}
                </li>
              </ul>
            </article>
          </div>
          <div v-if="knowledgeAnswer.diagnostics && knowledgeAnswer.diagnostics.deep" class="deep-diagnostics">
            <strong>深度策略：</strong>{{ deepOutcomeLabel(knowledgeAnswer.diagnostics.deep) }} ·
            追加检索 {{ knowledgeAnswer.diagnostics.deep.additional_searches }} 次 ·
            候选 {{ knowledgeAnswer.diagnostics.deep.candidates_before }} → {{ knowledgeAnswer.diagnostics.deep.candidates_after }} 条 ·
            Rewrite {{ enhancementOutcomeLabel(knowledgeAnswer.diagnostics.deep.rewrite.outcome_reason) }} ·
            Rerank {{ enhancementOutcomeLabel(knowledgeAnswer.diagnostics.deep.rerank.outcome_reason) }}
            <div class="deep-budget-observation">
              实际增强模型用量：输入 {{ knowledgeAnswer.diagnostics.deep.usage.input_tokens || 0 }} Token，
              输出 {{ knowledgeAnswer.diagnostics.deep.usage.output_tokens || 0 }} Token ·
              Rewrite {{ knowledgeAnswer.diagnostics.deep.rewrite.latency_ms || 0 }} ms ·
              Rerank {{ knowledgeAnswer.diagnostics.deep.rerank.latency_ms || 0 }} ms
            </div>
            <div v-if="knowledgeAnswer.diagnostics.deep.fallback_reasons && knowledgeAnswer.diagnostics.deep.fallback_reasons.length" class="deep-fallback-reasons">
              安全回退原因：{{ knowledgeAnswer.diagnostics.deep.fallback_reasons.map(enhancementOutcomeLabel).join('；') }}
            </div>
            <details v-if="knowledgeAnswer.diagnostics.deep.rewrite.queries && knowledgeAnswer.diagnostics.deep.rewrite.queries.length > 1">
              <summary>查看原查询与改写查询</summary>
              <ol>
                <li v-for="query in knowledgeAnswer.diagnostics.deep.rewrite.queries" :key="query">{{ query }}</li>
              </ol>
            </details>
          </div>
          <div v-if="knowledgeAnswer.diagnostics && knowledgeAnswer.diagnostics.parent" class="deep-diagnostics parent-context-diagnostics">
            <strong>父子策略：</strong>先用 Child 精确召回，再补充 Parent 对象/函数/章节上下文 ·
            候选 {{ knowledgeAnswer.diagnostics.parent.candidates_before }} → {{ knowledgeAnswer.diagnostics.parent.candidates_after }} 条 ·
            Parent 上下文 {{ knowledgeAnswer.diagnostics.parent.parent_context_hits }} 条 ·
            同 Parent 限流 {{ knowledgeAnswer.diagnostics.parent.filtered_by_parent }} 条 ·
            同文档限流 {{ knowledgeAnswer.diagnostics.parent.filtered_by_document }} 条 ·
            引用仍固定指向 Child
          </div>
          <div v-if="knowledgeAnswer.result.follow_up_questions && knowledgeAnswer.result.follow_up_questions.length" class="knowledge-follow-up">
            <strong>需要补充：</strong>{{ knowledgeAnswer.result.follow_up_questions.join('；') }}
          </div>
          <div v-if="knowledgeAnswer.result.citations && knowledgeAnswer.result.citations.length" class="knowledge-citations">
            <details v-for="(citation, index) in knowledgeAnswer.result.citations" :key="citation.citation_id">
              <summary>
                [{{ index + 1 }}] {{ citation.document }} · v{{ citation.version }} ·
                {{ citation.section || '未命名章节' }} · L{{ citation.line_start }}-{{ citation.line_end }}
              </summary>
              <div v-if="evidenceForCitation(citation).source_kind || evidenceForCitation(citation).source_revision" class="evidence-source-meta">
                {{ sourceKindLabel(evidenceForCitation(citation).source_kind) }} ·
                revision {{ shortRevision(evidenceForCitation(citation).source_revision) }} ·
                权威级 {{ evidenceForCitation(citation).authority || '兼容旧数据' }}
              </div>
              <pre>{{ evidenceForCitation(citation).content || '证据内容不可用' }}</pre>
              <div v-if="knowledgeAnswer.strategy === 'rag_parent_context' && evidenceForCitation(citation).parent_context" class="parent-context-evidence">
                <strong>Parent 上下文（只辅助理解，引用仍为上方 Child 行号）</strong>
                <span>{{ evidenceForCitation(citation).parent_section || '文档范围' }} · L{{ evidenceForCitation(citation).parent_line_start }}-{{ evidenceForCitation(citation).parent_line_end }}</span>
                <pre>{{ evidenceForCitation(citation).parent_context }}</pre>
              </div>
            </details>
          </div>
        </section>
        <div v-if="knowledgeSearchDiagnostics" class="knowledge-search-summary">
          {{ retrievalModeLabel(knowledgeSearchDiagnostics.mode) }} · Dense {{ knowledgeSearchDiagnostics.dense_candidates }} 条 ·
          BM25 {{ knowledgeSearchDiagnostics.keyword_candidates }} 条 · 融合后 {{ knowledgeSearchDiagnostics.fused_candidates }} 条
          <template v-if="knowledgeSearchDiagnostics.freshness_filtered"> · 已过滤过期/未生效 {{ knowledgeSearchDiagnostics.freshness_filtered }} 条</template>
        </div>
        <div v-if="knowledgeSearchConflicts.length" class="knowledge-conflicts">
          <strong>⚠ 检索结果中有 {{ knowledgeSearchConflicts.length }} 个当前有效来源冲突</strong>
          <article v-for="conflict in knowledgeSearchConflicts" :key="conflict.conflict_id">
            <div>{{ conflict.fact_key }}</div>
            <ul>
              <li v-for="value in conflict.values" :key="`${conflict.conflict_id}-${value.evidence_id}`">
                <strong>{{ value.value }}</strong> · {{ value.source_title || value.source_id }} · revision {{ shortRevision(value.source_revision) }}
              </li>
            </ul>
          </article>
        </div>
        <div v-if="knowledgeSearchDiagnostics && knowledgeSearchDiagnostics.query_assessment" class="query-assessment">
          <div>
            <strong>策略建议（本次检索预览）：</strong>
            {{ knowledgeSearchDiagnostics.query_assessment.deep_recommended ? '建议进入 rag_deep' : '保持 rag_fast' }} ·
            复杂度 {{ queryComplexityLabel(knowledgeSearchDiagnostics.query_assessment.complexity) }} ·
            信息缺口 {{ queryGapLabel(knowledgeSearchDiagnostics.query_assessment.gap) }}
          </div>
          <div class="query-assessment-reasons">
            判定依据：{{ knowledgeSearchDiagnostics.query_assessment.reason_codes.map(queryReasonLabel).join('；') }}
          </div>
          <div v-if="knowledgeSearchDiagnostics.query_assessment.deep_recommended" class="query-assessment-action">
            如需执行改写、多查询合并和重排，请点击“深度分析回答”；普通混合检索不会暗中增加模型调用。
          </div>
        </div>
        <div v-if="knowledgeSearchResults.length" class="knowledge-search-results">
          <article v-for="(hit, index) in knowledgeSearchResults" :key="hit.evidence.id" class="evidence-card">
            <div class="evidence-title">
              <strong>#{{ index + 1 }} {{ hit.evidence.title }}</strong>
              <span>{{ hit.evidence.retrieval }} · RRF {{ hit.rrf_score.toFixed(4) }}</span>
            </div>
            <div class="evidence-location">
              v{{ hit.evidence.source_version }} · {{ hit.evidence.section || '未命名章节' }} ·
              L{{ hit.evidence.line_start }}-{{ hit.evidence.line_end }}
            </div>
            <div v-if="hit.evidence.source_kind || hit.evidence.source_revision" class="evidence-source-meta">
              {{ sourceKindLabel(hit.evidence.source_kind) }} · revision {{ shortRevision(hit.evidence.source_revision) }} ·
              权威级 {{ hit.evidence.authority || '兼容旧数据' }}
            </div>
            <pre>{{ hit.evidence.content }}</pre>
          </article>
        </div>
        <div v-else-if="knowledgeSearchDiagnostics && !searchingKnowledge" class="knowledge-search-empty">
          当前知识库没有召回相关证据；这只是检索结果，不会让聊天模型凭空回答。
        </div>
      </div>

      <section v-if="memoryPreviewOpen" class="memory-workbench">
        <div class="memory-workbench-header">
          <div>
            <strong>🧠 Context Assembler v2 · 三级记忆控制台</strong>
            <span>Working、用户确认的 Episodic、环境 Profile 都以 MySQL 为权威；Redis 仅作可重建缓存/索引</span>
          </div>
          <div>
            <button :disabled="loadingMemoryEvaluation" @click="toggleMemoryEvaluation">{{ memoryEvaluationOpen ? '收起记忆评测' : '查看记忆评测' }}</button>
            <button :disabled="loadingMemoryPreview" @click="loadMemoryPreview">刷新</button>
            <button :disabled="loadingMemoryPreview || !currentSessionId || tempSession" @click="rebuildWorkingMemory">从 MySQL 安全重建 Working</button>
          </div>
        </div>
        <div v-if="loadingMemoryPreview" class="memory-loading">正在读取当前用户的记忆边界...</div>
        <template v-else-if="memoryPreview || profileMemories">
          <section v-if="memoryEvaluationOpen" class="diagnostic-evaluation">
            <div v-if="loadingMemoryEvaluation" class="diagnostic-evaluation-loading">正在读取可追溯记忆评测报告...</div>
            <template v-else-if="memoryEvaluation">
              <div class="diagnostic-evaluation-title">
                <div><strong>三级记忆安全契约评测</strong><span>{{ memoryEvaluation.metrics.case_count }} 条 · {{ memoryEvaluation.dataset_version }}</span></div>
                <span :class="['evaluation-gate', memoryEvaluation.technical_gates_passed ? 'passed' : 'failed']">
                  {{ memoryEvaluation.technical_gates_passed ? '技术门通过' : '技术门未通过' }}
                </span>
              </div>
              <div class="diagnostic-evaluation-grid">
                <div><strong>{{ metricPercent(memoryEvaluation.metrics.relevant_memory_recall) }}</strong><span>相关记忆召回</span></div>
                <div><strong>{{ metricPercent(memoryEvaluation.metrics.stale_wrong_injection_rate) }}</strong><span>过期/错误注入率</span></div>
                <div><strong>{{ memoryEvaluation.metrics.deleted_memory_recall }}</strong><span>删除后召回数</span></div>
                <div><strong>{{ memoryEvaluation.metrics.cross_principal_leakage }}</strong><span>跨用户/租户泄漏</span></div>
                <div><strong>{{ metricPercent(memoryEvaluation.metrics.context_budget_pass_rate) }}</strong><span>Token 预算遵守率</span></div>
                <div><strong>{{ metricPercent(memoryEvaluation.metrics.deterministic_replay_rate) }}</strong><span>确定性重放率</span></div>
              </div>
              <div class="evaluation-candidate-warning">
                <strong>候选报告，不是正式基线：</strong>
                <span v-for="limitation in memoryEvaluation.limitations" :key="limitation">{{ limitation }}</span>
              </div>
              <small>{{ memoryEvaluation.evaluator_version }} · 报告 SHA-256 {{ memoryEvaluation.report_sha256.slice(0, 16) }}… · 人工复核 {{ memoryEvaluation.human_reviewed ? '完成' : '未完成' }}</small>
            </template>
          </section>
          <div v-if="memoryPreview" class="memory-stats">
            <span :class="['memory-cache-badge', `cache-${memoryPreview.window.cache_status}`]">
              {{ memoryCacheLabel(memoryPreview.window.cache_status) }}
            </span>
            <span>热窗口 {{ memoryPreview.window.messages.length }}/{{ memoryPreview.window.window_limit }} 条</span>
            <span>TTL {{ Math.round(memoryPreview.window.cache_ttl_seconds / 3600) }} 小时</span>
            <span>预算 {{ memoryPreview.context.estimated_tokens }}/{{ memoryPreview.context.budget_tokens }} Token</span>
            <span>裁剪 {{ memoryPreview.context.dropped_by_budget }} 项</span>
            <span>估算 Token 降幅 {{ metricPercent(memoryPreview.context.token_reduction_ratio) }}</span>
            <span v-if="memoryPreview.profile_recall">
              Profile {{ memoryPreview.profile_recall.status === 'hit' ? `命中 ${memoryPreview.profile_recall.returned} 条` : memoryPreview.profile_recall.status === 'no_match' ? '无相关事实' : '降级未使用' }} · {{ memoryPreview.profile_recall.policy_version }}
            </span>
          </div>
          <div class="memory-boundary">
            Working 已参与聊天上下文；Episodic 只接收用户明确确认的解决案例；Profile 自动提取结果先是候选，只有你确认/更正后才成为可召回环境事实。
          </div>
          <details v-if="memoryPreview" open>
            <summary>按当前历史和预算重建的上下文预览（{{ memoryPreview.context.included.length }}）</summary>
            <ol class="memory-context-items">
              <li v-for="(item, index) in memoryPreview.context.included" :key="`${item.kind}-${index}`">
                <strong>{{ memoryContextKindLabel(item.kind) }}</strong>
                <span>{{ item.required ? '不可压缩' : '按预算纳入' }} · 约 {{ item.estimated_tokens }} Token</span>
                <p>{{ item.content }}</p>
              </li>
            </ol>
          </details>
          <div v-else class="memory-no-session">当前没有聊天会话，因此只展示跨会话 Profile Memory；选择历史会话后可查看 Working Memory。</div>
          <section v-if="profileMemories" class="profile-memory-panel">
            <div class="profile-memory-summary">
              <strong>Profile Memory · 环境事实</strong>
              <span>已确认 {{ profileMemories.active_count }} · 待确认 {{ profileMemories.candidate_count }} · 冲突 {{ profileMemories.conflict_count }}</span>
            </div>
            <p>候选和冲突事实不会自动进入模型上下文。确认时可直接改值；更正会生成新版本并取代同键旧值。</p>
            <div v-if="profileMemories.items.length" class="profile-memory-list">
              <article v-for="item in profileMemories.items" :key="item.id" :class="`profile-${item.status}`">
                <div class="profile-memory-card-header">
                  <strong>{{ profileKeyLabel(item.key) }}</strong>
                  <span>{{ profileStatusLabel(item.status) }} · 置信度 {{ Math.round(item.confidence * 100) }}% · v{{ item.version }}</span>
                </div>
                <input v-model="profileDrafts[item.id]" maxlength="256" :aria-label="`${profileKeyLabel(item.key)}的环境记忆值`" />
                <div class="profile-memory-actions">
                  <small>来源：{{ profileSourceLabel(item.source_type) }}{{ item.expires_at ? ` · 有效至 ${new Date(item.expires_at).toLocaleDateString()}` : ' · 长期有效' }}</small>
                  <div>
                    <button :disabled="profileMemoryBusy === item.id || !profileDrafts[item.id]?.trim()" @click="correctProfileMemory(item)">
                      {{ item.status === 'active' ? '保存更正' : '确认并启用' }}
                    </button>
                    <button class="profile-delete-btn" :disabled="profileMemoryBusy === item.id" @click="deleteProfileMemory(item)">删除</button>
                  </div>
                </div>
              </article>
            </div>
            <div v-else class="memory-no-session">尚无环境记忆。故障诊断中明确出现 OS、Go、部署方式、云厂商、Redis/MySQL 版本后会生成待确认候选。</div>
          </section>
        </template>
      </section>

      <section v-if="diagnosticMode" class="diagnostic-workbench">
        <div class="diagnostic-workbench-header">
          <div>
            <strong>🩺 可恢复故障诊断</strong>
            <span>只形成有证据的假设和只读验证步骤，不执行修复命令</span>
            <span v-if="diagnosticRecovered" class="diagnostic-recovered">已从服务端持久化检查点恢复</span>
          </div>
          <div class="diagnostic-actions">
            <button v-if="activeDiagnosticRun" type="button" :disabled="loadingContextCompression" @click="toggleContextCompression">
              {{ contextCompressionOpen ? '收起上下文工程' : '查看上下文工程' }}
            </button>
            <button type="button" :disabled="loadingDiagnosticEvaluation" @click="toggleDiagnosticEvaluation">
              {{ diagnosticEvaluationOpen ? '收起评测' : '查看评测' }}
            </button>
            <button v-if="activeDiagnosticRun && !isDiagnosticTerminal(activeDiagnosticRun.run.state)" :disabled="loading" @click="cancelDiagnosticRun">取消运行</button>
            <button :disabled="loading" @click="resetDiagnosticRun">新建诊断</button>
          </div>
        </div>
        <section v-if="diagnosticEvaluationOpen" class="diagnostic-evaluation">
          <div v-if="loadingDiagnosticEvaluation" class="diagnostic-evaluation-loading">正在读取可追溯评测报告...</div>
          <template v-else-if="diagnosticEvaluation">
            <div class="diagnostic-evaluation-title">
              <div>
                <strong>DiagnosticAgent 技术候选评测</strong>
                <span>{{ diagnosticEvaluation.metrics.case_count }} 条 · {{ diagnosticEvaluation.dataset_version }}</span>
              </div>
              <span :class="['evaluation-gate', diagnosticEvaluation.technical_gates_passed ? 'passed' : 'failed']">
                {{ diagnosticEvaluation.technical_gates_passed ? '技术门通过' : '技术门未通过' }}
              </span>
            </div>
            <div class="diagnostic-evaluation-grid">
              <div><strong>{{ metricPercent(diagnosticEvaluation.metrics.root_cause_top3_recall) }}</strong><span>根因 Top-3 Recall</span></div>
              <div><strong>{{ metricPercent(diagnosticEvaluation.metrics.necessary_step_coverage) }}</strong><span>必要步骤覆盖率</span></div>
              <div><strong>{{ metricPercent(diagnosticEvaluation.metrics.verification_action_accuracy) }}</strong><span>验证动作准确率</span></div>
              <div><strong>{{ metricPercent(diagnosticEvaluation.metrics.clarification_accuracy) }}</strong><span>澄清判断准确率</span></div>
              <div><strong>{{ metricPercent(diagnosticEvaluation.metrics.premature_certainty_rate) }}</strong><span>过早确认率</span></div>
              <div><strong>{{ metricPercent(diagnosticEvaluation.metrics.dangerous_action_rate) }}</strong><span>危险动作率</span></div>
            </div>
            <div class="evaluation-candidate-warning">
              <strong>候选报告，不是正式基线：</strong>
              <span v-for="limitation in diagnosticEvaluation.limitations" :key="limitation">{{ limitation }}</span>
            </div>
            <details>
              <summary>查看分类覆盖与可追溯版本</summary>
              <div class="evaluation-category-list">
                <span v-for="(score, category) in diagnosticEvaluation.metrics.category_root_cause_recall" :key="category">
                  {{ category }} {{ metricPercent(score) }}
                </span>
              </div>
              <small>
                {{ diagnosticEvaluation.evaluator_version }} · 报告 SHA-256 {{ diagnosticEvaluation.report_sha256.slice(0, 16) }}… ·
                人工复核 {{ diagnosticEvaluation.human_reviewed ? '完成' : '未完成' }} ·
                基线资格 {{ diagnosticEvaluation.baseline_eligible ? '具备' : '不具备' }}
              </small>
            </details>
          </template>
        </section>
        <section v-if="contextCompressionOpen" class="diagnostic-evaluation">
          <div v-if="loadingContextCompression" class="diagnostic-evaluation-loading">正在从持久化 Checkpoint 重建结构化上下文...</div>
          <template v-else-if="contextCompression">
            <div class="diagnostic-evaluation-title">
              <div>
                <strong>Context Engineering · 结构化压缩</strong>
                <span>{{ contextCompression.run_state }} v{{ contextCompression.state_version }} · {{ contextCompression.assembler_version }}</span>
              </div>
              <span :class="['evaluation-gate', contextCompression.context.over_budget ? 'failed' : 'passed']">
                {{ contextCompression.context.over_budget ? '预算超限' : '预算内组装' }}
              </span>
            </div>
            <div class="diagnostic-evaluation-grid">
              <div><strong>{{ contextCompression.source_tokens }}</strong><span>压缩前估算 Token</span></div>
              <div><strong>{{ contextCompression.assembled_tokens }}</strong><span>组装后估算 Token</span></div>
              <div><strong>{{ metricPercent(contextCompression.token_reduction_ratio) }}</strong><span>Token 降幅</span></div>
              <div><strong>{{ metricPercent(contextCompression.retention.constraints.rate) }}</strong><span>约束保留率</span></div>
              <div><strong>{{ metricPercent(contextCompression.retention.confirmed_facts.rate) }}</strong><span>确认事实保留率</span></div>
              <div><strong>{{ metricPercent(contextCompression.retention.open_questions.rate) }}</strong><span>未决问题保留率</span></div>
            </div>
            <template v-if="contextEvaluation">
              <div class="diagnostic-evaluation-title">
                <div>
                  <strong>12 条成对压缩候选集</strong>
                  <span>answer / clarify / refuse / resume 各 3 条</span>
                </div>
                <span :class="['evaluation-gate', contextEvaluation.technical_gates_passed ? 'passed' : 'failed']">
                  {{ contextEvaluation.technical_gates_passed ? '技术门通过' : '技术门未通过' }}
                </span>
              </div>
              <div class="diagnostic-evaluation-grid">
                <div><strong>{{ metricPercent(contextEvaluation.metrics.constraint_retention) }}</strong><span>约束保留</span></div>
                <div><strong>{{ metricPercent(contextEvaluation.metrics.confirmed_fact_retention) }}</strong><span>确认事实保留</span></div>
                <div><strong>{{ metricPercent(contextEvaluation.metrics.open_question_retention) }}</strong><span>未决项保留</span></div>
                <div><strong>{{ metricPercent(contextEvaluation.metrics.next_action_retention) }}</strong><span>下一动作保留</span></div>
                <div><strong>{{ metricPercent(contextEvaluation.metrics.average_token_reduction) }}</strong><span>平均 Token 降幅</span></div>
                <div><strong>{{ contextEvaluation.metrics.over_budget_cases }}</strong><span>预算超限用例</span></div>
              </div>
            </template>
            <details>
              <summary>查看可校验结构化摘要（不是隐藏思维链）</summary>
              <div class="context-summary-fields">
                <p><strong>目标：</strong>{{ contextCompression.structured_summary.goal || '无' }}</p>
                <p><strong>约束：</strong>{{ (contextCompression.structured_summary.constraints || []).join('；') || '无' }}</p>
                <p><strong>已确认事实：</strong>{{ formattedFacts(contextCompression.structured_summary.confirmed_facts) }}</p>
                <p><strong>未决问题：</strong>{{ (contextCompression.structured_summary.open_questions || []).join('；') || '无' }}</p>
                <p><strong>已完成步骤：</strong>{{ (contextCompression.structured_summary.completed_steps || []).join('；') || '无' }}</p>
                <p><strong>失败步骤：</strong>{{ (contextCompression.structured_summary.failed_steps || []).join('；') || '无' }}</p>
                <p><strong>证据引用：</strong>{{ (contextCompression.structured_summary.evidence_refs || []).join('、') || '无' }}</p>
                <p><strong>下一动作：</strong>{{ contextCompression.structured_summary.next_action || '无' }}</p>
              </div>
            </details>
            <div class="evaluation-candidate-warning">
              <strong>口径说明：</strong>
              <span v-for="limitation in contextCompression.limitations" :key="limitation">{{ limitation }}</span>
            </div>
          </template>
        </section>
        <div v-if="!activeDiagnosticRun" class="diagnostic-empty">
          <span v-if="restoringDiagnosticRun">正在恢复上次诊断 Run...</span>
          <span v-else>在下方输入故障现象和脱敏日志。系统会展示状态机、公开步骤、预算、假设、证据与验证方法。</span>
        </div>
        <template v-else>
          <div class="diagnostic-run-summary">
            <span :class="['run-state-badge', `state-${activeDiagnosticRun.run.state.toLowerCase()}`]">
              {{ diagnosticStateLabel(activeDiagnosticRun.run.state) }}
            </span>
            <span>Run {{ activeDiagnosticRun.run.run_id.slice(0, 8) }}</span>
            <span>状态版本 v{{ activeDiagnosticRun.run.state_version }}</span>
            <span>策略 {{ activeDiagnosticRun.run.strategy }} · {{ activeDiagnosticRun.run.policy_version }}</span>
            <span>
              预算：轮次 {{ activeDiagnosticRun.run.budget.used_iterations }}/{{ activeDiagnosticRun.run.budget.max_iterations }} ·
              工具 {{ activeDiagnosticRun.run.budget.used_tool_calls }}/{{ activeDiagnosticRun.run.budget.max_tool_calls }} ·
              输入 {{ activeDiagnosticRun.run.budget.used_input_tokens }}/{{ activeDiagnosticRun.run.budget.max_input_tokens }} Token
            </span>
          </div>
          <div v-if="activeDiagnosticRun.run.state === 'WAITING_USER'" class="diagnostic-waiting">
            <strong>需要补充信息后才能继续：</strong>
            {{ (activeDiagnosticRun.checkpoint?.open_questions || []).join('；') }}
            <div>请直接在下方输入补充信息；恢复请求会携带当前状态版本，陈旧页面不会覆盖新状态。</div>
          </div>
          <details class="diagnostic-steps" open>
            <summary>公开执行轨迹（不包含隐藏思维链）</summary>
            <ol>
              <li v-for="step in activeDiagnosticRun.steps" :key="`${step.step_id}-${step.attempt}`">
                <strong>{{ step.public_summary || step.kind }}</strong>
                <span>{{ step.reason_code }} · 状态版本 v{{ step.state_version }}</span>
              </li>
            </ol>
          </details>
          <section
            v-if="activeDiagnosticRun.result?.case_memory_status"
            :class="['case-memory-recall', `case-${activeDiagnosticRun.result.case_memory_status}`]"
          >
            <div class="case-memory-header">
              <strong>🧠 Episodic Memory · 相似已解决案例</strong>
              <span v-if="activeDiagnosticRun.result.case_memory_status === 'hit'">
                命中 {{ activeDiagnosticRun.result.similar_incidents.length }} 条 · TopK≤3 · {{ activeDiagnosticRun.result.case_memory_policy }}
              </span>
              <span v-else-if="activeDiagnosticRun.result.case_memory_status === 'no_match'">已检索 · 无高相似案例</span>
              <span v-else>召回暂不可用 · 当前诊断不受影响</span>
            </div>
            <p class="case-memory-disclaimer">
              历史案例只提供排查经验，不是当前故障证据；不会改变本次根因假设、置信度或证据门判断。
            </p>
            <div v-if="activeDiagnosticRun.result.case_memory_status === 'hit'" class="case-memory-list">
              <article v-for="item in activeDiagnosticRun.result.similar_incidents" :key="item.incident_id">
                <div>
                  <strong>相似度 {{ Math.round(item.score * 100) }}%</strong>
                  <span>匹配错误特征：{{ item.matched_error_signatures.join('、') }}</span>
                  <span v-if="item.matched_components.length">匹配组件：{{ item.matched_components.join('、') }}</span>
                </div>
                <details>
                  <summary>查看历史根因与已验证解决办法</summary>
                  <p><strong>当时现象：</strong>{{ item.symptom }}</p>
                  <p><strong>当时根因：</strong>{{ item.root_cause }}</p>
                  <p><strong>当时解决：</strong>{{ item.resolution }}</p>
                  <small>案例 {{ item.incident_id.slice(0, 8) }} · 用户明确确认后才进入记忆</small>
                </details>
              </article>
            </div>
          </section>
          <div v-if="activeDiagnosticRun.result?.hypotheses?.length" class="diagnostic-hypotheses">
            <article v-for="hypothesis in activeDiagnosticRun.result.hypotheses" :key="hypothesis.id">
              <div class="hypothesis-header">
                <strong>{{ hypothesis.cause }}</strong>
                <span>置信度 {{ Math.round(hypothesis.confidence * 100) }}% · 待验证假设</span>
              </div>
              <p>{{ hypothesis.rationale }}</p>
              <div><strong>证据：</strong>{{ hypothesis.evidence.map(item => item.summary).join('；') }}</div>
              <ol>
                <li v-for="step in hypothesis.verification_steps" :key="step.id">
                  {{ step.instruction }}<br>
                  <small>预期：{{ step.expected_observation }}；否则：{{ step.failure_meaning }}</small>
                </li>
              </ol>
              <button
                v-if="activeDiagnosticRun.run.state === 'SUCCEEDED' && !confirmedResolution"
                class="resolution-preview-btn"
                :disabled="loadingResolution"
                @click="previewResolution(hypothesis.id)"
              >
                {{ loadingResolution ? '生成确认预览中...' : '这个假设已验证，预览案例记忆' }}
              </button>
            </article>
          </div>
          <section v-if="resolutionProposal && !confirmedResolution" class="resolution-proposal">
            <div class="resolution-proposal-header">
              <div>
                <strong>🧩 Action Proposal · 写入案例记忆前预览</strong>
                <span>预览不会写数据库；确认后才会事务写入反馈、案例和 Outbox</span>
              </div>
              <button @click="closeResolutionProposal">关闭</button>
            </div>
            <div class="resolution-proposal-content">
              <p><strong>故障现象：</strong>{{ resolutionProposal.symptom }}</p>
              <p><strong>你将确认的根因：</strong>{{ resolutionProposal.proposed_root_cause }}</p>
              <p><strong>已有证据：</strong>{{ resolutionProposal.evidence.map(item => item.summary).join('；') }}</p>
            </div>
            <label class="resolution-input-label">
              <span>请填写你实际执行且已验证有效的解决办法（不会自动执行）：</span>
              <textarea v-model="resolutionText" maxlength="1000" rows="3" placeholder="例如：修正后端容器的 Redis 主机名并重启，随后 PING 返回 PONG、接口恢复。"></textarea>
            </label>
            <label class="resolution-confirm-check">
              <input v-model="resolutionAcknowledged" type="checkbox" />
              我确认该办法已经在真实环境验证有效，并同意将脱敏结果作为我的已解决案例
            </label>
            <button
              class="resolution-confirm-btn"
              :disabled="loadingResolution || !resolutionAcknowledged || resolutionText.trim().length < 5"
              @click="confirmResolution"
            >
              {{ loadingResolution ? '确认写入中...' : '确认解决并写入 Episodic Memory' }}
            </button>
          </section>
          <section v-if="confirmedResolution" class="confirmed-resolution">
            <div class="confirmed-resolution-header">
              <strong>✅ 用户已确认的解决案例</strong>
              <div class="confirmed-resolution-actions">
                <span :class="['incident-index-badge', `index-${confirmedResolution.index_status}`]">
                  {{ incidentIndexLabel(confirmedResolution.index_status) }}
                </span>
                <button :disabled="loadingResolution" @click="loadConfirmedResolution">刷新索引状态</button>
              </div>
            </div>
            <p><strong>根因：</strong>{{ confirmedResolution.root_cause }}</p>
            <p><strong>实际解决：</strong>{{ confirmedResolution.resolution }}</p>
            <small>案例 {{ confirmedResolution.id.slice(0, 8) }} · 仅当前用户可召回 · 未确认假设不会进入索引</small>
          </section>
        </template>
      </section>

      </div>

      <div class="chat-messages" ref="messagesRef">
        <div
          v-for="(message, index) in currentMessages"
          :key="index"
          :class="['message', message.role === 'user' ? 'user-message' : 'ai-message']"
        >
          <div class="message-header">
            <b>{{ message.role === 'user' ? '你' : 'AI' }}:</b>
            <button v-if="message.role === 'assistant'" class="tts-btn" @click="playTTS(message.content)">🔊</button>
            <span v-if="message.meta && message.meta.status === 'streaming'" class="streaming-indicator"> ··</span>
          </div>
          <div class="message-content" v-html="renderMarkdown(message.content)"></div>
          <div v-if="message.role === 'assistant' && message.meta && message.meta.traceId" class="routing-meta">
            <div>实际路由 · {{ message.meta.strategy }} · {{ message.meta.policyVersion }} · Trace {{ message.meta.traceId.slice(0, 8) }}</div>
            <div v-if="message.meta.intentShadow" class="shadow-intent-meta">
              影子判断 · {{ intentLabel(message.meta.intentShadow.intent) }} ·
              {{ intentStageLabel(message.meta.intentShadow.final_stage) }} ·
              {{ Math.round((message.meta.intentShadow.confidence || 0) * 100) }}% · 不切流
              <span v-if="message.meta.intentShadow.needs_clarify"> · 建议澄清</span>
            </div>
          </div>
          <div v-if="message.role === 'assistant' && message.meta && message.meta.citations && message.meta.citations.length" class="chat-citations">
            <span v-for="(citation, citationIndex) in message.meta.citations" :key="citation.citation_id">
              [{{ citationIndex + 1 }}] {{ citation.document }} · v{{ citation.version }} ·
              {{ citation.section || '未命名章节' }} · L{{ citation.line_start }}-{{ citation.line_end }}
            </span>
          </div>
        </div>
      </div>

      <div class="chat-input">
        <div class="input-wrapper">
          <textarea
            v-model="inputMessage"
            :placeholder="diagnosticMode && activeDiagnosticRun?.run?.state === 'WAITING_USER' ? '补充上方要求的环境或日志信息，继续同一个 Run...' : '请输入项目问题、报错信息或排障目标...'"
            @keydown.enter.exact.prevent="sendMessage"
            :disabled="loading"
            ref="messageInput"
            rows="1"
          ></textarea>
        </div>
        <button
          type="button"
          :disabled="!inputMessage.trim() || loading"
          @click="sendMessage"
          class="send-btn"
        >
          {{ loading ? '发送中...' : '发送' }}
        </button>
      </div>
    </div>
  </div>
</template>

<script>


import { ref, nextTick, computed, onMounted, onUnmounted } from 'vue'
import { ElMessage } from 'element-plus'
import api from '../utils/api'

export default {
  name: 'AIChat',
  setup() {

    const sessions = ref({})
    const currentSessionId = ref(null)
    const tempSession = ref(false)
    const currentMessages = ref([])
    const inputMessage = ref('')
    const loading = ref(false)
    const messagesRef = ref(null)
    const messageInput = ref(null)
    const isStreaming = ref(false)
    const knowledgeRequired = ref(false)
    const uploading = ref(false)
    const fileInput = ref(null)
    const versionFileInput = ref(null)
    const uploadingVersion = ref(false)
    const versionTargetDocumentId = ref('')
    const pendingVersionJob = ref(null)
    const rebuildingDocument = ref(false)
    const deletingDocument = ref(false)
    const knowledgeDocuments = ref([])
    const indexedKnowledgeDocuments = computed(() => knowledgeDocuments.value.filter(document => document.status === 'indexed'))
    const knowledgeSearchOpen = ref(false)
    const knowledgeQuery = ref('')
    const searchingKnowledge = ref(false)
    const knowledgeSearchResults = ref([])
    const knowledgeSearchDiagnostics = ref(null)
    const knowledgeSearchConflicts = ref([])
    const answeringKnowledge = ref(false)
    const answeringKnowledgeMode = ref('')
    const knowledgeAnswer = ref(null)
    const evaluationCatalogOpen = ref(false)
    const loadingEvaluationCatalog = ref(false)
    const evaluationCatalog = ref(null)
    const evaluationRun = ref(null)
    const metricCatalog = ref(null)
    const loadingAnomaly = ref(false)
    const anomalyResult = ref(null)
    const anomalyScenarios = [
      { value: 'healthy', label: '健康窗口' },
      { value: 'quality_drop', label: 'RAG 质量下降' },
      { value: 'latency_spike', label: 'Agent 延迟突增' },
      { value: 'low_sample', label: '低样本抑制' },
      { value: 'zero_variance_shift', label: '零方差突变' }
    ]
    const parentContextEvaluationOpen = ref(false)
    const loadingParentContextEvaluation = ref(false)
    const parentContextEvaluation = ref(null)
    const diagnosticMode = ref(false)
    const activeDiagnosticRun = ref(null)
    const diagnosticRecovered = ref(false)
    const restoringDiagnosticRun = ref(false)
    const diagnosticEvaluationOpen = ref(false)
    const loadingDiagnosticEvaluation = ref(false)
    const diagnosticEvaluation = ref(null)
    const contextCompressionOpen = ref(false)
    const loadingContextCompression = ref(false)
    const contextCompression = ref(null)
    const contextEvaluation = ref(null)
    const memoryPreviewOpen = ref(false)
    const loadingMemoryPreview = ref(false)
    const memoryPreview = ref(null)
    const memoryEvaluationOpen = ref(false)
    const loadingMemoryEvaluation = ref(false)
    const memoryEvaluation = ref(null)
    const toolRuntimeOpen = ref(false)
    const loadingToolCatalog = ref(false)
    const toolCatalog = ref(null)
    const invokingTool = ref(false)
    const toolResult = ref(null)
    const toolAgentQuery = ref('给出当前发布清单，并检查后端和 Worker 健康状态')
    const runningToolAgent = ref(false)
    const toolAgentResult = ref(null)
    const toolEvaluationOpen = ref(false)
    const loadingToolEvaluation = ref(false)
    const toolEvaluation = ref(null)
    const policyControlOpen = ref(false)
    const loadingPolicyControl = ref(false)
    const policySnapshot = ref(null)
    const selectedStrategyIntent = ref('troubleshooting')
    const simulatingPolicy = ref(false)
    const policySimulation = ref(null)
    const caseShadowMessage = ref('Redis 返回 NOAUTH Authentication required，应用容器无法连接缓存。')
    const runningCaseShadow = ref(false)
    const caseShadowResult = ref(null)
    const collaborationPlanMessage = ref('生产发布后服务返回 HTTP 502；同时请根据 m3b-config.json 核对 release.probe_code 和 timeout_seconds，并给出只读故障排查假设。')
    const planningCollaboration = ref(false)
    const collaborationPlan = ref(null)
    const runningCollaboration = ref(false)
    const collaborationRun = ref(null)
    const collaborationEvaluationOpen = ref(false)
    const loadingCollaborationEvaluation = ref(false)
    const collaborationEvaluation = ref(null)
    const strategyIntentOptions = [
      { value: 'troubleshooting', label: '故障诊断' },
      { value: 'project_qa', label: '项目知识问答' },
      { value: 'general', label: '通用问答' }
    ]
    const profileMemories = ref(null)
    const profileDrafts = ref({})
    const profileMemoryBusy = ref('')
    const resolutionProposal = ref(null)
    const resolutionText = ref('')
    const resolutionAcknowledged = ref(false)
    const loadingResolution = ref(false)
    const confirmedResolution = ref(null)
    const diagnosticRunStorageKey = 'gopherai.active-diagnostic-run-v1'
    const diagnosticModeStorageKey = 'gopherai.diagnostic-mode-v1'
    let knowledgePollTimer = null

    const closeUtilityWorkspaces = (keep = '') => {
      if (keep !== 'memory') memoryPreviewOpen.value = false
      if (keep !== 'tools') toolRuntimeOpen.value = false
      if (keep !== 'policy') policyControlOpen.value = false
      if (keep !== 'evaluation') evaluationCatalogOpen.value = false
      if (keep !== 'knowledge') knowledgeSearchOpen.value = false
    }

    const renderMarkdown = (text) => {
      if (!text && text !== '') return ''
      return String(text)
        .replace(/\*\*(.*?)\*\*/g, '<strong>$1</strong>')
        .replace(/\*(.*?)\*/g, '<em>$1</em>')
        .replace(/`(.*?)`/g, '<code>$1</code>')
        .replace(/\n/g, '<br>')
    }

    const playTTS = async (text) => {
      try {
        // 创建TTS任务
        const createResponse = await api.post('/AI/chat/tts', { text })
        if (createResponse.data && createResponse.data.status_code === 1000 && createResponse.data.task_id) {
          const taskId = createResponse.data.task_id
          
          // 先等待5秒钟再开始轮询
          await new Promise(resolve => setTimeout(resolve, 5000))
          
          // 轮询查询任务结果
          const maxAttempts = 30
          const pollInterval = 2000
          let attempts = 0
          
          const pollResult = async () => {
            const queryResponse = await api.get('/AI/chat/tts/query', { params: { task_id: taskId } })
            
            if (queryResponse.data && queryResponse.data.status_code === 1000) {
              const taskStatus = queryResponse.data.task_status
                
              if (taskStatus === 'Success' && queryResponse.data.task_result) {
                // 任务完成，播放音频
                // 后端返回的 task_result 是直接的 URL 字符串
                const audio = new Audio(queryResponse.data.task_result)
                audio.play()
                return true
              } else if (taskStatus === 'Running' ||taskStatus === 'Created' ) {
                // 任务进行中，继续轮询
                attempts++
                if (attempts < maxAttempts) {
                  await new Promise(resolve => setTimeout(resolve, pollInterval))
                  return await pollResult()
                } else {
                  ElMessage.error('语音合成超时')
                  return true
                }
              } else {
                // 其他状态（如失败）
                ElMessage.error('语音合成失败')
                return true
              }
            }
            
            attempts++
            if (attempts < maxAttempts) {
              await new Promise(resolve => setTimeout(resolve, pollInterval))
              return await pollResult()
            } else {
              ElMessage.error('语音合成超时')
              return true
            }
          }
          
          await pollResult()
        } else {
          ElMessage.error('无法创建语音合成任务')
        }
      } catch (error) {
        console.error('TTS error:', error)
        ElMessage.error('请求语音接口失败')
      }
    }

    const loadSessions = async () => {
      try {
        const response = await api.get('/AI/chat/sessions')
        if (response.data && response.data.status_code === 1000 && Array.isArray(response.data.sessions)) {
          const sessionMap = {}
          response.data.sessions.forEach(s => {
            const sid = String(s.sessionId)
            sessionMap[sid] = {
              id: sid,
              name: s.name || `会话 ${sid}`,
              messages: [] // lazy load
            }
          })
          sessions.value = sessionMap
        }
      } catch (error) {
        console.error('Load sessions error:', error)
      }
    }

    const createNewSession = () => {
      currentSessionId.value = 'temp'
      tempSession.value = true
      currentMessages.value = []
      // focus input
      nextTick(() => {
        if (messageInput.value) messageInput.value.focus()
      })
    }

    const switchSession = async (sessionId) => {
      if (!sessionId) return
      currentSessionId.value = String(sessionId)
      tempSession.value = false

      // lazy load history if not present
      if (!sessions.value[sessionId].messages || sessions.value[sessionId].messages.length === 0) {
        try {
          const response = await api.post('/AI/chat/history', { sessionId: currentSessionId.value })
          if (response.data && response.data.status_code === 1000 && Array.isArray(response.data.history)) {
            const messages = response.data.history.map(item => ({
              role: item.is_user ? 'user' : 'assistant',
              content: item.content
            }))
            sessions.value[sessionId].messages = messages
          }
        } catch (err) {
          console.error('Load history error:', err)
        }
      }


      currentMessages.value = [...(sessions.value[sessionId].messages || [])]
      if (memoryPreviewOpen.value) await loadMemoryPreview()
      await nextTick()
      scrollToBottom()
    }

    const syncHistory = async () => {
      if (!currentSessionId.value || tempSession.value) {
        ElMessage.warning('请选择已有会话进行同步')
        return
      }
      try {
        const response = await api.post('/AI/chat/history', { sessionId: currentSessionId.value })
        if (response.data && response.data.status_code === 1000 && Array.isArray(response.data.history)) {
          const messages = response.data.history.map(item => ({
            role: item.is_user ? 'user' : 'assistant',
            content: item.content
          }))
          sessions.value[currentSessionId.value].messages = messages
          currentMessages.value = [...messages]
          await nextTick()
          scrollToBottom()
        } else {
          ElMessage.error('无法获取历史数据')
        }
      } catch (err) {
        console.error('Sync history error:', err)
        ElMessage.error('请求历史数据失败')
      }
    }


    const sendMessage = async () => {
      if (!inputMessage.value || !inputMessage.value.trim()) {
        ElMessage.warning('请输入消息内容')
        return
      }

      const userMessage = {
        role: 'user',
        content: inputMessage.value
      }
      const currentInput = inputMessage.value
      inputMessage.value = ''


      currentMessages.value.push(userMessage)
      await nextTick()
      scrollToBottom()

      try {
        loading.value = true
        if (diagnosticMode.value) {
          await handleDiagnostic(currentInput)
        } else if (isStreaming.value) {

          await handleStreaming(currentInput)
        } else {

          await handleNormal(currentInput)
        }
      } catch (err) {
        console.error('Send message error:', err)
        ElMessage.error('发送失败，请重试')

        if (!tempSession.value && currentSessionId.value && sessions.value[currentSessionId.value] && sessions.value[currentSessionId.value].messages) {

          const sessionArr = sessions.value[currentSessionId.value].messages
          if (sessionArr && sessionArr.length) sessionArr.pop()
        }
        currentMessages.value.pop()
      } finally {
        if (!isStreaming.value) {
          loading.value = false
        }
        await nextTick()
        scrollToBottom()
        if (memoryPreviewOpen.value && currentSessionId.value && !tempSession.value) await loadMemoryPreview()
      }
    }


    async function handleStreaming(question) {
      const aiMessage = {
        role: 'assistant',
        content: '',
        meta: { status: 'streaming', strategy: '固定路由', policyVersion: '加载中', traceId: '', intentShadow: null }
      }
      const aiMessageIndex = currentMessages.value.length
      currentMessages.value.push(aiMessage)

      if (!tempSession.value && currentSessionId.value && sessions.value[currentSessionId.value]) {
        if (!sessions.value[currentSessionId.value].messages) sessions.value[currentSessionId.value].messages = []
        sessions.value[currentSessionId.value].messages.push({ role: 'user', content: question })
        sessions.value[currentSessionId.value].messages.push({ role: 'assistant', content: '' })
      }

      const headers = {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${localStorage.getItem('token') || ''}`
      }
      const body = { message: question, knowledge_required: knowledgeRequired.value }
      if (!tempSession.value) body.session_id = currentSessionId.value

      try {
        const response = await fetch('/api/chat/auto/stream', {
          method: 'POST',
          headers,
          body: JSON.stringify(body)
        })

        if (!response.ok) {
          const errorBody = await response.json().catch(() => ({}))
          throw new Error(errorBody.message || 'Network response was not ok')
        }

        const reader = response.body.getReader()
        const decoder = new TextDecoder()
        let buffer = ''

        const processEventBlock = (block) => {
          let eventType = 'message'
          const dataLines = []
          block.split('\n').forEach(line => {
            if (line.startsWith('event:')) eventType = line.slice(6).trim()
            if (line.startsWith('data:')) dataLines.push(line.slice(5).trim())
          })
          if (!dataLines.length) return
          let payload
          try {
            payload = JSON.parse(dataLines.join('\n'))
          } catch {
            throw new Error('服务端返回了无法识别的流式事件')
          }

          const message = currentMessages.value[aiMessageIndex]
          if (eventType === 'meta') {
            message.meta = {
              status: 'streaming',
              traceId: payload.trace_id || '',
              strategy: payload.strategy || '固定路由',
              policyVersion: payload.policy_version || '',
              intentShadow: payload.intent_shadow || null
            }
            if (payload.session_id && tempSession.value) {
              const newSessionId = String(payload.session_id)
              sessions.value[newSessionId] = {
                id: newSessionId,
                name: question.slice(0, 30) || '新会话',
                messages: currentMessages.value
              }
              currentSessionId.value = newSessionId
              tempSession.value = false
            }
          } else if (eventType === 'delta') {
            message.content += payload.text || ''
          } else if (eventType === 'citation') {
            message.meta.citations = [...(message.meta.citations || []), payload.citation]
          } else if (eventType === 'final') {
            message.meta.status = 'done'
          } else if (eventType === 'error') {
            throw new Error(payload.error?.message || '流式生成失败')
          }
          currentMessages.value = [...currentMessages.value]
        }

        // eslint-disable-next-line no-constant-condition
        while (true) {
          const { done, value } = await reader.read()
          if (done) break
          buffer = (buffer + decoder.decode(value, { stream: true })).replace(/\r\n/g, '\n')
          const blocks = buffer.split('\n\n')
          buffer = blocks.pop() || ''
          blocks.filter(Boolean).forEach(processEventBlock)
          await nextTick()
          scrollToBottom()
        }
        if (buffer.trim()) processEventBlock(buffer.trim())

        loading.value = false
        currentMessages.value[aiMessageIndex].meta.status = 'done'
        currentMessages.value = [...currentMessages.value]

        // 同步到 sessions 存储
        if (!tempSession.value && currentSessionId.value && sessions.value[currentSessionId.value]) {
          const sessMsgs = sessions.value[currentSessionId.value].messages
          if (Array.isArray(sessMsgs) && sessMsgs.length) {
            const lastIndex = sessMsgs.length - 1
            if (sessMsgs[lastIndex] && sessMsgs[lastIndex].role === 'assistant') {
              sessMsgs[lastIndex].content = currentMessages.value[aiMessageIndex].content
            }
          }
        }
      } catch (err) {
        console.error('Stream error:', err)
        loading.value = false
        currentMessages.value[aiMessageIndex].meta.status = 'error'
        currentMessages.value = [...currentMessages.value]
        ElMessage.error(err.message || '流式传输出错')
      }
    }

    const newClientRequestId = () => {
      if (window.crypto && typeof window.crypto.randomUUID === 'function') return window.crypto.randomUUID()
      return `web-${Date.now()}-${Math.random().toString(16).slice(2)}`
    }

    const metricPercent = (value) => `${((Number(value) || 0) * 100).toFixed(1)}%`

    const memoryCacheLabel = (status) => ({
      hit: 'Redis 热窗口命中',
      rebuilt_from_mysql: '已从 MySQL 重建 Redis',
      mysql_fallback_cache_unavailable: 'Redis 不可用，MySQL 降级'
    }[status] || status)

    const memoryContextKindLabel = (kind) => ({
      safety_rule: '系统安全规则',
      current_question: '当前问题',
      constraint: '用户明确约束',
      run_state: '当前 Run 状态',
      confirmed_fact: '已确认事实',
      open_question: '未决问题',
      evidence_ref: '证据引用',
      structured_summary: '结构化摘要',
      profile_memory: '已确认环境事实',
      working_message: '近期原始消息'
    }[kind] || kind)

    const profileKeyLabel = (key) => ({
      os: '操作系统',
      go_version: 'Go 版本',
      deployment_mode: '部署方式',
      cloud_provider: '云环境',
      redis_version: 'Redis 版本',
      mysql_version: 'MySQL 版本'
    }[key] || key)

    const profileStatusLabel = (status) => ({ active: '已确认', candidate: '待确认', conflicted: '存在冲突' }[status] || status)
    const profileSourceLabel = (source) => ({ diagnostic_observation: '诊断输入提取', user_corrected: '用户确认/更正' }[source] || source)

    const loadMemoryPreview = async () => {
      try {
        loadingMemoryPreview.value = true
        const profileResponse = await api.get('/memory/profiles')
        profileMemories.value = profileResponse.data
        profileDrafts.value = Object.fromEntries((profileResponse.data.items || []).map(item => [item.id, item.value]))
        if (currentSessionId.value && !tempSession.value) {
          const response = await api.get(`/memory/sessions/${encodeURIComponent(currentSessionId.value)}/context`, {
            params: { budget_tokens: 1024 }
          })
          memoryPreview.value = response.data
        } else {
          memoryPreview.value = null
        }
      } catch (error) {
        memoryPreviewOpen.value = false
        ElMessage.error(error.response?.data?.message || '上下文记忆暂时不可用')
      } finally {
        loadingMemoryPreview.value = false
      }
    }

    const correctProfileMemory = async (item) => {
      try {
        profileMemoryBusy.value = item.id
        await api.patch(`/memory/profiles/${encodeURIComponent(item.id)}`, {
          value: profileDrafts.value[item.id],
          expires_in_days: 180
        })
        await loadMemoryPreview()
        ElMessage.success('环境事实已由你确认并启用，旧值不会继续生效')
      } catch (error) {
        ElMessage.error(error.response?.data?.message || '环境记忆确认失败')
      } finally {
        profileMemoryBusy.value = ''
      }
    }

    const deleteProfileMemory = async (item) => {
      if (!window.confirm(`确认删除环境记忆“${profileKeyLabel(item.key)}：${item.value}”吗？`)) return
      try {
        profileMemoryBusy.value = item.id
        await api.delete(`/memory/profiles/${encodeURIComponent(item.id)}`)
        await loadMemoryPreview()
        ElMessage.success('环境记忆已从 MySQL 权威记录删除')
      } catch (error) {
        ElMessage.error(error.response?.data?.message || '环境记忆删除失败')
      } finally {
        profileMemoryBusy.value = ''
      }
    }

    const toggleMemoryPreview = async () => {
      const opening = !memoryPreviewOpen.value
      closeUtilityWorkspaces(opening ? 'memory' : '')
      memoryPreviewOpen.value = opening
      if (memoryPreviewOpen.value) await loadMemoryPreview()
    }

    const rebuildWorkingMemory = async () => {
      if (!currentSessionId.value || tempSession.value) return
      try {
        loadingMemoryPreview.value = true
        const rebuildResponse = await api.post(`/memory/sessions/${encodeURIComponent(currentSessionId.value)}/rebuild`)
        await loadMemoryPreview()
        if (memoryPreview.value?.window && rebuildResponse.data?.window?.cache_status) {
          memoryPreview.value.window.cache_status = rebuildResponse.data.window.cache_status
        }
        ElMessage.success('已删除当前用户该会话的 Redis 缓存，并从 MySQL 权威消息重建')
      } catch (error) {
        ElMessage.error(error.response?.data?.message || '工作记忆重建失败')
      } finally {
        loadingMemoryPreview.value = false
      }
    }

    const toggleDiagnosticEvaluation = async () => {
      diagnosticEvaluationOpen.value = !diagnosticEvaluationOpen.value
      if (!diagnosticEvaluationOpen.value || diagnosticEvaluation.value || loadingDiagnosticEvaluation.value) return
      try {
        loadingDiagnosticEvaluation.value = true
        const response = await api.get('/evaluations/diagnostic/latest')
        diagnosticEvaluation.value = response.data
      } catch (error) {
        diagnosticEvaluationOpen.value = false
        ElMessage.error(error.response?.data?.message || '诊断评测报告暂时不可用')
      } finally {
        loadingDiagnosticEvaluation.value = false
      }
    }

    const toggleContextCompression = async () => {
      contextCompressionOpen.value = !contextCompressionOpen.value
      if (!contextCompressionOpen.value || loadingContextCompression.value) return
      if (!activeDiagnosticRun.value?.run?.run_id) {
        contextCompressionOpen.value = false
        return
      }
      try {
        loadingContextCompression.value = true
        const [contextResponse, evaluationResponse] = await Promise.all([
          api.get(`/agent-runs/${activeDiagnosticRun.value.run.run_id}/context-compression`, { params: { budget_tokens: 512 } }),
          api.get('/evaluations/context/latest').catch(() => null)
        ])
        contextCompression.value = contextResponse.data
        contextEvaluation.value = evaluationResponse?.data || null
      } catch (error) {
        contextCompressionOpen.value = false
        ElMessage.error(error.response?.data?.message || '结构化上下文暂时不可用')
      } finally {
        loadingContextCompression.value = false
      }
    }

    const formattedFacts = (facts) => {
      const entries = Object.entries(facts || {})
      if (!entries.length) return '无'
      return entries.sort(([left], [right]) => left.localeCompare(right)).map(([key, value]) => `${key}=${value}`).join('；')
    }

    const toggleToolRuntime = async () => {
      const opening = !toolRuntimeOpen.value
      closeUtilityWorkspaces(opening ? 'tools' : '')
      toolRuntimeOpen.value = opening
      if (!toolRuntimeOpen.value || toolCatalog.value || loadingToolCatalog.value) return
      try {
        loadingToolCatalog.value = true
        const response = await api.get('/tools')
        toolCatalog.value = response.data
      } catch (error) {
        toolRuntimeOpen.value = false
        ElMessage.error(error.response?.data?.message || '工具注册表暂时不可用')
      } finally {
        loadingToolCatalog.value = false
      }
    }

    const invokeGovernedTool = async (toolName, argumentsPayload = {}) => {
      try {
        invokingTool.value = true
        toolResult.value = null
        const response = await api.post('/tools/invoke', { tool_name: toolName, arguments: argumentsPayload, intent: 'tool_task' })
        toolResult.value = response.data
        ElMessage.success(`${toolName} 已通过完整治理链路返回`)
      } catch (error) {
        toolResult.value = error.response?.data || { status: 'error', error_code: 'TOOL_CONSOLE_REQUEST_FAILED', retryable: true }
        ElMessage.error(toolResult.value?.message || `工具调用失败：${toolResult.value?.error_code || '未知错误'}`)
      } finally {
        invokingTool.value = false
      }
    }

    const formatToolData = (data) => JSON.stringify(data, null, 2)

    const runToolAgent = async () => {
      if (!toolAgentQuery.value.trim()) return
      try {
        runningToolAgent.value = true
        toolAgentResult.value = null
        const response = await api.post('/tools/agent', { message: toolAgentQuery.value.trim() })
        toolAgentResult.value = response.data
        if (response.data.status === 'succeeded') ElMessage.success('ToolAgent 已按有限计划返回证据')
        else if (response.data.plan?.decision === 'refuse') ElMessage.warning('危险动作已在规划层拒绝，未调用工具')
        else ElMessage.info('该问题不需要当前受治理工具')
      } catch (error) {
        ElMessage.error(error.response?.data?.message || 'ToolAgent 暂时不可用')
      } finally {
        runningToolAgent.value = false
      }
    }

    const toggleToolEvaluation = async () => {
      toolEvaluationOpen.value = !toolEvaluationOpen.value
      if (!toolEvaluationOpen.value || toolEvaluation.value || loadingToolEvaluation.value) return
      try {
        loadingToolEvaluation.value = true
        const response = await api.get('/evaluations/tools/latest')
        toolEvaluation.value = response.data
      } catch (error) {
        toolEvaluationOpen.value = false
        ElMessage.error(error.response?.data?.message || '工具评测报告暂时不可用')
      } finally {
        loadingToolEvaluation.value = false
      }
    }

    const toggleMemoryEvaluation = async () => {
      memoryEvaluationOpen.value = !memoryEvaluationOpen.value
      if (!memoryEvaluationOpen.value || memoryEvaluation.value || loadingMemoryEvaluation.value) return
      try {
        loadingMemoryEvaluation.value = true
        const response = await api.get('/evaluations/memory/latest')
        memoryEvaluation.value = response.data
      } catch (error) {
        memoryEvaluationOpen.value = false
        ElMessage.error(error.response?.data?.message || '记忆评测报告暂时不可用')
      } finally {
        loadingMemoryEvaluation.value = false
      }
    }

    const togglePolicyControl = async () => {
      const opening = !policyControlOpen.value
      closeUtilityWorkspaces(opening ? 'policy' : '')
      policyControlOpen.value = opening
      if (!policyControlOpen.value || policySnapshot.value || loadingPolicyControl.value) return
      try {
        loadingPolicyControl.value = true
        const response = await api.get('/policies/active')
        policySnapshot.value = response.data
      } catch (error) {
        policyControlOpen.value = false
        ElMessage.error(error.response?.data?.message || '当前策略暂时不可读取')
      } finally {
        loadingPolicyControl.value = false
      }
    }

    const simulatePolicy = async (intent) => {
      try {
        selectedStrategyIntent.value = intent
        simulatingPolicy.value = true
        policySimulation.value = null
        const response = await api.post('/policies/simulate', { intent })
        policySimulation.value = response.data
        ElMessage.success('Shadow 演算完成，真实对话流量未改变')
      } catch (error) {
        ElMessage.error(error.response?.data?.message || '策略演算暂时不可用')
      } finally {
        simulatingPolicy.value = false
      }
    }

    const runCaseShadow = async () => {
      if (!caseShadowMessage.value.trim()) return
      try {
        runningCaseShadow.value = true
        caseShadowResult.value = null
        const response = await api.post('/agent-runs/diagnostics/case-shadow', { message: caseShadowMessage.value.trim() })
        caseShadowResult.value = response.data
        if (response.data.case_strength === 'strong') ElMessage.success('命中强案例候选；仍保持 Shadow，不影响线上诊断')
        else ElMessage.info('未达到强匹配门槛，保持标准诊断')
      } catch (error) {
        ElMessage.error(error.response?.data?.message || '案例增强演算暂时不可用')
      } finally {
        runningCaseShadow.value = false
      }
    }

    const runCollaborationPlan = async () => {
      if (!collaborationPlanMessage.value.trim()) return
      try {
        planningCollaboration.value = true
        collaborationPlan.value = null
        const response = await api.post('/agent-runs/diagnostics/collaboration-plan', { message: collaborationPlanMessage.value.trim() })
        collaborationPlan.value = response.data
        if (response.data.decision === 'collaborative_candidate') ElMessage.success('满足协作候选门；本次仍只生成计划，不执行 Agent')
        else ElMessage.info('复杂度不足，保持单 DiagnosticAgent 基线')
      } catch (error) {
        ElMessage.error(error.response?.data?.message || '协作规划暂时不可用')
      } finally {
        planningCollaboration.value = false
      }
    }

    const runCollaborationShadow = async () => {
      if (!collaborationPlanMessage.value.trim()) return
      try {
        runningCollaboration.value = true
        collaborationRun.value = null
        const response = await api.post('/agent-runs/diagnostics/collaboration-shadow', { message: collaborationPlanMessage.value.trim() })
        collaborationRun.value = response.data
        collaborationPlan.value = response.data.plan
        if (!response.data.executed) ElMessage.info('规划门判定协作收益不足，未启动子 Agent')
        else if (response.data.status === 'complete') ElMessage.success('两个 Agent 已完成，且所有合成结论均通过引用校验')
        else ElMessage.warning('协作 Shadow 已完成，但存在降级、证据不足或冲突，请查看结果')
      } catch (error) {
        ElMessage.error(error.response?.data?.message || '协作 Shadow 暂时不可用')
      } finally {
        runningCollaboration.value = false
      }
    }

    const toggleCollaborationEvaluation = async () => {
      collaborationEvaluationOpen.value = !collaborationEvaluationOpen.value
      if (!collaborationEvaluationOpen.value || collaborationEvaluation.value) return
      try {
        loadingCollaborationEvaluation.value = true
        const response = await api.get('/evaluations/collaboration/latest')
        collaborationEvaluation.value = response.data
      } catch (error) {
        collaborationEvaluationOpen.value = false
        ElMessage.error(error.response?.data?.message || '多 Agent 成对评测报告暂时不可用')
      } finally {
        loadingCollaborationEvaluation.value = false
      }
    }

    const policySourceLabel = (source) => ({ redis: 'Redis 缓存', mysql: 'MySQL 权威源' }[source] || source)
    const shortPolicyHash = (hash) => hash ? `${hash.slice(0, 10)}…${hash.slice(-6)}` : 'unknown'
    const strategyIntentLabel = (intent) => ({ troubleshooting: '故障诊断', project_qa: '项目知识问答', general: '通用问答' }[intent] || intent)
    const strategyReasonLabel = (reason) => ({
      stable_weighted_selection: '稳定权重命中',
      dependency_filtered_selection: '过滤异常依赖后命中',
      dependency_fallback: '依赖异常安全降级'
    }[reason] || reason)
    const strategyDependencyLabel = (dependency) => ({ model: '回答模型', vector: '向量检索', tool: '受治理工具', case_memory: '案例记忆' }[dependency] || dependency)
    const strategyStateLabel = (state) => ({ active: '可参与演算', shadow: '仅影子候选', disabled: '禁用' }[state] || state)
    const caseStrengthLabel = (strength) => ({ strong: '强匹配', weak: '弱匹配，仅参考', none: '无匹配' }[strength] || strength)
    const caseMemoryStatusLabel = (status) => ({ hit: '命中', no_match: '未命中', unavailable: '不可用，已降级' }[status] || status)
    const caseReasonLabel = (reason) => ({
      strong_case_prioritization_candidate: '强案例与基线假设一致',
      case_match_advisory_only: '相似度不足，仅作参考',
      strong_case_without_baseline_hypothesis: '强案例未获基线证据支持',
      case_no_match: '没有可用的确认案例',
      case_recall_unavailable: '案例召回异常，已降级',
      case_payload_invalid: '案例载荷校验失败，已降级'
    }[reason] || reason)
    const collaborationDecisionLabel = (decision) => ({ single_agent: '保持单 Agent', collaborative_candidate: '候选：KnowledgeAgent + DiagnosticAgent' }[decision] || decision)
    const collaborationReasonLabel = (reason) => ({
      single_task_preferred: '单一故障，协作收益不足',
      independent_diagnostic_branches: '存在两个独立故障域',
      knowledge_diagnostic_split: '证据核对与诊断可独立执行',
      conflict_requires_evidence_verification: '冲突需要独立证据核验'
    }[reason] || reason)
    const collaborationRunStatusLabel = (status) => ({
      not_executed: '保持单 Agent：未执行',
      complete: '协作完成：引用校验通过',
      partial: '部分完成：已显式降级',
      conflict: '发现证据冲突：不静默选边',
      insufficient: '证据不足：回退标准诊断',
      failed: '协作执行失败',
      cancelled: '协作已取消'
    }[status] || status)
    const collaborationRunReasonLabel = (reason) => ({
      single_agent_gate: '简单请求不值得启动多 Agent',
      all_claims_citation_verified: '全部 Claim 都能追溯到证据',
      partial_agent_results: '仅保留成功 Agent 的可验证结论',
      evidence_conflict_requires_user_review: '有效证据相互冲突，需要人工核对',
      no_supported_claims: '没有通过引用校验的结论'
    }[reason] || reason)
    const collaborationTaskStatusLabel = (status) => ({
      succeeded: '成功', insufficient: '证据不足', failed: '失败', timed_out: '超时', cancelled: '取消', budget_exceeded: '超预算'
    }[status] || status)

    const diagnosticStateLabel = (state) => ({
      RECEIVED: '已接收',
      CONTEXT_READY: '上下文就绪',
      PLANNED: '计划完成',
      RUNNING: '诊断执行中',
      WAITING_USER: '等待补充信息',
      SUCCEEDED: '已形成诊断假设',
      FAILED: '运行失败',
      CANCELLED: '已取消',
      BUDGET_EXCEEDED: '预算终止'
    }[state] || state)

    const isDiagnosticTerminal = (state) => ['SUCCEEDED', 'FAILED', 'CANCELLED', 'BUDGET_EXCEEDED'].includes(state)

    const incidentIndexLabel = (status) => ({
      pending: '等待可靠索引',
      indexed: 'RabbitMQ 异步索引完成',
      failed: '索引失败，可由 Outbox/重试恢复'
    }[status] || status)

    const previewResolution = async (hypothesisId) => {
      if (!activeDiagnosticRun.value?.run?.run_id) return
      try {
        loadingResolution.value = true
        const response = await api.post(`/agent-runs/${activeDiagnosticRun.value.run.run_id}/resolution-proposals`, {
          hypothesis_id: hypothesisId
        })
        resolutionProposal.value = response.data
        resolutionText.value = ''
        resolutionAcknowledged.value = false
      } catch (error) {
        ElMessage.error(error.response?.data?.message || '解决案例预览失败')
      } finally {
        loadingResolution.value = false
      }
    }

    const closeResolutionProposal = () => {
      resolutionProposal.value = null
      resolutionText.value = ''
      resolutionAcknowledged.value = false
    }

    const loadConfirmedResolution = async () => {
      if (!activeDiagnosticRun.value?.run?.run_id) return
      try {
        const response = await api.get(`/agent-runs/${activeDiagnosticRun.value.run.run_id}/resolution`)
        confirmedResolution.value = response.data
        resolutionProposal.value = null
      } catch (error) {
        if (error.response?.status !== 404) ElMessage.warning(error.response?.data?.message || '已解决案例状态暂时不可用')
        confirmedResolution.value = null
      }
    }

    const confirmResolution = async () => {
      if (!resolutionProposal.value || !resolutionAcknowledged.value || resolutionText.value.trim().length < 5) return
      try {
        loadingResolution.value = true
        const response = await api.post(`/agent-runs/${resolutionProposal.value.run_id}/resolution-confirmations`, {
          hypothesis_id: resolutionProposal.value.hypothesis_id,
          resolution: resolutionText.value,
          client_request_id: newClientRequestId(),
          expected_state_version: resolutionProposal.value.expected_state_version
        })
        confirmedResolution.value = response.data.incident
        resolutionProposal.value = null
        ElMessage.success(response.data.created ? '已确认解决，案例正在通过 Outbox 异步建立索引' : '该确认已处理，没有重复写入')
        window.setTimeout(loadConfirmedResolution, 1500)
      } catch (error) {
        ElMessage.error(error.response?.data?.message || '确认解决方案失败')
      } finally {
        loadingResolution.value = false
      }
    }

    const diagnosticMessage = (payload) => {
      const state = payload.run?.state
      const result = payload.result
      if (state === 'WAITING_USER') {
        const questions = payload.checkpoint?.open_questions || result?.missing_information?.map(item => item.question) || []
        return `诊断 Run 已暂停，当前证据不足。\n\n请补充：\n${questions.map((item, index) => `${index + 1}. ${item}`).join('\n')}`
      }
      if (state === 'CANCELLED') return '诊断 Run 已取消，后续步骤不会继续执行。'
      if (state === 'BUDGET_EXCEEDED') return `诊断 Run 已被护栏终止：${payload.run?.terminal_reason || '预算已耗尽'}。`
      const hypotheses = result?.hypotheses || []
      if (!hypotheses.length) return `诊断运行状态：${diagnosticStateLabel(state)}。`
      return `已形成 ${hypotheses.length} 个有证据的待验证假设。完整依据、只读验证步骤和公开执行轨迹见上方“可恢复故障诊断”面板。当前没有把任何假设标记为已确认根因。`
    }

    async function handleDiagnostic(message) {
      let response
      if (activeDiagnosticRun.value?.run?.state === 'WAITING_USER') {
        response = await api.post(`/agent-runs/${activeDiagnosticRun.value.run.run_id}/resume`, {
          message,
          client_request_id: newClientRequestId(),
          expected_state_version: activeDiagnosticRun.value.run.state_version
        })
      } else {
        response = await api.post('/agent-runs/diagnostics', {
          message,
          session_id: tempSession.value ? '' : currentSessionId.value,
          client_request_id: newClientRequestId()
        })
      }
      activeDiagnosticRun.value = response.data
      confirmedResolution.value = null
      resolutionProposal.value = null
      diagnosticRecovered.value = false
      rememberDiagnosticRun(response.data)
      currentMessages.value.push({
        role: 'assistant',
        content: diagnosticMessage(response.data),
        meta: {
          status: 'done',
          traceId: response.data.run?.trace_id || '',
          strategy: response.data.run?.strategy || 'diagnosis_standard',
          policyVersion: response.data.run?.policy_version || 'policy-diagnostic-v1',
          diagnosticRunId: response.data.run?.run_id || ''
        }
      })
    }

    const cancelDiagnosticRun = async () => {
      if (!activeDiagnosticRun.value?.run?.run_id) return
      try {
        loading.value = true
        const response = await api.post(`/agent-runs/${activeDiagnosticRun.value.run.run_id}/cancel`)
        activeDiagnosticRun.value = response.data
        rememberDiagnosticRun(response.data)
        ElMessage.success('诊断运行已取消')
      } catch (error) {
        ElMessage.error(error.response?.data?.message || '取消诊断运行失败')
      } finally {
        loading.value = false
      }
    }

    const resetDiagnosticRun = () => {
      activeDiagnosticRun.value = null
      contextCompressionOpen.value = false
      contextCompression.value = null
      contextEvaluation.value = null
      confirmedResolution.value = null
      resolutionProposal.value = null
      diagnosticRecovered.value = false
      sessionStorage.removeItem(diagnosticRunStorageKey)
      nextTick(() => messageInput.value?.focus())
    }

    const rememberDiagnosticRun = (payload) => {
      const runId = payload?.run?.run_id
      if (runId) sessionStorage.setItem(diagnosticRunStorageKey, runId)
    }

    const restoreDiagnosticRun = async () => {
      const runId = sessionStorage.getItem(diagnosticRunStorageKey)
      if (!runId || activeDiagnosticRun.value || restoringDiagnosticRun.value) return
      try {
        restoringDiagnosticRun.value = true
        const response = await api.get(`/agent-runs/${encodeURIComponent(runId)}`)
        activeDiagnosticRun.value = response.data
        diagnosticRecovered.value = true
        await loadConfirmedResolution()
      } catch (error) {
        if (error.response?.status === 404) sessionStorage.removeItem(diagnosticRunStorageKey)
        ElMessage.warning(error.response?.data?.message || '上次诊断 Run 暂时无法恢复，可稍后重新打开诊断模式')
      } finally {
        restoringDiagnosticRun.value = false
      }
    }

    const onDiagnosticModeChanged = async () => {
      sessionStorage.setItem(diagnosticModeStorageKey, diagnosticMode.value ? '1' : '0')
      if (diagnosticMode.value) {
        isStreaming.value = false
        knowledgeRequired.value = false
        await restoreDiagnosticRun()
      }
    }


    async function handleNormal(question) {
      if (tempSession.value) {
        const response = await api.post('/chat/auto', { message: question, knowledge_required: knowledgeRequired.value })
        if (response.data && response.data.session_id) {
          const sessionId = String(response.data.session_id)
          const aiMessage = {
            role: 'assistant',
            content: response.data.message || '',
            meta: {
              status: 'done', traceId: response.data.trace_id || '',
              strategy: response.data.strategy || '固定路由', policyVersion: response.data.policy_version || '',
              intentShadow: response.data.intent_shadow || null,
              citations: response.data.citations || [], needsUserInput: response.data.needs_user_input || false
            }
          }

          sessions.value[sessionId] = {
            id: sessionId,
            name: question.slice(0, 30) || '新会话',
            messages: [ { role: 'user', content: question }, aiMessage ]
          }
          currentSessionId.value = sessionId
          tempSession.value = false
          currentMessages.value = [...sessions.value[sessionId].messages]
        } else {
          ElMessage.error(response.data?.message || '发送失败')

          currentMessages.value.pop()
        }
      } else {

        const sessionMsgs = sessions.value[currentSessionId.value].messages

        sessionMsgs.push({ role: 'user', content: question })

        const response = await api.post('/chat/auto', {
          message: question,
          session_id: currentSessionId.value,
          knowledge_required: knowledgeRequired.value
        })
        if (response.data && response.data.session_id) {
          const aiMessage = {
            role: 'assistant',
            content: response.data.message || '',
            meta: {
              status: 'done', traceId: response.data.trace_id || '',
              strategy: response.data.strategy || '固定路由', policyVersion: response.data.policy_version || '',
              intentShadow: response.data.intent_shadow || null,
              citations: response.data.citations || [], needsUserInput: response.data.needs_user_input || false
            }
          }
          sessionMsgs.push(aiMessage)
          currentMessages.value = [...sessionMsgs]
        } else {
          ElMessage.error(response.data?.message || '发送失败')
          sessionMsgs.pop() // rollback
          currentMessages.value.pop()
        }
      }
    }


    const intentLabel = (intent) => ({
      project_qa: '项目知识问答',
      troubleshooting: '故障排查',
      doc_task: '文档任务',
      tool_task: '受治理的操作任务',
      follow_up: '上下文追问',
      general: '通用对话'
    }[intent] || '未知意图')

    const intentStageLabel = (stage) => ({
      pattern: '规则高置信命中',
      prototype: '语义原型匹配',
      llm: '结构化模型判定',
      degraded_clarification: '安全降级',
      unavailable: '识别器暂不可用'
    }[stage] || '未知阶段')

    const scrollToBottom = () => {
      if (messagesRef.value) {
        try {
          messagesRef.value.scrollTop = messagesRef.value.scrollHeight
        } catch (e) {
          // ignore
        }
      }
    }

    const triggerFileUpload = () => {
      if (fileInput.value) {
        fileInput.value.click()
      }
    }

    const documentStatusLabel = (status) => {
      const labels = {
        uploaded: '已接收，等待索引',
        parsing: '正在解析与索引',
        indexed: '索引完成，可检索',
        failed: '索引失败'
      }
      return labels[status] || status
    }

  const jobStatusLabel = (status) => {
    const labels = {
    queued: '等待索引',
    processing: '正在建立索引',
    retrying: '索引重试中',
    completed: '已切换为活动版本',
    failed: '索引失败'
    }
    return labels[status] || status
  }

    const retrievalModeLabel = (mode) => {
      const labels = {
        hybrid: 'Dense + BM25 混合检索',
        dense_only: 'Dense 降级检索',
        bm25_only: 'BM25 降级检索'
      }
      return labels[mode] || '检索状态未知'
    }

    const queryComplexityLabel = (complexity) => ({ simple: '简单', complex: '复杂' }[complexity] || complexity)

    const queryGapLabel = (gap) => ({ none: '无', soft: '轻微', hard: '明显' }[gap] || gap)

    const queryReasonLabel = (reason) => {
      const labels = {
        multi_part_query: '包含多个子问题',
        comparison_query: '需要比较或权衡',
        cross_document_query: '明确要求跨文档',
        causal_query: '包含因果问题',
        analytical_query: '需要分析或诊断',
        long_query: '问题较长',
        ambiguous_reference: '存在缺少上下文的指代',
        no_evidence: '没有召回证据',
        retrieval_degraded: '检索器发生降级',
        single_retriever_evidence: '证据仅由单路检索支持',
        low_top_score: '最高证据分数偏低',
        cross_document_evidence_gap: '跨文档证据覆盖不足',
        weak_rank_separation: '多个来源排名接近',
        simple_query_high_confidence: '简单问题且证据置信度足够'
      }
      return labels[reason] || reason
    }

    const deepOutcomeLabel = (deep) => {
      if (!deep.activated) return '问题简单且证据充分，智能跳过额外模型调用'
      return ({ completed: '增强链路完成', partial_fallback: '部分增强失败，已安全回退' }[deep.outcome] || deep.outcome)
    }

    const enhancementOutcomeLabel = (outcome) => {
      const labels = {
        rewrite_not_required: '未触发',
        rewrite_completed: '完成',
        rewrite_model_error: '模型错误，已回退',
        rewrite_timeout: '超时，已回退',
        rewrite_invalid_output: '输出无效，已回退',
        rerank_not_required: '未触发',
        rerank_completed: '完成',
        rerank_model_error: '模型错误，已回退',
        rerank_timeout: '超时，已回退',
        rerank_invalid_output: '输出无效，已回退'
      }
      return labels[outcome] || '未触发'
    }

    const toggleKnowledgeSearch = () => {
      const opening = !knowledgeSearchOpen.value
      closeUtilityWorkspaces(opening ? 'knowledge' : '')
      knowledgeSearchOpen.value = opening
      if (knowledgeSearchOpen.value && !knowledgeQuery.value.trim() && inputMessage.value.trim()) {
        knowledgeQuery.value = inputMessage.value.trim()
      }
    }

    const searchKnowledge = async () => {
      const query = knowledgeQuery.value.trim()
      if (!query || searchingKnowledge.value) return
      searchingKnowledge.value = true
      knowledgeAnswer.value = null
      knowledgeSearchResults.value = []
      knowledgeSearchDiagnostics.value = null
      knowledgeSearchConflicts.value = []
      try {
        const response = await api.post('/knowledge/search', { query, top_k: 5 })
        knowledgeSearchResults.value = response.data?.hits || []
        knowledgeSearchDiagnostics.value = response.data?.diagnostics || null
        knowledgeSearchConflicts.value = response.data?.conflicts || []
      } catch (error) {
        console.error('Knowledge search error:', error)
        ElMessage.error(error.response?.data?.message || '知识检索暂时不可用')
      } finally {
        searchingKnowledge.value = false
      }
    }

    const answerKnowledge = async (mode = false) => {
      const question = knowledgeQuery.value.trim()
      if (!question || answeringKnowledge.value) return
      answeringKnowledge.value = true
      answeringKnowledgeMode.value = mode === 'parent' ? 'parent' : (mode ? 'deep' : 'fast')
      knowledgeAnswer.value = null
      try {
        const endpoint = mode === 'parent' ? '/knowledge/parent-answer' : (mode ? '/knowledge/deep-answer' : '/knowledge/answer')
        const response = await api.post(endpoint, { question, top_k: 5 })
        knowledgeAnswer.value = response.data || null
        if (knowledgeAnswer.value?.result?.resolved) {
          ElMessage.success('回答已通过证据门和引用校验')
        } else {
          ElMessage.warning('证据不足，系统未调用模型生成结论')
        }
      } catch (error) {
        console.error('Knowledge answer error:', error)
        ElMessage.error(error.response?.data?.message || '知识库回答暂时不可用')
      } finally {
        answeringKnowledge.value = false
        answeringKnowledgeMode.value = ''
      }
    }

    const toggleParentContextEvaluation = async () => {
      parentContextEvaluationOpen.value = !parentContextEvaluationOpen.value
      if (!parentContextEvaluationOpen.value || parentContextEvaluation.value || loadingParentContextEvaluation.value) return
      try {
        loadingParentContextEvaluation.value = true
        const response = await api.get('/evaluations/parent-context/latest')
        parentContextEvaluation.value = response.data
      } catch (error) {
        parentContextEvaluationOpen.value = false
        ElMessage.error(error.response?.data?.message || '父子上下文成对评测报告暂时不可用')
      } finally {
        loadingParentContextEvaluation.value = false
      }
    }

    const toggleEvaluationCatalog = async () => {
      const opening = !evaluationCatalogOpen.value
      closeUtilityWorkspaces(opening ? 'evaluation' : '')
      evaluationCatalogOpen.value = opening
      if (!evaluationCatalogOpen.value || evaluationCatalog.value || loadingEvaluationCatalog.value) return
      try {
        loadingEvaluationCatalog.value = true
        const [catalogResponse, runResponse, metricCatalogResponse] = await Promise.all([
          api.get('/evaluations/catalog/latest'),
          api.get('/evaluations/unified/latest'),
          api.get('/evaluations/metrics/catalog')
        ])
        evaluationCatalog.value = catalogResponse.data
        evaluationRun.value = runResponse.data
        metricCatalog.value = metricCatalogResponse.data.report
      } catch (error) {
        evaluationCatalogOpen.value = false
        ElMessage.error(error.response?.data?.message || '评测数据目录暂时不可用')
      } finally {
        loadingEvaluationCatalog.value = false
      }
    }

    const evaluationSliceLabel = (slice) => ({
      intent: '意图识别', rag: 'RAG', diagnosis: '故障诊断', tool: '工具治理', memory: '三级记忆', insufficient_evidence: '证据不足'
    }[slice] || slice)

    const evaluationStatusLabel = (status) => ({
      technical_candidate: '技术候选 · 不可切流', rejected: '技术门拒绝', baseline_eligible: '可冻结基线 · 仍不可自动切流'
    }[status] || status)

    const evaluationFailureLabel = (code) => ({
      misclassification: '普通误分类', severe_misroute: '严重误路由', citation_gap: '引用覆盖缺口',
      verification_gap: '验证步骤缺口', retrieval_miss: '检索漏召回', unsupported_answer: '无证据作答',
      runtime_error: '运行错误', unsafe_action: '危险动作', nondeterministic_replay: '重放不一致'
    }[code] || code)

    const metricDomainLabel = (domain) => ({
      platform: '平台入口', intent: '意图识别', knowledge_rag: '知识与 RAG', agent_harness: 'Agent Harness',
      memory: '三级记忆', tool_governance: '工具治理', multi_agent: '多 Agent', evaluation: '评测与反馈', control_plane: '策略控制面'
    }[domain] || domain)

    const metricTypeLabel = (type) => ({ counter: 'Counter', histogram: 'Histogram', gauge: 'Gauge' }[type] || type)

    const anomalyMetricLabel = (metric) => ({
      rag_grounded_answer_rate: 'RAG 有依据回答率', request_p95_latency_seconds: '请求 P95 延迟'
    }[metric] || metric)

    const anomalyRecommendationLabel = (action) => ({
      none: '不产生策略建议', reduce_candidate_weight: '建议候选策略降权（未执行）'
    }[action] || action)

    const anomalyDecisionLabel = (status) => ({
      anomalous: '检测到退化', healthy: '窗口健康', insufficient_data: '数据不足 · 暂不判定'
    }[status] || '状态未知')

    const anomalyDecisionClass = (analysis) => ({
      anomalous: 'anomaly-detected', healthy: 'anomaly-healthy', insufficient_data: 'anomaly-insufficient'
    }[analysis?.decision_status] || 'anomaly-insufficient')

    const anomalySignalStatusLabel = (status) => ({
      suppressed: '已抑制', healthy: '健康', anomalous: '异常', warning: '告警', critical: '严重', insufficient_window: '窗口不足'
    }[status] || status)

    const simulateAnomaly = async (scenario) => {
      if (loadingAnomaly.value) return
      try {
        loadingAnomaly.value = true
        anomalyResult.value = null
        const response = await api.post('/evaluations/anomaly/simulate', { scenario })
        anomalyResult.value = response.data
        if (response.data?.analysis?.anomalous) {
          ElMessage.warning('检测到退化，但只生成建议，没有修改线上策略')
        } else if (response.data?.analysis?.fixed_threshold?.status === 'suppressed') {
          ElMessage.info('样本量不足，检测器按门禁抑制告警')
        } else {
          ElMessage.success('窗口健康，不产生策略建议')
        }
      } catch (error) {
        ElMessage.error(error.response?.data?.message || '异常检测验收暂时不可用')
      } finally {
        loadingAnomaly.value = false
      }
    }

    const evidenceForCitation = (citation) => {
      const evidence = knowledgeAnswer.value?.result?.evidence || []
      return evidence.find(item => item.id === citation.evidence_id) || {}
    }

    const sourceKindLabel = (kind) => ({
      upload: '用户上传',
      repository: '代码仓库',
      legacy_upload: '历史上传'
    }[kind] || kind || '历史来源')

    const shortRevision = (revision) => {
      const value = String(revision || '').trim()
      if (!value) return '未记录'
      return value.length > 12 ? value.slice(0, 12) : value
    }

    const loadKnowledgeDocuments = async () => {
      try {
        const response = await api.get('/knowledge/documents')
        knowledgeDocuments.value = response.data?.documents || []
        if (!indexedKnowledgeDocuments.value.some(document => document.id === versionTargetDocumentId.value)) {
          versionTargetDocumentId.value = indexedKnowledgeDocuments.value[0]?.id || ''
        }
        const hasPendingDocument = knowledgeDocuments.value.some(document =>
          document.status === 'uploaded' || document.status === 'parsing'
        )
        if (hasPendingDocument || pendingVersionJob.value) {
          startKnowledgePolling()
        } else {
          stopKnowledgePolling()
        }
      } catch (error) {
        console.error('Load knowledge documents error:', error)
      }
    }

    const startKnowledgePolling = () => {
      if (!knowledgePollTimer) {
        knowledgePollTimer = window.setInterval(async () => {
          await pollPendingVersionJob()
          await loadKnowledgeDocuments()
        }, 3000)
      }
    }

    const stopKnowledgePolling = () => {
      if (knowledgePollTimer) {
        window.clearInterval(knowledgePollTimer)
        knowledgePollTimer = null
      }
    }

    const handleFileUpload = async (event) => {
      const file = event.target.files[0]
      if (!file) return

      const fileName = file.name.toLowerCase()
      const allowedExtensions = ['.md', '.txt', '.json', '.yaml', '.yml', '.go']
      if (!allowedExtensions.some(extension => fileName.endsWith(extension))) {
        ElMessage.error('支持 .md、.txt、.json、.yaml、.yml 和 .go 文件')
        // 清空文件输入
        if (fileInput.value) {
          fileInput.value.value = ''
        }
        return
      }

      try {
        uploading.value = true
        const formData = new FormData()
        formData.append('file', file)

        const response = await api.post('/knowledge/documents', formData, {
          headers: {
            'Content-Type': 'multipart/form-data'
          }
        })

        if (response.data?.document) {
          if (response.data.duplicate) {
            ElMessage.success('文档已存在，沿用原索引任务')
          } else {
            ElMessage.success('文档已接收，等待索引')
          }
          await loadKnowledgeDocuments()
        } else {
          ElMessage.error('上传失败')
        }
      } catch (error) {
        console.error('File upload error:', error)
        ElMessage.error(error.response?.data?.message || '文件上传失败')
      } finally {
        uploading.value = false
        // 清空文件输入
        if (fileInput.value) {
          fileInput.value.value = ''
        }
      }
    }

    const triggerVersionUpload = () => {
      if (!versionTargetDocumentId.value) {
        ElMessage.warning('请先选择要更新的活动文档')
        return
      }
      versionFileInput.value?.click()
    }

    const pollPendingVersionJob = async () => {
      const job = pendingVersionJob.value
      if (!job?.id) return
      try {
        const response = await api.get(`/knowledge/jobs/${job.id}`)
        const latest = response.data?.job
        if (!latest) return
        pendingVersionJob.value = latest
        if (latest.status === 'completed') {
          if (latest.job_type === 'document_delete') {
            ElMessage.success('文档已删除，Redis 索引清理完成')
          } else {
            ElMessage.success(`文档 v${latest.version} 索引完成，活动版本已原子切换`)
          }
          pendingVersionJob.value = null
        } else if (latest.status === 'failed') {
          if (latest.job_type === 'document_delete') {
            ElMessage.warning(`文档已从查询中移除，但 Redis 清理失败并已进入死信（${latest.last_error_code || 'UNKNOWN'}）`)
          } else {
            ElMessage.warning(`文档 v${latest.version} 索引失败，旧版本保持可用（${latest.last_error_code || 'UNKNOWN'}）`)
          }
          pendingVersionJob.value = null
        }
      } catch (error) {
        console.error('Poll knowledge version job error:', error)
      }
    }

    const handleVersionUpload = async (event) => {
      const file = event.target.files[0]
      if (!file || !versionTargetDocumentId.value) return
      const fileName = file.name.toLowerCase()
      const allowedExtensions = ['.md', '.txt', '.json', '.yaml', '.yml', '.go']
      if (!allowedExtensions.some(extension => fileName.endsWith(extension))) {
        ElMessage.error('支持 .md、.txt、.json、.yaml、.yml 和 .go 文件')
        if (versionFileInput.value) versionFileInput.value.value = ''
        return
      }
      try {
        uploadingVersion.value = true
        const formData = new FormData()
        formData.append('file', file)
        const response = await api.post(`/knowledge/documents/${versionTargetDocumentId.value}/versions`, formData, {
          headers: { 'Content-Type': 'multipart/form-data' }
        })
        pendingVersionJob.value = response.data?.job || null
        if (response.data?.duplicate) {
          ElMessage.info(`该内容已存在于 v${response.data?.pending_version || response.data?.job?.version}`)
        } else {
          ElMessage.success(`已接收 v${response.data?.pending_version}；v${response.data?.previous_version} 将持续生效直到新索引成功`)
        }
        startKnowledgePolling()
      } catch (error) {
        console.error('Version upload error:', error)
        ElMessage.error(error.response?.data?.message || '文档新版本上传失败')
      } finally {
        uploadingVersion.value = false
        if (versionFileInput.value) versionFileInput.value.value = ''
      }
    }

    const rebuildSelectedDocument = async () => {
      if (!versionTargetDocumentId.value || rebuildingDocument.value) return
      try {
        rebuildingDocument.value = true
        const response = await api.post(`/knowledge/documents/${versionTargetDocumentId.value}/rebuild`)
        pendingVersionJob.value = response.data?.job || null
        ElMessage.success(`已创建重建候选 v${response.data?.pending_version}；活动 v${response.data?.previous_version} 不受影响`)
        startKnowledgePolling()
      } catch (error) {
        console.error('Rebuild document error:', error)
        ElMessage.error(error.response?.data?.message || '文档重建任务创建失败')
      } finally {
        rebuildingDocument.value = false
      }
    }

    const deleteSelectedDocument = async () => {
      const document = indexedKnowledgeDocuments.value.find(item => item.id === versionTargetDocumentId.value)
      if (!document || deletingDocument.value) return
      if (!window.confirm(`确定删除文档“${document.display_name}”吗？删除后会立即停止参与回答。`)) return
      try {
        deletingDocument.value = true
        const response = await api.delete(`/knowledge/documents/${document.id}`)
        pendingVersionJob.value = response.data?.job || null
        ElMessage.success('文档已立即退出知识库，后台正在清理 Redis 索引')
        await loadKnowledgeDocuments()
        startKnowledgePolling()
      } catch (error) {
        console.error('Delete document error:', error)
        ElMessage.error(error.response?.data?.message || '文档删除失败')
      } finally {
        deletingDocument.value = false
      }
    }

    onMounted(() => {
      loadSessions()
      loadKnowledgeDocuments()
      if (sessionStorage.getItem(diagnosticModeStorageKey) === '1' && sessionStorage.getItem(diagnosticRunStorageKey)) {
        diagnosticMode.value = true
        restoreDiagnosticRun()
      }
    })

    onUnmounted(() => {
      stopKnowledgePolling()
    })

    // expose to template
    return {
      sessions: computed(() => Object.values(sessions.value)),
      currentSessionId,
      tempSession,
      currentMessages,
      inputMessage,
      loading,
      messagesRef,
      messageInput,
      isStreaming,
      knowledgeRequired,
      uploading,
      fileInput,
      versionFileInput,
      uploadingVersion,
      versionTargetDocumentId,
      pendingVersionJob,
      rebuildingDocument,
      deletingDocument,
      indexedKnowledgeDocuments,
      knowledgeDocuments,
      knowledgeSearchOpen,
      knowledgeQuery,
      searchingKnowledge,
      knowledgeSearchResults,
      knowledgeSearchDiagnostics,
      knowledgeSearchConflicts,
      answeringKnowledge,
      answeringKnowledgeMode,
      knowledgeAnswer,
      evaluationCatalogOpen,
      loadingEvaluationCatalog,
      evaluationCatalog,
      evaluationRun,
      metricCatalog,
      loadingAnomaly,
      anomalyResult,
      anomalyScenarios,
      parentContextEvaluationOpen,
      loadingParentContextEvaluation,
      parentContextEvaluation,
      diagnosticMode,
      activeDiagnosticRun,
      diagnosticRecovered,
      restoringDiagnosticRun,
      diagnosticEvaluationOpen,
      loadingDiagnosticEvaluation,
      diagnosticEvaluation,
      contextCompressionOpen,
      loadingContextCompression,
      contextCompression,
      contextEvaluation,
      memoryPreviewOpen,
      loadingMemoryPreview,
      memoryPreview,
      memoryEvaluationOpen,
      loadingMemoryEvaluation,
      memoryEvaluation,
      toolRuntimeOpen,
      loadingToolCatalog,
      toolCatalog,
      invokingTool,
      toolResult,
      toolAgentQuery,
      runningToolAgent,
      toolAgentResult,
      toolEvaluationOpen,
      loadingToolEvaluation,
      toolEvaluation,
      policyControlOpen,
      loadingPolicyControl,
      policySnapshot,
      selectedStrategyIntent,
      simulatingPolicy,
      policySimulation,
      caseShadowMessage,
      runningCaseShadow,
      caseShadowResult,
      collaborationPlanMessage,
      planningCollaboration,
      collaborationPlan,
      runningCollaboration,
      collaborationRun,
      collaborationEvaluationOpen,
      loadingCollaborationEvaluation,
      collaborationEvaluation,
      strategyIntentOptions,
      profileMemories,
      profileDrafts,
      profileMemoryBusy,
      resolutionProposal,
      resolutionText,
      resolutionAcknowledged,
      loadingResolution,
      confirmedResolution,
      documentStatusLabel,
      jobStatusLabel,
      retrievalModeLabel,
      queryComplexityLabel,
      queryGapLabel,
      queryReasonLabel,
      deepOutcomeLabel,
      enhancementOutcomeLabel,
      intentLabel,
      intentStageLabel,
      diagnosticStateLabel,
      isDiagnosticTerminal,
      incidentIndexLabel,
      metricPercent,
      memoryCacheLabel,
      memoryContextKindLabel,
      profileKeyLabel,
      profileStatusLabel,
      profileSourceLabel,
      toggleMemoryPreview,
      loadMemoryPreview,
      rebuildWorkingMemory,
      correctProfileMemory,
      deleteProfileMemory,
      toggleMemoryEvaluation,
      toggleToolRuntime,
      invokeGovernedTool,
      formatToolData,
      runToolAgent,
      toggleToolEvaluation,
      togglePolicyControl,
      simulatePolicy,
      runCaseShadow,
      runCollaborationPlan,
      runCollaborationShadow,
      toggleCollaborationEvaluation,
      policySourceLabel,
      shortPolicyHash,
      strategyIntentLabel,
      strategyReasonLabel,
      strategyDependencyLabel,
      strategyStateLabel,
      caseStrengthLabel,
      caseMemoryStatusLabel,
      caseReasonLabel,
      collaborationDecisionLabel,
      collaborationReasonLabel,
      collaborationRunStatusLabel,
      collaborationRunReasonLabel,
      collaborationTaskStatusLabel,
      toggleDiagnosticEvaluation,
      toggleContextCompression,
      formattedFacts,
      previewResolution,
      closeResolutionProposal,
      confirmResolution,
      loadConfirmedResolution,
      renderMarkdown,
      playTTS,
      createNewSession,
      switchSession,
      syncHistory,
      sendMessage,
      triggerFileUpload,
      handleFileUpload,
      triggerVersionUpload,
      handleVersionUpload,
      rebuildSelectedDocument,
      deleteSelectedDocument,
      toggleKnowledgeSearch,
      searchKnowledge,
      answerKnowledge,
      toggleParentContextEvaluation,
      toggleEvaluationCatalog,
      evaluationSliceLabel,
      evaluationStatusLabel,
      evaluationFailureLabel,
      metricDomainLabel,
      metricTypeLabel,
      anomalyMetricLabel,
      anomalyRecommendationLabel,
      anomalyDecisionLabel,
      anomalyDecisionClass,
      anomalySignalStatusLabel,
      simulateAnomaly,
      cancelDiagnosticRun,
      resetDiagnosticRun,
      onDiagnosticModeChanged,
      evidenceForCitation,
      sourceKindLabel,
      shortRevision
    }
  }
}
</script>

<style scoped>
.ai-chat-container {
  height: 100vh;
  display: flex;
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  position: relative;
  overflow: hidden;
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial;
  color: #222;
}

.ai-chat-container::before {
  content: '';
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: url('data:image/svg+xml,<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><circle cx="20" cy="20" r="2" fill="rgba(255,255,255,0.08)"/><circle cx="80" cy="80" r="2" fill="rgba(255,255,255,0.08)"/><circle cx="40" cy="60" r="1" fill="rgba(255,255,255,0.06)"/><circle cx="60" cy="30" r="1.5" fill="rgba(255,255,255,0.06)"/></svg>');
  animation: float 20s ease-in-out infinite;
  opacity: 0.25;
}

@keyframes float {
  0%, 100% { transform: translateY(0px) rotate(0deg); }
  50% { transform: translateY(-20px) rotate(180deg); }
}

.session-list {
  width: 280px;
  height: 100vh;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  background: rgba(255, 255, 255, 0.95);
  backdrop-filter: blur(15px);
  border-right: 1px solid rgba(0, 0, 0, 0.08);
  box-shadow: 2px 0 20px rgba(0, 0, 0, 0.08);
  position: relative;
  z-index: 2;
}

.session-list-header {
  padding: 20px;
  text-align: center;
  font-weight: 600;
  background: linear-gradient(135deg, rgba(102, 126, 234, 0.06) 0%, rgba(103, 194, 58, 0.06) 100%);
  border-bottom: 1px solid rgba(0, 0, 0, 0.06);
  display: flex;
  flex-direction: column;
  gap: 12px;
  align-items: center;
}

.new-chat-btn {
  width: 100%;
  padding: 12px 0;
  cursor: pointer;
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  color: white;
  border: none;
  border-radius: 12px;
  font-size: 14px;
  font-weight: 600;
  box-shadow: 0 4px 15px rgba(102, 126, 234, 0.28);
  transition: all 0.25s ease;
  position: relative;
  overflow: hidden;
}

.new-chat-btn::before {
  content: '';
  position: absolute;
  top: 0;
  left: -100%;
  width: 100%;
  height: 100%;
  background: linear-gradient(90deg, transparent, rgba(255,255,255,0.12), transparent);
  transition: left 0.5s;
}

.new-chat-btn:hover::before {
  left: 100%;
}

.new-chat-btn:hover {
  transform: translateY(-2px);
  box-shadow: 0 8px 25px rgba(102, 126, 234, 0.36);
}

.session-list-ul {
  list-style: none;
  padding: 0;
  margin: 0;
  flex: 1;
  overflow-y: auto;
}

.session-item {
  padding: 15px 20px;
  cursor: pointer;
  border-bottom: 1px solid rgba(0, 0, 0, 0.03);
  transition: all 0.2s ease;
  position: relative;
  color: #2c3e50;
}

.session-item.active {
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  color: white;
  font-weight: 600;
  box-shadow: inset 0 0 20px rgba(102, 126, 234, 0.2);
}

.session-item:hover {
  background: rgba(102, 126, 234, 0.06);
  transform: translateX(4px);
}

/* chat section */
.chat-section {
  flex: 1;
  display: flex;
  flex-direction: column;
  position: relative;
  z-index: 1;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
}

.top-bar {
  background: rgba(255, 255, 255, 0.95);
  backdrop-filter: blur(10px);
  color: #2c3e50;
  display: flex;
  align-items: center;
  padding: 12px 24px;
  box-shadow: 0 2px 14px rgba(0, 0, 0, 0.06);
  border-bottom: 1px solid rgba(0, 0, 0, 0.06);
  gap: 12px;
  flex-wrap: wrap;
  flex: 0 0 auto;
}

.top-bar button.workspace-active {
  outline: 3px solid rgba(64, 158, 255, 0.24);
  outline-offset: 2px;
  filter: saturate(1.12);
}

.capability-workspace {
  flex: 0 1 auto;
  min-height: 0;
  max-height: 62vh;
  overflow-y: auto;
  overflow-x: hidden;
  overscroll-behavior: contain;
  scrollbar-gutter: stable;
  padding-bottom: 12px;
}

.capability-workspace:empty {
  display: none;
}

.capability-workspace::-webkit-scrollbar {
  width: 10px;
}

.capability-workspace::-webkit-scrollbar-thumb {
  background: rgba(74, 67, 168, 0.42);
  border: 2px solid transparent;
  border-radius: 10px;
  background-clip: padding-box;
}

.capability-workspace::-webkit-scrollbar-track {
  background: rgba(255, 255, 255, 0.18);
}

.memory-workbench {
  margin: 12px 20px 0;
  padding: 16px;
  border-radius: 14px;
  border: 1px solid #b8d9c7;
  background: linear-gradient(135deg, #f2fff7 0%, #eef8ff 100%);
  color: #243746;
}

.memory-workbench-header,
.memory-workbench-header > div,
.memory-stats {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.memory-workbench-header {
  justify-content: space-between;
}

.memory-workbench-header > div:first-child {
  align-items: flex-start;
  flex-direction: column;
}

.memory-workbench-header span,
.memory-stats,
.memory-context-items span {
  color: #547080;
  font-size: 13px;
}

.memory-workbench button,
.memory-toggle-btn {
  border: 0;
  border-radius: 8px;
  padding: 8px 12px;
  cursor: pointer;
  background: #3b9f74;
  color: #fff;
}

.memory-workbench button:disabled,
.memory-toggle-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.memory-loading,
.memory-stats,
.memory-boundary,
.memory-workbench details {
  margin-top: 12px;
}

.memory-cache-badge {
  border-radius: 999px;
  padding: 4px 9px;
  background: #d7f5e5;
  color: #176744;
  font-weight: 700;
}

.cache-mysql_fallback_cache_unavailable {
  background: #fff1d6;
  color: #8a5a00;
}

.memory-boundary {
  border-left: 4px solid #5aa6b8;
  padding: 8px 12px;
  background: rgba(255, 255, 255, 0.72);
  font-size: 13px;
}

.memory-context-items {
  max-height: 280px;
  overflow: auto;
  padding-left: 24px;
}

.memory-context-items li {
  margin: 9px 0;
}

.memory-context-items span {
  margin-left: 8px;
}

.memory-context-items p {
  margin: 4px 0 0;
  white-space: pre-wrap;
  word-break: break-word;
}

.memory-no-session {
  margin-top: 12px;
  padding: 10px;
  border-radius: 8px;
  color: #607380;
  background: rgba(255, 255, 255, 0.7);
  font-size: 13px;
}

.profile-memory-panel {
  margin-top: 14px;
  padding-top: 13px;
  border-top: 1px solid #b8d9c7;
}

.profile-memory-summary,
.profile-memory-card-header,
.profile-memory-actions,
.profile-memory-actions > div {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
}

.profile-memory-summary span,
.profile-memory-panel > p,
.profile-memory-actions small {
  color: #607380;
  font-size: 12px;
}

.profile-memory-panel > p {
  margin: 7px 0 10px;
}

.profile-memory-list {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
  gap: 9px;
}

.profile-memory-list article {
  padding: 10px;
  border: 1px solid #b9d7c9;
  border-radius: 9px;
  background: #fff;
}

.profile-memory-list article.profile-candidate {
  border-color: #d8bd70;
  background: #fffdf5;
}

.profile-memory-list article.profile-conflicted {
  border-color: #e39a9a;
  background: #fff7f7;
}

.profile-memory-card-header span {
  color: #667783;
  font-size: 12px;
}

.profile-memory-list input {
  width: 100%;
  box-sizing: border-box;
  margin: 8px 0;
  padding: 8px 9px;
  border: 1px solid #bfd2ca;
  border-radius: 7px;
}

.profile-memory-actions button {
  padding: 6px 9px;
}

.profile-memory-actions .profile-delete-btn {
  color: #fff;
  background: #d45d5d;
}

.diagnostic-workbench {
  max-height: 48vh;
  overflow-y: auto;
  padding: 14px 20px;
  color: #23344d;
  background: rgba(247, 250, 255, 0.98);
  border-bottom: 2px solid rgba(103, 126, 234, 0.22);
  box-shadow: 0 5px 18px rgba(31, 45, 85, 0.08);
}

.diagnostic-workbench-header,
.diagnostic-run-summary,
.hypothesis-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px 14px;
}

.diagnostic-workbench-header > div:first-child {
  display: grid;
  gap: 3px;
}

.diagnostic-workbench-header span,
.diagnostic-run-summary,
.diagnostic-steps li span {
  color: #65758b;
  font-size: 12px;
}

.diagnostic-workbench-header .diagnostic-recovered {
  display: inline-flex;
  width: fit-content;
  margin-top: 6px;
  padding: 3px 8px;
  border-radius: 999px;
  color: #176b45;
  background: #dff7e9;
  font-weight: 600;
}

.diagnostic-actions {
  display: flex;
  gap: 8px;
}

.diagnostic-actions button {
  padding: 6px 11px;
  border: 1px solid rgba(90, 103, 216, 0.3);
  border-radius: 8px;
  color: #4b55a5;
  background: #fff;
  cursor: pointer;
}

.diagnostic-actions button:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.diagnostic-evaluation {
  margin: 12px 0;
  padding: 14px;
  border: 1px solid #c9d8ff;
  border-radius: 12px;
  background: #f7f9ff;
}

.diagnostic-evaluation-loading {
  color: #52627d;
}

.diagnostic-evaluation-title,
.diagnostic-evaluation-title > div {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}

.diagnostic-evaluation-title > div {
  align-items: baseline;
  justify-content: flex-start;
  flex-wrap: wrap;
}

.diagnostic-evaluation-title span,
.diagnostic-evaluation details small {
  color: #64728b;
  font-size: 12px;
}

.evaluation-gate {
  padding: 4px 9px;
  border-radius: 999px;
  font-weight: 700;
}

.evaluation-gate.passed {
  color: #176b45;
  background: #dcf7e8;
}

.evaluation-gate.failed {
  color: #a23131;
  background: #ffe4e4;
}

.diagnostic-evaluation-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 8px;
  margin: 12px 0;
}

.diagnostic-evaluation-grid > div {
  display: flex;
  flex-direction: column;
  gap: 3px;
  padding: 10px;
  border-radius: 9px;
  background: #ffffff;
  border: 1px solid #e4e9f5;
}

.diagnostic-evaluation-grid strong {
  color: #3657ba;
  font-size: 18px;
}

.diagnostic-evaluation-grid span,
.evaluation-candidate-warning span {
  color: #596781;
  font-size: 12px;
}

.evaluation-candidate-warning {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin: 10px 0;
  padding: 10px;
  border-radius: 9px;
  color: #7a5715;
  background: #fff4d6;
  border: 1px solid #f0d78f;
}

.evaluation-category-list {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin: 8px 0;
}

.evaluation-category-list span {
  padding: 3px 7px;
  border-radius: 999px;
  color: #485977;
  background: #e8eefc;
  font-size: 11px;
}

@media (max-width: 760px) {
  .diagnostic-evaluation-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

.diagnostic-empty,
.diagnostic-waiting {
  margin-top: 10px;
  padding: 10px 12px;
  border-radius: 9px;
  background: rgba(103, 126, 234, 0.08);
}

.diagnostic-waiting {
  color: #7a5312;
  background: #fff7e6;
  border: 1px solid #ffd591;
}

.diagnostic-run-summary {
  justify-content: flex-start;
  margin-top: 12px;
}

.run-state-badge {
  padding: 5px 9px;
  border-radius: 999px;
  color: #3451a3;
  background: #e9efff;
  font-weight: 700;
}

.state-waiting_user {
  color: #9a6200;
  background: #fff1cc;
}

.state-succeeded {
  color: #176e4b;
  background: #ddf6e9;
}

.state-cancelled,
.state-budget_exceeded,
.state-failed {
  color: #9c3434;
  background: #ffe7e7;
}

.diagnostic-steps {
  margin-top: 12px;
  padding: 9px 12px;
  border: 1px solid rgba(103, 126, 234, 0.18);
  border-radius: 9px;
  background: #fff;
}

.diagnostic-steps summary {
  cursor: pointer;
  font-weight: 700;
}

.diagnostic-steps ol,
.diagnostic-hypotheses ol {
  margin: 8px 0 0;
  padding-left: 22px;
}

.diagnostic-steps li {
  display: grid;
  gap: 2px;
  margin: 5px 0;
}

.case-memory-recall {
  margin-top: 12px;
  padding: 12px;
  border: 1px solid #a7c8bd;
  border-radius: 10px;
  background: #f2fbf7;
}

.case-memory-recall.case-unavailable {
  border-color: #ddc98e;
  background: #fffaf0;
}

.case-memory-header,
.case-memory-list article > div {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
}

.case-memory-header span,
.case-memory-list span,
.case-memory-list small {
  color: #617584;
  font-size: 12px;
}

.case-memory-disclaimer {
  margin: 8px 0;
  padding-left: 9px;
  border-left: 3px solid #4a9c7d;
  color: #465b68;
  font-size: 13px;
}

.case-memory-list {
  display: grid;
  gap: 8px;
}

.case-memory-list article {
  padding: 9px 10px;
  border-radius: 8px;
  background: #fff;
}

.case-memory-list details {
  margin-top: 7px;
}

.case-memory-list summary {
  cursor: pointer;
  color: #356b5a;
  font-weight: 700;
}

.case-memory-list p {
  margin: 6px 0;
  color: #4a5e6d;
  font-size: 13px;
}

.diagnostic-hypotheses {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(300px, 1fr));
  gap: 10px;
  margin-top: 10px;
}

.diagnostic-hypotheses article {
  padding: 12px;
  border: 1px solid rgba(103, 126, 234, 0.2);
  border-radius: 10px;
  background: #fff;
}

.diagnostic-hypotheses p {
  margin: 8px 0;
  color: #526176;
  font-size: 13px;
}

.diagnostic-hypotheses li {
  margin-bottom: 7px;
}

.diagnostic-hypotheses small {
  color: #6d7b90;
}

.resolution-preview-btn {
  width: 100%;
  margin-top: 8px;
  padding: 8px 10px;
  border: 1px solid #98a9e8;
  border-radius: 8px;
  color: #4054a5;
  background: #eef2ff;
  cursor: pointer;
}

.resolution-preview-btn:disabled,
.resolution-confirm-btn:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}

.resolution-proposal,
.confirmed-resolution {
  margin-top: 12px;
  padding: 14px;
  border-radius: 12px;
}

.resolution-proposal {
  border: 1px solid #e0bd67;
  background: #fffbeb;
}

.resolution-proposal-header,
.confirmed-resolution-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.confirmed-resolution-actions {
  display: flex;
  align-items: center;
  gap: 7px;
}

.confirmed-resolution-actions button {
  padding: 4px 7px;
  border: 1px solid #9bd4b6;
  border-radius: 7px;
  color: #246346;
  background: #fff;
  cursor: pointer;
}

.resolution-proposal-header > div {
  display: grid;
  gap: 3px;
}

.resolution-proposal-header span,
.confirmed-resolution small {
  color: #68768b;
  font-size: 12px;
}

.resolution-proposal-header button {
  border: 0;
  color: #6f5a25;
  background: transparent;
  cursor: pointer;
}

.resolution-proposal-content p,
.confirmed-resolution p {
  margin: 8px 0;
  color: #435169;
  font-size: 13px;
}

.resolution-input-label {
  display: grid;
  gap: 6px;
  margin-top: 12px;
  color: #4b5363;
  font-size: 13px;
}

.resolution-input-label textarea {
  width: 100%;
  box-sizing: border-box;
  padding: 9px 10px;
  border: 1px solid #d7be7e;
  border-radius: 8px;
  resize: vertical;
  font: inherit;
}

.resolution-confirm-check {
  display: flex;
  align-items: flex-start;
  gap: 7px;
  margin: 10px 0;
  color: #705821;
  font-size: 12px;
}

.resolution-confirm-btn {
  padding: 9px 13px;
  border: 0;
  border-radius: 8px;
  color: white;
  background: #5267c9;
  cursor: pointer;
}

.confirmed-resolution {
  border: 1px solid #9bd4b6;
  background: #effbf4;
}

.incident-index-badge {
  padding: 4px 8px;
  border-radius: 999px;
  color: #7e5d16;
  background: #fff1c7;
  font-size: 12px;
  font-weight: 700;
}

.incident-index-badge.index-indexed {
  color: #176b45;
  background: #d9f4e5;
}

.incident-index-badge.index-failed {
  color: #9c3434;
  background: #ffe3e3;
}

.knowledge-status {
  display: flex;
  justify-content: space-between;
  align-items: center;
  flex-wrap: wrap;
  gap: 16px;
  padding: 9px 24px;
  color: #36506c;
  background: rgba(239, 247, 255, 0.96);
  border-bottom: 1px solid rgba(64, 158, 255, 0.18);
  font-size: 13px;
}

.knowledge-document-details {
  flex-basis: 100%;
  padding-top: 2px;
  border-top: 1px dashed rgba(64, 158, 255, 0.2);
}

.knowledge-document-details summary {
  width: fit-content;
  cursor: pointer;
  color: #2f6ea7;
  font-weight: 600;
}

.knowledge-document-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
  gap: 8px;
  margin-top: 8px;
}

.knowledge-document-card {
  padding: 9px 11px;
  border: 1px solid rgba(64, 158, 255, 0.18);
  border-radius: 8px;
  background: rgba(255, 255, 255, 0.82);
}

.knowledge-document-card > div:first-child {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
}

.document-status-badge {
  flex-shrink: 0;
  padding: 2px 7px;
  border-radius: 999px;
  background: #e8eef5;
  color: #52677f;
  font-size: 11px;
}

.document-status-badge.status-indexed {
  background: #e2f7ee;
  color: #137a52;
}

.document-status-badge.status-failed {
  background: #fff0f0;
  color: #c23d4b;
}

.document-status-badge.status-uploaded,
.document-status-badge.status-parsing {
  background: #fff4de;
  color: #a56500;
}

.document-version-line {
  margin-top: 5px;
  color: #708399;
  font-size: 11px;
}

.document-index-stats {
  display: flex;
  flex-wrap: wrap;
  gap: 5px 9px;
  margin-top: 6px;
  color: #5e7187;
  font-size: 11px;
}

.document-index-stats strong {
  flex-basis: 100%;
  color: #38536f;
}

.document-error-code {
  margin-top: 5px;
  color: #b23a48;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 11px;
}

.knowledge-version-controls {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 420px;
}

.knowledge-version-controls select {
  max-width: 260px;
  padding: 6px 8px;
  border: 1px solid #b9cce3;
  border-radius: 7px;
  background: #fff;
  color: #36506c;
}

.knowledge-version-controls button {
  padding: 6px 10px;
  border: 0;
  border-radius: 7px;
  background: #409eff;
  color: #fff;
  cursor: pointer;
}

.knowledge-version-controls button:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}

.knowledge-version-controls .delete-document-btn {
  background: #f56c6c;
}

.query-assessment {
  margin: 8px 0 12px;
  padding: 10px 12px;
  border-left: 3px solid #409eff;
  border-radius: 4px;
  background: #f2f8ff;
  color: #3f4a5a;
  font-size: 13px;
}

.query-assessment-reasons {
  margin-top: 4px;
  color: #6b7280;
}

.query-assessment-action {
  margin-top: 5px;
  color: #355f93;
}

.version-pending {
  color: #b36b00;
  white-space: nowrap;
}

.knowledge-latest {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.knowledge-search-panel {
  max-height: 46vh;
  overflow-y: auto;
  padding: 14px 24px 18px;
  background: rgba(248, 251, 255, 0.98);
  border-bottom: 1px solid rgba(64, 158, 255, 0.2);
  box-shadow: 0 8px 18px rgba(27, 54, 93, 0.08);
}

.knowledge-search-form {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
}

.knowledge-search-form input {
  flex: 1;
  flex-basis: 320px;
  min-width: 0;
  padding: 10px 13px;
  border: 1px solid #c9d7e8;
  border-radius: 9px;
  outline: none;
}

.knowledge-search-form input:focus {
  border-color: #409eff;
  box-shadow: 0 0 0 3px rgba(64, 158, 255, 0.12);
}

.knowledge-search-form button,
.search-toggle-btn {
  padding: 8px 14px;
  border: none;
  border-radius: 9px;
  color: white;
  background: linear-gradient(135deg, #409eff 0%, #536dfe 100%);
  cursor: pointer;
  font-weight: 600;
}

.knowledge-search-form .answer-evidence-btn {
  background: linear-gradient(135deg, #19a974 0%, #2f80ed 100%);
}

.knowledge-search-form .deep-answer-btn {
  background: linear-gradient(135deg, #7c3aed 0%, #2563eb 100%);
}

.knowledge-search-form button:disabled {
  background: #b8c2cf;
  cursor: not-allowed;
}

.knowledge-search-summary {
  margin: 11px 0;
  color: #52677f;
  font-size: 12px;
}

.knowledge-answer {
  margin-top: 12px;
  padding: 13px;
  border: 1px solid rgba(25, 169, 116, 0.35);
  border-radius: 10px;
  background: rgba(238, 252, 247, 0.96);
}

.knowledge-answer.insufficient {
  border-color: rgba(230, 162, 60, 0.45);
  background: rgba(255, 248, 235, 0.96);
}

.knowledge-answer-header {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  color: #31506b;
  font-size: 13px;
}

.knowledge-answer-header span {
  color: #6e8195;
  font-size: 11px;
}

.knowledge-answer-text {
  margin-top: 10px;
  white-space: pre-wrap;
  line-height: 1.65;
  color: #273b4d;
}

.deep-diagnostics {
  margin-top: 10px;
  padding: 9px 10px;
  border-radius: 7px;
  background: rgba(124, 58, 237, 0.08);
  color: #4c3d72;
  font-size: 12px;
  line-height: 1.55;
}

.deep-diagnostics details {
  margin-top: 5px;
}

.deep-budget-observation,
.deep-fallback-reasons {
  margin-top: 5px;
}

.deep-fallback-reasons {
  color: #9a630d;
}

.knowledge-follow-up {
  margin-top: 9px;
  color: #9a630d;
  font-size: 13px;
}

.knowledge-citations {
  margin-top: 10px;
}

.knowledge-citations details {
  margin-top: 6px;
  padding: 7px 9px;
  border-radius: 7px;
  background: rgba(255, 255, 255, 0.8);
  color: #536a82;
  font-size: 12px;
}

.knowledge-citations summary {
  cursor: pointer;
}

.knowledge-citations pre {
  max-height: 140px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-word;
}

.knowledge-search-results {
  display: grid;
  gap: 10px;
}

.evidence-card {
  padding: 11px 13px;
  border: 1px solid #dce7f5;
  border-radius: 10px;
  background: white;
}

.evidence-title {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  color: #2d425c;
  font-size: 13px;
}

.evidence-title span,
.evidence-location {
  color: #788da6;
  font-size: 11px;
}

.evidence-location {
  margin-top: 5px;
}

.evidence-source-meta {
  margin-top: 4px;
  color: #59718c;
  font-size: 11px;
}

.parent-context-evidence {
  display: grid;
  gap: 4px;
  margin-top: 8px;
  padding: 8px;
  border-left: 3px solid #8b7cf6;
  background: #f7f5ff;
  color: #4c4a73;
  font-size: 11px;
}

.knowledge-conflicts {
  display: grid;
  gap: 8px;
  margin: 10px 0;
  padding: 10px 12px;
  border: 1px solid #e6a23c;
  border-radius: 9px;
  background: #fff8e8;
  color: #7a4d00;
}

.knowledge-conflicts article,
.knowledge-conflicts ul {
  margin: 0;
}

.evidence-card pre {
  margin: 9px 0 0;
  padding: 9px;
  max-height: 130px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-word;
  border-radius: 7px;
  background: #f6f8fb;
  color: #34495e;
  font-family: Consolas, monospace;
  font-size: 12px;
}

.knowledge-search-empty {
  padding: 16px 0 3px;
  color: #8a98a8;
  font-size: 13px;
}

.back-btn {
  background: rgba(255, 255, 255, 0.22);
  border: 1px solid rgba(0, 0, 0, 0.06);
  color: #2c3e50;
  padding: 8px 14px;
  border-radius: 10px;
  cursor: pointer;
  font-weight: 600;
  transition: all 0.2s ease;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.06);
}

.back-btn:hover {
  background: rgba(255, 255, 255, 0.32);
  transform: translateY(-2px);
  box-shadow: 0 6px 20px rgba(0, 0, 0, 0.08);
}

.sync-btn {
  background: linear-gradient(135deg, #67c23a 0%, #409eff 100%);
  color: white;
  padding: 8px 14px;
  border: none;
  border-radius: 10px;
  cursor: pointer;
  font-size: 13px;
  font-weight: 600;
  box-shadow: 0 4px 12px rgba(103, 194, 58, 0.2);
  transition: all 0.2s ease;
}

.sync-btn:disabled {
  background: #ccc;
  box-shadow: none;
  cursor: not-allowed;
}

.route-mode {
  margin-left: 6px;
  padding: 7px 12px;
  border-radius: 999px;
  background: rgba(64, 158, 255, 0.1);
  color: #337ecc;
  font-size: 13px;
  font-weight: 700;
}

.knowledge-mode {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 6px 9px;
  border-radius: 999px;
  background: rgba(25, 169, 116, 0.1);
  color: #167a57;
  font-size: 12px;
  font-weight: 700;
  white-space: nowrap;
}

.diagnostic-mode {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 6px 9px;
  border-radius: 999px;
  background: rgba(93, 78, 190, 0.11);
  color: #5946aa;
  font-size: 12px;
  font-weight: 700;
  white-space: nowrap;
}

.tool-runtime-toggle {
  padding: 8px 12px;
  border: none;
  border-radius: 10px;
  background: linear-gradient(135deg, #405a7d 0%, #526da8 100%);
  color: #fff;
  font-size: 12px;
  font-weight: 700;
  cursor: pointer;
  white-space: nowrap;
}

.tool-runtime-toggle:disabled {
  cursor: wait;
  opacity: 0.65;
}

.strategy-control-toggle {
  padding: 8px 12px;
  border: none;
  border-radius: 10px;
  background: linear-gradient(135deg, #6848c8 0%, #3e78c7 100%);
  color: #fff;
  font-size: 12px;
  font-weight: 700;
  cursor: pointer;
  white-space: nowrap;
}

.strategy-control-toggle:disabled {
  cursor: wait;
  opacity: 0.65;
}

.strategy-control-panel {
  margin: 12px 20px 0;
  padding: 15px;
  border: 1px solid rgba(103, 73, 190, 0.22);
  border-radius: 14px;
  background: linear-gradient(145deg, rgba(249, 247, 255, 0.98), rgba(237, 246, 255, 0.98));
  box-shadow: 0 8px 24px rgba(70, 72, 150, 0.1);
  color: #465069;
  font-size: 12px;
}

.strategy-control-header,
.strategy-result-heading,
.strategy-policy-identity,
.strategy-dependencies {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}

.strategy-control-header {
  justify-content: space-between;
}

.strategy-control-header > div {
  display: grid;
  gap: 3px;
}

.strategy-control-header span:not(.shadow-only-badge) {
  color: #6f7890;
}

.shadow-only-badge {
  padding: 5px 9px;
  border-radius: 999px;
  background: #ebe2ff;
  color: #613fb1;
  font-weight: 800;
}

.strategy-policy-identity {
  margin-top: 12px;
}

.strategy-policy-identity > span,
.strategy-dependencies > span {
  padding: 5px 8px;
  border-radius: 7px;
  background: rgba(255, 255, 255, 0.82);
}

.strategy-control-notice {
  margin-top: 10px;
  padding: 9px 11px;
  border-left: 4px solid #7255bd;
  border-radius: 6px;
  background: rgba(255, 255, 255, 0.78);
}

.strategy-simulator {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
  margin-top: 11px;
}

.strategy-intent-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 7px;
}

.strategy-intent-actions button {
  padding: 7px 11px;
  border: 1px solid rgba(104, 72, 200, 0.25);
  border-radius: 8px;
  background: #fff;
  color: #5e4a9d;
  cursor: pointer;
  font-weight: 700;
}

.strategy-intent-actions button.active {
  background: #6547bd;
  color: #fff;
}

.strategy-simulation-result {
  margin-top: 10px;
  padding: 11px;
  border: 1px solid rgba(73, 127, 190, 0.22);
  border-radius: 10px;
  background: rgba(255, 255, 255, 0.9);
}

.strategy-result-heading {
  justify-content: space-between;
}

.strategy-result-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(155px, 1fr));
  gap: 7px;
  margin-top: 9px;
}

.strategy-result-grid > span {
  padding: 7px 8px;
  border-radius: 7px;
  background: #f0f5ff;
}

.strategy-dependencies {
  margin-top: 9px;
}

.dependency-ready,
.policy-ok,
.strategy-state-active {
  color: #16704c;
  font-weight: 700;
}

.dependency-down,
.policy-warning,
.strategy-state-disabled {
  color: #a24b3d;
  font-weight: 700;
}

.strategy-state-shadow {
  color: #7b58b1;
  font-weight: 700;
}

.strategy-registry-details {
  margin-top: 11px;
}

.strategy-registry-details summary {
  cursor: pointer;
  color: #5d4a99;
  font-weight: 700;
}

.strategy-registry-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
  gap: 8px;
  margin-top: 9px;
}

.strategy-registry-grid article {
  padding: 9px;
  border: 1px solid rgba(92, 80, 155, 0.14);
  border-radius: 9px;
  background: rgba(255, 255, 255, 0.82);
}

.strategy-registry-grid p {
  margin: 5px 0 0;
  word-break: break-word;
}

.strategy-control-empty {
  color: #778197;
}

.case-shadow-console {
  margin-top: 12px;
  padding: 12px;
  border: 1px dashed rgba(104, 72, 200, 0.34);
  border-radius: 11px;
  background: rgba(244, 240, 255, 0.72);
}

.collaboration-plan-console {
  margin-top: 12px;
  padding: 12px;
  border: 1px dashed rgba(41, 124, 151, 0.34);
  border-radius: 11px;
  background: rgba(235, 250, 252, 0.72);
}

.collaboration-plan-console .strategy-result-heading > div {
  display: grid;
  gap: 3px;
}

.collaboration-plan-console p {
  margin: 0;
  color: #627985;
  font-weight: 500;
}

.case-shadow-console .strategy-result-heading > div {
  display: grid;
  gap: 3px;
}

.case-shadow-console p {
  margin: 0;
  color: #6f7890;
  font-weight: 500;
}

.case-shadow-input {
  display: grid;
  grid-template-columns: minmax(240px, 1fr) auto;
  gap: 9px;
  margin-top: 10px;
}

.case-shadow-input textarea {
  resize: vertical;
  min-height: 52px;
  padding: 9px;
  border: 1px solid rgba(92, 80, 155, 0.24);
  border-radius: 8px;
  color: #465069;
  font: inherit;
}

.case-shadow-input button {
  padding: 0 13px;
  border: none;
  border-radius: 8px;
  background: #6547bd;
  color: #fff;
  font-weight: 700;
  cursor: pointer;
}

.case-shadow-input button:disabled {
  cursor: wait;
  opacity: 0.6;
}

.collaboration-actions {
  display: grid;
  gap: 7px;
}

.collaboration-actions .collaboration-run-button {
  background: #167c86;
}

.case-shadow-result {
  margin-top: 10px;
  padding: 10px;
  border-radius: 9px;
  background: rgba(255, 255, 255, 0.9);
}

.case-priority-recommendation {
  display: grid;
  gap: 5px;
  margin-top: 9px;
  padding: 9px;
  border-left: 4px solid #2a9d71;
  border-radius: 6px;
  background: #effaf6;
  color: #3f5860;
}

.collaboration-signal-list {
  display: flex;
  flex-wrap: wrap;
  gap: 7px;
  margin-top: 9px;
}

.collaboration-signal-list span {
  padding: 5px 8px;
  border-radius: 999px;
  font-weight: 700;
}

.signal-on {
  background: #ddf5ec;
  color: #16704c;
}

.signal-off {
  background: #edf0f4;
  color: #7c8491;
}

.collaboration-task-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(270px, 1fr));
  gap: 8px;
  margin-top: 9px;
}

.collaboration-task-grid article {
  display: grid;
  gap: 6px;
  padding: 10px;
  border: 1px solid rgba(41, 124, 151, 0.18);
  border-radius: 8px;
  background: #fff;
}

.collaboration-run-result {
  border: 1px solid rgba(22, 124, 134, 0.22);
}

.collaboration-unified-answer {
  margin-top: 10px;
  padding: 10px;
  border-left: 4px solid #167c86;
  border-radius: 7px;
  background: #eefafa;
  color: #34535c;
  white-space: pre-wrap;
}

.unified-evaluation-summary {
  display: grid;
  gap: 10px;
  margin: 10px 0 14px;
  padding: 12px;
  border: 1px solid rgba(52, 168, 121, 0.24);
  border-radius: 10px;
  background: rgba(244, 253, 249, 0.92);
}

.evaluation-run-heading,
.evaluation-catalog-heading,
.evaluation-scorecard-grid article,
.evaluation-scorecard-grid article > div {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}

.evaluation-run-heading > div {
  display: grid;
  gap: 2px;
}

.evaluation-run-heading span,
.evaluation-scorecard-grid span,
.evaluation-run-details,
.evaluation-catalog-heading span {
  color: #65718b;
  font-size: 12px;
}

.evaluation-decision-strip,
.evaluation-failure-list,
.evaluation-artifact-list {
  display: flex;
  flex-wrap: wrap;
  gap: 7px;
}

.evaluation-scorecard-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(170px, 1fr));
  gap: 7px;
}

.evaluation-scorecard-grid article {
  padding: 8px;
  border: 1px solid rgba(64, 90, 125, 0.13);
  border-radius: 8px;
  background: #fff;
}

.evaluation-scorecard-grid article > div {
  align-items: flex-start;
  flex-direction: column;
  gap: 2px;
}

.evaluation-run-details summary {
  cursor: pointer;
  color: #5e45ad;
  font-weight: 700;
}

.evaluation-failure-list,
.evaluation-artifact-list {
  margin-top: 8px;
}

.evaluation-failure-list span,
.evaluation-artifact-list span {
  padding: 5px 7px;
  border-radius: 6px;
  background: #fff;
}

.evaluation-catalog-heading {
  margin: 4px 0 8px;
}

.anomaly-workbench {
  padding: 9px;
  border: 1px dashed rgba(94, 69, 173, 0.28);
  border-radius: 8px;
  background: rgba(255, 255, 255, 0.74);
}

.anomaly-reason-line {
  color: #69758c;
  font-family: Consolas, "Courier New", monospace;
  font-size: 12px;
}

.anomaly-workbench summary {
  cursor: pointer;
  color: #5e45ad;
  font-weight: 800;
}

.anomaly-workbench > p {
  margin: 8px 0;
  color: #69758c;
  font-size: 12px;
}

.anomaly-scenario-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 7px;
}

.anomaly-scenario-actions button {
  padding: 6px 9px;
  border: 1px solid rgba(94, 69, 173, 0.24);
  border-radius: 7px;
  background: #fff;
  color: #5e45ad;
  cursor: pointer;
  font-weight: 700;
}

.anomaly-result {
  display: grid;
  gap: 9px;
  margin-top: 9px;
  padding: 10px;
  border-radius: 8px;
}

.anomaly-result.anomaly-detected {
  border: 1px solid rgba(217, 137, 35, 0.35);
  background: #fff8ea;
}

.anomaly-result.anomaly-healthy {
  border: 1px solid rgba(52, 168, 121, 0.28);
  background: #f0fbf6;
}

.anomaly-result.anomaly-insufficient {
  border: 1px solid rgba(85, 116, 173, 0.3);
  background: #f3f6fb;
}

.metric-catalog-workbench {
  padding: 9px;
  border: 1px dashed rgba(42, 137, 118, 0.35);
  border-radius: 8px;
  background: rgba(240, 251, 247, 0.72);
}

.metric-catalog-workbench > summary,
.metric-definition-list > summary {
  cursor: pointer;
  color: #267d6b;
  font-weight: 800;
}

.metric-catalog-workbench > p {
  margin: 8px 0 0;
  color: #69758c;
  font-size: 12px;
}

.metric-catalog-heading,
.metric-catalog-heading > div {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.metric-catalog-heading {
  margin: 9px 0;
}

.metric-catalog-heading > div {
  align-items: flex-start;
  flex-direction: column;
}

.metric-catalog-heading span,
.metric-domain-grid span,
.metric-definition-list small {
  color: #69758c;
  font-size: 12px;
}

.metric-component-strip {
  display: flex;
  flex-wrap: wrap;
  gap: 7px;
  margin: 8px 0;
}

.metric-domain-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 7px;
}

.metric-domain-grid article,
.metric-definition-list article {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  padding: 7px;
  border: 1px solid rgba(42, 137, 118, 0.16);
  border-radius: 7px;
  background: rgba(255, 255, 255, 0.86);
}

.metric-domain-grid article {
  flex-direction: column;
}

.metric-cardinality-guard {
  display: grid;
  gap: 4px;
  margin: 8px 0;
  padding: 8px;
  border-radius: 7px;
  background: #edf6ff;
  color: #48627e;
  font-size: 12px;
  overflow-wrap: anywhere;
}

.metric-definition-list {
  margin-top: 8px;
}

.metric-definition-list > div {
  max-height: 320px;
  overflow-y: auto;
}

.metric-definition-list article {
  margin-top: 6px;
  align-items: flex-start;
}

.metric-definition-list article > div {
  display: grid;
  gap: 2px;
}

@media (max-width: 760px) {
  .case-shadow-input {
    grid-template-columns: 1fr;
  }

  .case-shadow-input button {
    min-height: 38px;
  }
}

.tool-runtime-panel {
  margin: 12px 20px 0;
  padding: 15px;
  border: 1px solid rgba(64, 90, 125, 0.18);
  border-radius: 14px;
  background: linear-gradient(145deg, rgba(246, 249, 255, 0.98), rgba(238, 244, 252, 0.98));
  box-shadow: 0 8px 24px rgba(45, 67, 99, 0.08);
}

.tool-runtime-header,
.tool-runtime-title,
.tool-result-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.tool-runtime-header > div {
  display: grid;
  gap: 3px;
}

.tool-runtime-header .tool-runtime-header-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.tool-runtime-header-actions button {
  padding: 6px 10px;
  border: 1px solid rgba(64, 90, 125, 0.2);
  border-radius: 8px;
  background: #fff;
  color: #405a7d;
  cursor: pointer;
  font-size: 12px;
  font-weight: 700;
}

.tool-evaluation-panel {
  margin-top: 11px;
  padding: 11px;
  border: 1px solid rgba(52, 168, 121, 0.2);
  border-radius: 10px;
  background: rgba(255, 255, 255, 0.92);
}

.tool-evaluation-summary {
  display: grid;
  gap: 9px;
  color: #5c6680;
  font-size: 12px;
}

.tool-evaluation-metrics {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: 6px;
}

.tool-evaluation-metrics span {
  padding: 7px 8px;
  border-radius: 7px;
  background: #eef7f3;
  color: #28795b;
  font-weight: 700;
}

.tool-runtime-header span,
.tool-runtime-card p,
.tool-result,
.tool-runtime-empty {
  color: #60718a;
  font-size: 12px;
}

.tool-runtime-schema,
.tool-runtime-title span {
  padding: 4px 8px;
  border-radius: 999px;
  background: rgba(64, 90, 125, 0.1);
  color: #405a7d;
  font-weight: 700;
}

.tool-runtime-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(290px, 1fr));
  gap: 10px;
  margin-top: 12px;
}

.tool-agent-console {
  display: flex;
  gap: 8px;
  margin-top: 12px;
}

.tool-agent-console input {
  flex: 1;
  min-width: 220px;
  padding: 9px 11px;
  border: 1px solid rgba(64, 90, 125, 0.2);
  border-radius: 9px;
  outline: none;
}

.tool-agent-console button {
  padding: 8px 13px;
  border: none;
  border-radius: 9px;
  background: #7057b5;
  color: #fff;
  font-weight: 700;
  cursor: pointer;
}

.tool-agent-result {
  margin-top: 10px;
  padding: 11px;
  border: 1px solid rgba(112, 87, 181, 0.2);
  border-radius: 10px;
  background: rgba(255, 255, 255, 0.92);
  color: #5c6680;
  font-size: 12px;
}

.tool-agent-result ol {
  margin: 8px 0;
  padding-left: 20px;
}

.tool-agent-governance {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 8px;
}

.tool-agent-governance span {
  padding: 4px 8px;
  border-radius: 999px;
  background: #fff4d8;
  color: #8a5a00;
  font-size: 12px;
}

.tool-agent-result li {
  margin: 5px 0;
}

.tool-agent-result li span {
  color: #258965;
  font-weight: 700;
}

.tool-agent-result pre {
  max-height: 240px;
  overflow: auto;
  padding: 9px;
  border-radius: 8px;
  background: #172233;
  color: #d8e8fb;
  white-space: pre-wrap;
}

.tool-runtime-card,
.tool-result {
  padding: 12px;
  border: 1px solid rgba(64, 90, 125, 0.14);
  border-radius: 11px;
  background: #fff;
}

.tool-runtime-card p {
  margin: 8px 0;
  line-height: 1.55;
}

.tool-runtime-card button {
  margin-top: 9px;
  padding: 7px 11px;
  border: none;
  border-radius: 8px;
  background: #405a7d;
  color: #fff;
  cursor: pointer;
  font-weight: 700;
}

.tool-health-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 7px;
}

.tool-runtime-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 5px;
}

.tool-runtime-tags span {
  padding: 3px 6px;
  border-radius: 6px;
  background: #eef3fa;
  color: #526680;
  font-size: 11px;
}

.tool-result {
  display: grid;
  gap: 6px;
  margin-top: 11px;
}

.tool-result.success { border-left: 4px solid #34a879; }
.tool-result.failed { border-left: 4px solid #d66b6b; }

.tool-stale-warning {
  padding: 7px 9px;
  border-radius: 7px;
  background: #fff4d8;
  color: #875d0d;
  font-weight: 700;
}

.tool-result pre {
  max-height: 220px;
  margin: 4px 0;
  padding: 10px;
  overflow: auto;
  border-radius: 8px;
  background: #172233;
  color: #d8e8fb;
  font-size: 11px;
  white-space: pre-wrap;
}

.routing-meta {
  margin-top: 9px;
  color: #8492a6;
  font-size: 11px;
  letter-spacing: 0.01em;
}

.shadow-intent-meta {
  margin-top: 4px;
  color: #7a5daf;
  font-weight: 650;
}

.chat-citations {
  display: grid;
  gap: 4px;
  margin-top: 8px;
  padding-top: 7px;
  border-top: 1px dashed rgba(64, 158, 255, 0.25);
  color: #607d9b;
  font-size: 11px;
}

.upload-btn {
  background: linear-gradient(135deg, #f093fb 0%, #f5576c 100%);
  color: white;
  padding: 8px 14px;
  border: none;
  border-radius: 10px;
  cursor: pointer;
  font-size: 13px;
  font-weight: 600;
  box-shadow: 0 4px 12px rgba(245, 87, 108, 0.2);
  transition: all 0.2s ease;
}

.upload-btn:hover:not(:disabled) {
  transform: translateY(-2px);
  box-shadow: 0 6px 16px rgba(245, 87, 108, 0.3);
}

.upload-btn:disabled {
  background: #ccc;
  box-shadow: none;
  cursor: not-allowed;
}

.chat-messages {
  flex: 1;
  min-height: 120px;
  overflow-y: auto;
  padding: 30px;
  display: flex;
  flex-direction: column;
  gap: 18px;
  position: relative;
  z-index: 1;
}

/* scrollbar */
.chat-messages::-webkit-scrollbar {
  width: 8px;
}
.chat-messages::-webkit-scrollbar-thumb {
  background: rgba(0,0,0,0.12);
  border-radius: 8px;
}
.chat-messages::-webkit-scrollbar-track {
  background: transparent;
}

.message {
  max-width: 70%;
  padding: 14px 18px;
  border-radius: 18px;
  line-height: 1.6;
  word-wrap: break-word;
  position: relative;
  animation: messageSlideIn 0.28s ease-out;
  font-size: 15px;
  box-sizing: border-box;
}

@keyframes messageSlideIn {
  from {
    opacity: 0;
    transform: translateY(12px) scale(0.98);
  }
  to {
    opacity: 1;
    transform: translateY(0) scale(1);
  }
}

.user-message {
  align-self: flex-end;
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  color: white;
  box-shadow: 0 6px 20px rgba(102, 126, 234, 0.16);
}

.user-message::after {
  content: '';
  position: absolute;
  bottom: -6px;
  right: 18px;
  width: 0;
  height: 0;
  border-left: 8px solid transparent;
  border-right: 8px solid transparent;
  border-top: 8px solid #764ba2;
}

.ai-message {
  align-self: flex-start;
  background: rgba(255, 255, 255, 0.95);
  backdrop-filter: blur(4px);
  color: #2c3e50;
  box-shadow: 0 6px 20px rgba(0, 0, 0, 0.06);
  border: 1px solid rgba(255, 255, 255, 0.3);
}

.ai-message::after {
  content: '';
  position: absolute;
  bottom: -6px;
  left: 18px;
  width: 0;
  height: 0;
  border-left: 8px solid transparent;
  border-right: 8px solid transparent;
  border-top: 8px solid rgba(255, 255, 255, 0.95);
}

.message-header {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 8px;
}

.message-header b {
  font-weight: 600;
}

.tts-btn {
  padding: 6px 10px;
  border-radius: 8px;
  font-size: 12px;
  cursor: pointer;
  background: linear-gradient(135deg, #67c23a 0%, #409eff 100%);
  color: white;
  border: none;
  transition: all 0.18s ease;
  box-shadow: 0 2px 8px rgba(103, 194, 58, 0.18);
}

.tts-btn:hover {
  transform: scale(1.05);
  box-shadow: 0 4px 12px rgba(103, 194, 58, 0.25);
}

.streaming-indicator {
  color: #999;
  font-weight: 600;
  margin-left: 6px;
}

/* message content */
.message-content {
  white-space: pre-wrap;
  word-break: break-word;
}

/* input area */
.chat-input {
  padding: 24px;
  background: rgba(255, 255, 255, 0.96);
  backdrop-filter: blur(8px);
  border-top: 1px solid rgba(0, 0, 0, 0.06);
  position: relative;
  z-index: 1;
  display: flex;
  align-items: flex-end;
  gap: 12px;
  flex: 0 0 auto;
}

@media (max-height: 800px) {
  .capability-workspace {
    max-height: 54vh;
  }

  .chat-messages {
    min-height: 96px;
    padding-top: 18px;
    padding-bottom: 18px;
  }
}

/* textarea 样式已移至 .input-wrapper textarea */

.send-btn {
  padding: 12px 22px;
  border: none;
  border-radius: 50px;
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  color: white;
  font-size: 15px;
  font-weight: 600;
  cursor: pointer;
  box-shadow: 0 6px 20px rgba(102,126,234,0.18);
  transition: all 0.18s ease;
  flex-shrink: 0;
}

.send-btn:hover:not(:disabled) {
  transform: translateY(-3px) scale(1.02);
}

.send-btn:disabled {
  background: #ccc;
  box-shadow: none;
  cursor: not-allowed;
}

/* 输入框包裹器 */
.input-wrapper {
  position: relative;
  flex: 1;
}

.input-wrapper textarea {
  width: 100%;
  resize: none;
  border: 2px solid rgba(0, 0, 0, 0.06);
  border-radius: 12px;
  padding: 14px 16px;
  font-size: 15px;
  outline: none;
  background: rgba(255,255,255,0.96);
  color: #2c3e50;
  transition: all 0.18s ease;
  min-height: 20px;
  max-height: 160px;
  box-shadow: 0 2px 10px rgba(0,0,0,0.04);
  box-sizing: border-box;
}

.input-wrapper textarea:focus {
  border-color: #409eff;
  box-shadow: 0 8px 30px rgba(64,158,255,0.06);
  transform: translateY(-1px);
}

</style>
