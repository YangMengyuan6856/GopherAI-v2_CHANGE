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
        <button class="interview-demo-toggle" :class="{ 'workspace-active': interviewDemoOpen }" :aria-pressed="interviewDemoOpen" :disabled="loadingInterviewDemo" @click="toggleInterviewDemo">🎤 面试导览</button>
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
        <button class="human-review-toggle" :class="{ 'workspace-active': humanReviewOpen }" :aria-pressed="humanReviewOpen" @click="humanReviewOpen = true">✅ 人工验收</button>
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

      <HumanReviewWorkbench v-if="humanReviewOpen" @close="closeHumanReviewWorkbench" />

      <div
        v-show="interviewDemoOpen || policyControlOpen || toolRuntimeOpen || evaluationCatalogOpen || knowledgeDocuments.length > 0 || knowledgeSearchOpen || memoryPreviewOpen || diagnosticMode"
        class="capability-workspace"
        aria-label="当前能力工作区"
      >

      <section v-if="interviewDemoOpen" class="interview-demo-panel" aria-label="3 到 5 分钟面试导览">
        <div class="interview-demo-header">
          <div>
            <small>GopherAI DevSupport · LIVE WALKTHROUGH</small>
            <strong>3–5 分钟讲清：场景、证据、Agent、治理与反馈闭环</strong>
            <span>一次只讲一个重点；所有数字来自当前只读 API 或不可变报告。</span>
          </div>
          <span class="interview-demo-readonly">READ ONLY · 不切流</span>
        </div>

        <div class="interview-demo-proof-grid">
          <article>
            <strong>{{ interviewEvidence?.release_id || '证据包待就绪' }}</strong>
            <span>当前 Release</span>
          </article>
          <article>
            <strong>{{ evaluationCatalog ? `${evaluationCatalog.actual_total}/${evaluationCatalog.expected_total}` : '读取中' }}</strong>
            <span>固定评测目录</span>
          </article>
          <article>
            <strong>{{ interviewEvidence ? `${interviewEvidence.resume_ready_claims}/${interviewEvidence.total_claims}` : '读取中' }}</strong>
            <span>简历证据就绪</span>
          </article>
          <article>
            <strong>{{ interviewEvidence?.all_sources_verified ? 'Hash 全通过' : '等待证据' }}</strong>
            <span>来源可追溯</span>
          </article>
        </div>

        <div v-if="loadingInterviewDemo" class="interview-demo-loading">正在读取当前 Release、Full 320 与证据包，不执行任何验收动作...</div>
        <div v-else-if="interviewDemoLoadWarning" class="interview-demo-warning">{{ interviewDemoLoadWarning }}</div>

        <nav class="interview-demo-tabs" aria-label="面试导览步骤">
          <button
            v-for="(step, index) in interviewDemoSteps"
            :key="step.id"
            :class="{ active: interviewDemoStep === index }"
            :aria-current="interviewDemoStep === index ? 'step' : undefined"
            @click="interviewDemoStep = index"
          >{{ index + 1 }} · {{ step.shortTitle }}</button>
        </nav>

        <article class="interview-demo-step-card">
          <div class="interview-demo-step-heading">
            <div>
              <small>{{ currentInterviewDemoStep.timebox }}</small>
              <strong>{{ currentInterviewDemoStep.title }}</strong>
            </div>
            <span>{{ currentInterviewDemoStep.badge }}</span>
          </div>
          <p class="interview-demo-thesis">{{ currentInterviewDemoStep.thesis }}</p>
          <div class="interview-demo-three-column">
            <div><small>现场操作</small><strong>{{ currentInterviewDemoStep.action }}</strong></div>
            <div><small>预期证据</small><strong>{{ currentInterviewDemoStep.proof }}</strong></div>
            <div><small>主动披露边界</small><strong>{{ currentInterviewDemoStep.boundary }}</strong></div>
          </div>
          <details class="interview-question-card">
            <summary>高频追问：{{ currentInterviewDemoStep.question }}</summary>
            <p>{{ currentInterviewDemoStep.answer }}</p>
          </details>
          <div class="interview-demo-actions">
            <button :disabled="interviewDemoStep === 0" @click="interviewDemoStep--">上一步</button>
            <button class="interview-demo-open-workspace" @click="openInterviewDemoWorkspace(currentInterviewDemoStep.workspace)">{{ currentInterviewDemoStep.workspaceLabel }}</button>
            <button :disabled="interviewDemoStep === interviewDemoSteps.length - 1" @click="interviewDemoStep++">下一步</button>
          </div>
        </article>

        <div class="interview-demo-stack">
          <span>Go 1.24</span><span>Vue</span><span>MySQL 权威状态</span><span>Redis Cache + Vector</span>
          <span>RabbitMQ</span><span>MCP</span><span>Prometheus</span><span>Grafana</span><span>LLM-as-a-Judge</span>
        </div>
      </section>

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
            <details v-if="pairedComparison" class="paired-comparison-card">
              <summary>查看成对 A/B 统计可信度（{{ pairedComparison.comparisons.length }} 个实验）</summary>
              <div class="metric-catalog-heading">
                <div>
                  <strong>Paired Comparison · {{ pairedComparison.method_version }}</strong>
                  <span>同一问题逐对比较 · 固定种子 Bootstrap 95% CI · 精确双侧 McNemar</span>
                </div>
                <span :class="['evaluation-gate', pairedComparison.human_reviewed ? 'passed' : 'failed']">
                  {{ pairedComparison.human_reviewed ? '人工标签已复核' : '统计候选 · 不可晋级' }}
                </span>
              </div>
              <div class="paired-comparison-grid">
                <article v-for="comparison in pairedComparison.comparisons" :key="comparison.name">
                  <div class="evaluation-run-heading">
                    <div>
                      <strong>{{ pairedComparisonLabel(comparison.name) }}</strong>
                      <span>{{ comparison.baseline_strategy }} → {{ comparison.candidate_strategy }} · n={{ comparison.analysis.pair_count }}</span>
                    </div>
                    <span :class="comparison.analysis.conclusion === 'candidate_better' ? 'dependency-ready' : 'dependency-down'">
                      {{ pairedConclusionLabel(comparison.analysis.conclusion) }}
                    </span>
                  </div>
                  <div class="diagnostic-evaluation-grid">
                    <div><strong>{{ comparison.analysis.baseline_success.numerator }}/{{ comparison.analysis.baseline_success.denominator }}</strong><span>基线成功（≥{{ metricPercent(comparison.analysis.success_threshold) }}）</span></div>
                    <div><strong>{{ comparison.analysis.candidate_success.numerator }}/{{ comparison.analysis.candidate_success.denominator }}</strong><span>候选成功（同一分母）</span></div>
                    <div><strong>{{ metricPercent(comparison.analysis.mean_delta) }}</strong><span>成对平均质量差</span></div>
                    <div><strong>[{{ metricPercent(comparison.analysis.delta_ci95_lower) }}, {{ metricPercent(comparison.analysis.delta_ci95_upper) }}]</strong><span>Bootstrap 95% CI</span></div>
                    <div><strong>{{ comparison.analysis.wins }}/{{ comparison.analysis.losses }}/{{ comparison.analysis.ties }}</strong><span>胜 / 负 / 平</span></div>
                    <div><strong>{{ comparison.analysis.mcnemar_exact_two_sided_p_value.toFixed(4) }}</strong><span>McNemar 精确双侧 p</span></div>
                  </div>
                  <small>不一致对 {{ comparison.analysis.discordant_pairs }}（候选独赢 {{ comparison.analysis.candidate_only_successes }} / 基线独赢 {{ comparison.analysis.baseline_only_successes }}）· {{ comparison.analysis.bootstrap_iterations }} 次 · seed {{ comparison.analysis.bootstrap_seed }}</small>
                </article>
              </div>
              <div class="evaluation-artifact-list">
                <span v-for="source in pairedComparison.sources" :key="source.name">
                  {{ source.name }} · {{ source.dataset_version }} · SHA {{ source.report_sha256.slice(0, 16) }}…
                </span>
              </div>
              <div class="evaluation-candidate-warning">
                <strong>统计显著 ≠ 允许上线</strong>
                <span>当前 PromotionEligible={{ pairedComparison.promotion_eligible }}；输入标签未完成人工复核，两个实验也禁止合并成一个总体收益率。</span>
              </div>
              <small>Analysis SHA-256 {{ pairedComparison.analysis_sha256 }}</small>
            </details>
            <details class="judge-calibration-card">
              <summary>打开 LLM-as-a-Judge 人工校准（{{ judgeCalibrationAudit?.agreement?.reviewed_cases || 0 }}/30）</summary>
              <div class="metric-catalog-heading">
                <div>
                  <strong>Human Calibration · {{ judgeCalibrationAudit?.judge_prompt || 'judge-rubric-v2' }}</strong>
                  <span>30 条 / 6 个切片 · 五维人工评分 · 线性加权 Cohen’s κ ≥ 0.70</span>
                </div>
                <button :disabled="loadingJudgeCalibration" @click="loadJudgeCalibration">
                  {{ loadingJudgeCalibration ? '读取中...' : '刷新校准报告' }}
                </button>
              </div>
              <div v-if="!judgeCalibrationAudit" class="strategy-control-empty">真实 Judge 报告尚未生成或正在生成；不会用模拟分数冒充人工校准。</div>
              <template v-else>
                <div class="judge-calibration-summary">
                  <div><strong>{{ judgeCalibrationAudit.agreement.reviewed_cases }}/{{ judgeCalibrationAudit.agreement.required_cases }}</strong><span>当前复核进度</span></div>
                  <div><strong>{{ judgeCalibrationAudit.agreement.reviewed_cases === 30 ? judgeCalibrationAudit.agreement.linear_weighted_kappa.toFixed(4) : '待满 30 条' }}</strong><span>线性加权 κ</span></div>
                  <div><strong>{{ metricPercent(judgeCalibrationAudit.agreement.exact_grade_agreement) }}</strong><span>综合等级精确一致</span></div>
                  <div><strong>{{ metricPercent(judgeCalibrationAudit.agreement.within_one_grade) }}</strong><span>相差不超过一级</span></div>
                  <div><strong>{{ judgeCalibrationAudit.agreement.calibration_gate_passed ? '通过' : '未通过' }}</strong><span>κ ≥ 0.70 门禁</span></div>
                  <div><strong>{{ judgeCalibrationAudit.agreement.automation_use_permitted ? '允许' : '禁止' }}</strong><span>用于自动控制</span></div>
                </div>
                <article v-if="currentJudgeCalibrationCase" class="judge-calibration-case">
                  <div class="evaluation-run-heading">
                    <div>
                      <strong>{{ currentJudgeCalibrationCase.id }} · {{ judgeCalibrationSliceLabel(currentJudgeCalibrationCase.slice) }}</strong>
                      <span>{{ judgeCalibrationIndex + 1 }}/{{ judgeCalibrationAudit.case_count }} · {{ currentJudgeCalibrationCase.task_type }}</span>
                    </div>
                    <div class="judge-calibration-nav">
                      <button :disabled="judgeCalibrationIndex === 0" @click="selectJudgeCalibrationCase(judgeCalibrationIndex - 1)">上一条</button>
                      <button :disabled="judgeCalibrationIndex >= judgeCalibrationAudit.case_count - 1" @click="selectJudgeCalibrationCase(judgeCalibrationIndex + 1)">下一条</button>
                    </div>
                  </div>
                  <div class="judge-calibration-content">
                    <p><strong>问题：</strong>{{ currentJudgeCalibrationCase.question }}</p>
                    <p><strong>待评答案：</strong>{{ currentJudgeCalibrationCase.answer }}</p>
                    <div><strong>允许证据：</strong><span v-if="!currentJudgeCalibrationCase.evidence.length">无（应拒绝猜测）</span><span v-for="evidence in currentJudgeCalibrationCase.evidence" :key="evidence.id">[{{ evidence.id }}] {{ evidence.content }}</span></div>
                    <p><strong>期望要点：</strong>{{ currentJudgeCalibrationCase.expected_facts.join('；') || '无具体事实，应正确拒答' }}</p>
                    <p><strong>禁止声明：</strong>{{ currentJudgeCalibrationCase.forbidden_claims.join('；') }}</p>
                  </div>
                  <div class="judge-score-form">
                    <label v-for="dimension in judgeCalibrationDimensions" :key="dimension.value">
                      <span>{{ dimension.label }}</span>
                      <select v-model="judgeCalibrationDraft[dimension.value]">
                        <option value="" disabled>请选择</option>
                        <option v-for="option in judgeCalibrationScoreOptions" :key="option.value" :value="option.value">{{ option.label }}</option>
                      </select>
                    </label>
                  </div>
                  <div class="judge-calibration-actions">
                    <small v-if="!currentJudgeCalibrationCase.human_scores">为避免锚定偏差，Judge 分数会在你提交人工评分后显示。</small>
                    <small v-else>已保存 Revision {{ currentJudgeCalibrationCase.review_revision }}；再次提交不同分数会追加修订，不覆盖历史。</small>
                    <button :disabled="submittingJudgeReview || !judgeCalibrationDraftComplete" @click="submitJudgeCalibrationReview">
                      {{ submittingJudgeReview ? '保存中...' : (currentJudgeCalibrationCase.human_scores ? '追加评分修订' : '提交人工评分') }}
                    </button>
                  </div>
                  <div v-if="currentJudgeCalibrationCase.human_scores" class="judge-score-comparison">
                    <span v-for="dimension in judgeCalibrationDimensions" :key="dimension.value">
                      {{ dimension.label }}：人工 {{ metricPercent(currentJudgeCalibrationCase.human_scores[dimension.value]) }} / Judge {{ metricPercent(currentJudgeCalibrationCase.judge.scores[dimension.value]) }}
                    </span>
                    <small>Judge confidence {{ metricPercent(currentJudgeCalibrationCase.judge.confidence) }} · {{ currentJudgeCalibrationCase.judge.status }}</small>
                  </div>
                </article>
                <div class="judge-case-index" aria-label="Judge 校准用例索引">
                  <button v-for="(item, index) in judgeCalibrationAudit.cases" :key="item.id" :class="{ reviewed: !!item.human_scores, active: index === judgeCalibrationIndex }" @click="selectJudgeCalibrationCase(index)">{{ index + 1 }}</button>
                </div>
                <div class="evaluation-candidate-warning">
                  <strong>{{ judgeCalibrationAudit.agreement.status === 'rubric_revision_required' ? '一致性不足：先修 Rubric' : (judgeCalibrationAudit.agreement.calibration_gate_passed ? '校准门通过，仍需人工发布审批' : '人工校准尚未完成') }}</strong>
                  <span>Judge={{ judgeCalibrationAudit.judge_model }}；报告 SHA {{ judgeCalibrationAudit.report_sha256.slice(0, 16) }}…；任何时候都不会直接写活动策略。</span>
                </div>
              </template>
            </details>
            <details v-if="interviewEvidence" class="interview-evidence-card">
              <summary>打开可复现面试证据包（{{ interviewEvidence.resume_ready_claims }}/{{ interviewEvidence.total_claims }} 条简历指标就绪）</summary>
              <div class="metric-catalog-heading">
                <div>
                  <strong>Evidence Package · {{ interviewEvidence.release_id }}</strong>
                  <span>来源均经 Hash 校验 · Package SHA {{ interviewEvidence.package_sha256.slice(0, 16) }}…</span>
                </div>
                <div class="interview-evidence-actions">
                  <button :disabled="downloadingInterviewEvidence" @click="downloadInterviewEvidence('json')">下载 JSON</button>
                  <button :disabled="downloadingInterviewEvidence" @click="downloadInterviewEvidence('markdown')">下载 Markdown</button>
                </div>
              </div>
              <div class="interview-evidence-summary">
                <div><strong>{{ interviewEvidence.resume_ready_claims }}/{{ interviewEvidence.total_claims }}</strong><span>简历指标就绪</span></div>
                <div><strong>{{ interviewEvidence.all_sources_verified ? '全部通过' : '存在漂移' }}</strong><span>来源 Hash 校验</span></div>
                <div><strong>{{ interviewEvidence.build_strategy }}</strong><span>{{ interviewEvidence.target }}</span></div>
              </div>
              <div class="interview-evidence-grid">
                <article v-for="statement in interviewEvidence.statements" :key="statement.id">
                  <div class="interview-evidence-heading">
                    <strong>{{ statement.title }}</strong>
                    <span :class="{ ready: statement.resume_metric_eligible, blocked: !statement.resume_metric_eligible }">{{ interviewEvidenceStatusLabel(statement.status) }}</span>
                  </div>
                  <p>{{ statement.claim }}</p>
                  <div class="interview-evidence-metrics">
                    <span v-for="metric in statement.metrics" :key="metric.name">
                      {{ interviewEvidenceMetricLabel(metric.name) }}：{{ formatInterviewEvidenceMetric(metric) }}
                    </span>
                  </div>
                  <small>来源：{{ statement.source_refs.join(' · ') }}</small>
                  <small v-if="statement.blockers.length">阻塞：{{ statement.blockers.join(' · ') }}</small>
                  <details>
                    <summary>查看禁止夸大的边界</summary>
                    <ul><li v-for="item in statement.forbidden_overclaims" :key="item">{{ item }}</li></ul>
                  </details>
                </article>
              </div>
              <div class="evaluation-candidate-warning">
                <strong>证据可追溯，不等于所有数字都可写简历</strong>
                <span>质量收益仍受人工复核或净收益门阻断；可靠性、故障演练与控制器数字必须保留“隔离验收 / Observe-only / 不切流”边界。</span>
              </div>
            </details>
            <details v-if="g10Review" class="interview-evidence-card g10-review-card">
              <summary>打开 G10 发布事实核验（{{ g10Review.passed_gates }}/{{ g10Review.total_gates }} 门通过 · {{ g10ReviewStatusLabel(g10Review.status) }}）</summary>
              <div class="metric-catalog-heading">
                <div>
                  <strong>G10 Release Review · {{ g10Review.release_id }}</strong>
                  <span>Evidence {{ shortRevision(g10Review.evidence_package_sha256) }} · Report {{ shortRevision(g10Review.report_sha256) }}</span>
                </div>
                <div class="interview-evidence-actions">
                  <button :disabled="downloadingG10Review" @click="downloadG10Review('json')">下载 JSON</button>
                  <button :disabled="downloadingG10Review" @click="downloadG10Review('markdown')">下载中文核验表</button>
                </div>
              </div>
              <div class="interview-evidence-summary">
                <div><strong>{{ g10Review.passed_gates }}/{{ g10Review.total_gates }}</strong><span>G10 总门</span></div>
                <div><strong>{{ g10Review.resume_fact_count }}</strong><span>通过技术证据门</span></div>
                <div><strong>{{ g10Review.excluded_fact_count }}</strong><span>禁止写入简历</span></div>
                <div><strong>{{ g10Review.production_release_ready ? '允许' : '禁止' }}</strong><span>生产总门</span></div>
              </div>
              <div v-if="g10Review.human_gate_progress" class="g10-human-progress">
                <article>
                  <div>
                    <strong>Full 320 人工队列 · {{ g10Review.human_gate_progress.catalog_review.reviewed }}/{{ g10Review.human_gate_progress.catalog_review.total }}</strong>
                    <span>通过 {{ g10Review.human_gate_progress.catalog_review.approved }} · 退回 {{ g10Review.human_gate_progress.catalog_review.rejected }} · 待审 {{ g10Review.human_gate_progress.catalog_review.pending }}</span>
                  </div>
                  <small>Review Set {{ shortRevision(g10Review.human_gate_progress.catalog_review.review_set_sha256) }} · {{ g10Review.human_gate_progress.catalog_review.ready_for_sealed_materialization ? '可进入独立封存' : '尚未达到封存条件' }}</small>
                  <div class="g10-seal-progress">
                    <strong>不可变封存候选 · {{ g10Review.human_gate_progress.catalog_sealing.candidate_ready ? '已生成并复验' : (g10Review.human_gate_progress.catalog_sealing.eligible ? '等待显式生成' : '未准入') }}</strong>
                    <span v-if="g10Review.human_gate_progress.catalog_sealing.candidate_ready">Seal {{ shortRevision(g10Review.human_gate_progress.catalog_sealing.seal_sha256) }} · 仍不是正式基线</span>
                    <span v-else>{{ g10Review.human_gate_progress.catalog_sealing.next_gate }}</span>
                  </div>
                  <div class="g10-rerun-progress">
                    <strong>固定技术重跑 · {{ g10CatalogRerunLabel(g10Review.human_gate_progress.catalog_rerun.state) }}</strong>
                    <span v-if="g10Review.human_gate_progress.catalog_rerun.run_id">
                      Run {{ shortRevision(g10Review.human_gate_progress.catalog_rerun.run_id) }} · {{ g10Review.human_gate_progress.catalog_rerun.completed_steps }}/6 步 · 证据{{ g10Review.human_gate_progress.catalog_rerun.evidence_integrity_verified ? '已复验' : '未复验' }} · 不自动晋级
                    </span>
                    <span v-else>{{ g10Review.human_gate_progress.catalog_rerun.next_gate }}</span>
                  </div>
                </article>
                <article>
                  <div>
                    <strong>Judge 人工校准 · {{ g10Review.human_gate_progress.judge_calibration.reviewed }}/{{ g10Review.human_gate_progress.judge_calibration.total }}</strong>
                    <span>κ {{ g10Review.human_gate_progress.judge_calibration.kappa_available ? g10Review.human_gate_progress.judge_calibration.linear_weighted_kappa.toFixed(4) : '待满 30 条后计算' }}</span>
                  </div>
                  <small>{{ g10Review.human_gate_progress.judge_calibration.calibration_gate_passed ? '校准门通过' : '校准门未通过' }} · 队列完成也不会跳过独立封存与统一重跑</small>
                </article>
                <button type="button" @click="humanReviewOpen = true">进入人工验收</button>
              </div>
              <div class="g10-gate-grid">
                <article v-for="gate in g10Review.gates" :key="gate.id">
                  <div class="interview-evidence-heading">
                    <strong>{{ gate.title }}</strong>
                    <span :class="g10GateStatusClass(gate.status)">{{ g10GateStatusLabel(gate.status) }}</span>
                  </div>
                  <p>{{ gate.conclusion }}</p>
                  <small>证据：{{ gate.evidence_refs.join(' · ') }}</small>
                  <small>下一步：{{ gate.next_action }}</small>
                </article>
              </div>
              <details class="g10-fact-list">
                <summary>查看 {{ g10Review.resume_fact_count }} 条通过技术证据门的事实（{{ g10Review.resume_confirmation?.current_binding ? `已确认 ${g10Review.resume_confirmation.selected_count} 条` : '仍需用户选择最终 3～5 条' }}）</summary>
                <article v-for="fact in g10Review.resume_facts" :key="fact.statement_id">
                  <label class="g10-fact-choice">
                    <input v-model="selectedResumeFactIDs" type="checkbox" :value="fact.statement_id">
                    <strong>{{ fact.title }}</strong>
                  </label>
                  <p>{{ fact.claim }}</p>
                  <small>来源：{{ fact.source_refs.join(' · ') }}</small>
                  <small>表述必须保留：{{ fact.required_qualifiers.join('；') }}</small>
                </article>
              </details>
              <section class="g10-confirmation-panel">
                <div>
                  <strong>人工确认简历事实 · 已选 {{ selectedResumeFactIDs.length }}/3～5 条</strong>
                  <span v-if="g10Review.resume_confirmation?.current_binding">当前事实集已确认 · {{ shortRevision(g10Review.resume_confirmation.confirmation_sha256) }}</span>
                  <span v-else-if="g10Review.resume_confirmation">旧确认已失效：证据事实或限定语发生变化，请重新选择。</span>
                  <span v-else>机器只验证证据；最终采用哪些数字必须由你本人确认。</span>
                </div>
                <label class="g10-qualifier-ack">
                  <input v-model="resumeQualifierAcknowledged" type="checkbox">
                  我确认只使用所选事实，并在简历和面试中保留每条事实列出的全部限定语。
                </label>
                <button :disabled="!g10ResumeSelectionValid || submittingResumeConfirmation" @click="submitG10ResumeConfirmation">
                  {{ submittingResumeConfirmation ? '记录中...' : (g10Review.resume_confirmation?.current_binding ? '追加新的确认版本' : '确认所选 3～5 条') }}
                </button>
              </section>
              <details class="g10-fact-list excluded">
                <summary>查看 {{ g10Review.excluded_fact_count }} 条当前禁止写入简历的事实</summary>
                <article v-for="fact in g10Review.excluded_facts" :key="fact.statement_id">
                  <strong>{{ fact.title }} · {{ interviewEvidenceStatusLabel(fact.status) }}</strong>
                  <small>阻塞：{{ fact.blockers.length ? fact.blockers.join(' · ') : '当前结论属于负结果，不作为质量提升表述' }}</small>
                </article>
              </details>
              <div class="evaluation-candidate-warning">
                <strong>当前 G10 明确未通过，不把“技术可运行”包装成“生产发布完成”</strong>
                <span>待办：人工标签/Judge 校准{{ g10Review.resume_confirmation?.current_binding ? '' : '、用户确认最终简历数字' }}；环境延期：真实生产回滚和百分比灰度。</span>
              </div>
            </details>
            <details class="cleanup-audit-card">
              <summary>打开清理依赖与零调用审计（{{ cleanupAudit ? (cleanupAudit.summary.cleanup_complete ? '清理闭环已完成' : `${cleanupAudit.summary.eligible_to_delete} 个可删除候选`) : '尚未运行' }}）</summary>
              <div class="metric-catalog-heading">
                <div>
                  <strong>M10 Cleanup Gate · 只审计，不在页面删除</strong>
                  <span>固定源码符号扫描 + Prometheus 24h 退役入口观测 + 替代链路声明</span>
                </div>
                <button :disabled="runningCleanupAudit" @click="runCleanupAudit">
                  {{ runningCleanupAudit ? '审计中...' : '运行清理前审计' }}
                </button>
              </div>
              <div v-if="cleanupAudit">
                <div class="interview-evidence-summary">
                  <div><strong>{{ cleanupAudit.legacy_entry_observation.attempt_count }}</strong><span>旧 Skill API · 24h 调用</span></div>
                  <div><strong>{{ metricPercent(cleanupAudit.legacy_entry_observation.coverage_ratio) }}</strong><span>Prometheus 采样覆盖</span></div>
                  <div><strong>{{ cleanupAudit.summary.already_removed }}</strong><span>已删除并复核</span></div>
                  <div><strong>{{ cleanupAudit.summary.eligible_to_delete }}</strong><span>零外部引用候选</span></div>
                  <div><strong>{{ cleanupAudit.summary.retained_required }}</strong><span>明确保留边界</span></div>
                  <div><strong>{{ cleanupAudit.summary.blocked }}</strong><span>仍有引用/授权阻断</span></div>
                </div>
                <div class="evaluation-decision-strip">
                  <span :class="cleanupAudit.legacy_entry_observation.zero_calls ? 'dependency-ready' : 'dependency-down'">{{ cleanupObservationLabel(cleanupAudit.legacy_entry_observation.status) }}</span>
                  <span :class="cleanupAudit.summary.cleanup_complete || cleanupAudit.summary.deletion_plan_ready ? 'dependency-ready' : 'dependency-down'">
                    {{ cleanupAudit.summary.cleanup_complete ? '清理闭环 已完成' : `删除清单 ${cleanupAudit.summary.deletion_plan_ready ? '已具备证据' : '尚未就绪'}` }}
                  </span>
                  <span>Tracked {{ cleanupAudit.tracked_source_count }} · Inventory {{ shortRevision(cleanupAudit.source_inventory_sha256) }}</span>
                  <span>Report {{ shortRevision(cleanupAudit.report_sha256) }}</span>
                </div>
                <div class="cleanup-candidate-grid">
                  <article v-for="candidate in cleanupAudit.candidates" :key="candidate.id">
                    <div class="interview-evidence-heading">
                      <strong>{{ candidate.title }}</strong>
                      <span :class="{ ready: candidate.status === 'eligible_for_deletion' || candidate.status === 'removed_verified', blocked: candidate.status === 'blocked_active_reference' || candidate.status === 'blocked_user_approval_required' }">{{ cleanupCandidateStatusLabel(candidate.status) }}</span>
                    </div>
                    <small>替代：{{ candidate.replacement }}</small>
                    <span>当前产物 {{ candidate.present_artifacts.length }} · 外部引用 {{ candidate.external_reference_count }}</span>
                    <small>原因：{{ candidate.reason_codes.join(' · ') }}</small>
                  </article>
                </div>
                <div :class="['evaluation-candidate-warning', { passed: cleanupAudit.summary.cleanup_complete }]">
                  <strong>{{ cleanupAudit.summary.cleanup_complete ? '审计驱动清理闭环已完成' : '报告就绪不等于页面直接删除' }}</strong>
                  <span>{{ cleanupAudit.summary.cleanup_complete ? `${cleanupAudit.summary.already_removed} 项授权候选均已通过独立 Git 提交删除并由当前 Release 复核；${cleanupAudit.summary.retained_required} 个运行所需协议边界明确保留。` : '物理删除仍以独立 Git 提交执行，并重新跑全量构建、评测 Smoke 与云端健康门；数据库 Contract migration 不在本报告授权范围。' }}</span>
                </div>
              </div>
              <div v-else class="strategy-control-empty">尚无持久化审计报告；点击后只读取固定指标并扫描当前 Release，不执行删除。</div>
            </details>
            <details class="anomaly-workbench">
              <summary>打开固定阈值 + 滑动窗口 Z-score 验收工作台</summary>
              <p>下方五个场景是确定性 Fixture；“读取生产窗口”只读取后台定时落入 MySQL 的真实 Prometheus 聚合。两类数据不会混算，所有结果仅生成 Recommend-only 建议。</p>
              <div class="anomaly-scenario-actions">
                <button class="production-window-button" :disabled="loadingProductionAnomaly" @click="loadProductionAnomaly">
                  {{ loadingProductionAnomaly ? '读取中...' : '读取生产窗口' }}
                </button>
                <button v-for="scenario in anomalyScenarios" :key="scenario.value" :disabled="loadingAnomaly" @click="simulateAnomaly(scenario.value)">
                  {{ scenario.label }}
                </button>
              </div>
              <article v-if="productionAnomaly" class="production-anomaly-card">
                <div class="evaluation-run-heading">
                  <div>
                    <strong>生产窗口 · {{ productionWindowStatusLabel(productionAnomaly.status) }}</strong>
                    <span>{{ productionAnomaly.source }} · 非模拟 · 每分钟持久化</span>
                  </div>
                  <span>{{ productionAnomaly.rules_version || '等待首批快照' }}</span>
                </div>
                <div v-if="productionAnomaly.series.length" class="production-anomaly-grid">
                  <article v-for="series in productionAnomaly.series" :key="`${series.metric}-${series.strategy}`">
                    <div class="metric-catalog-heading">
                      <div>
                        <strong>{{ anomalyMetricLabel(series.metric) }} · {{ series.strategy }}</strong>
                        <span>{{ series.window_seconds / 60 }}m 窗口 · {{ productionDataStatusLabel(series.data_status) }}</span>
                      </div>
                      <span :class="anomalyDecisionClass(series.analysis)">{{ series.analysis ? anomalyDecisionLabel(series.analysis.decision_status) : '尚无可分析数值' }}</span>
                    </div>
                    <div class="diagnostic-evaluation-grid">
                      <div><strong>{{ productionMetricValue(series) }}</strong><span>当前聚合值</span></div>
                      <div><strong>{{ series.latest.population }}</strong><span>当前窗口样本量</span></div>
                      <div><strong>{{ series.history_point_count }}</strong><span>同规则版本历史点</span></div>
                      <div><strong>{{ series.analysis?.recommendation?.applied ? '是' : '否' }}</strong><span>是否已修改线上策略</span></div>
                    </div>
                    <div v-if="series.analysis" class="anomaly-reason-line">{{ series.analysis.fixed_threshold.reason_code }} · {{ series.analysis.z_score.reason_code }} · {{ series.analysis.recommendation.mode }}</div>
                  </article>
                </div>
                <div v-else class="strategy-control-empty">等待后台完成第一批 Prometheus 窗口快照；页面读取不会触发写入。</div>
                <div class="evaluation-decision-strip">
                  <span v-for="guardrail in productionAnomaly.guardrails" :key="guardrail" class="dependency-ready">{{ guardrail }}</span>
                </div>
              </article>
              <details v-if="webhookAudit" class="webhook-audit-card">
                <summary>查看签名 Webhook 异步投递（{{ webhookAudit.delivered }} 成功 / {{ webhookAudit.dead_lettered }} Dead Letter）</summary>
                <div class="metric-catalog-heading">
                  <div>
                    <strong>Control Webhook · {{ webhookAudit.endpoint_mode }}</strong>
                    <span>{{ webhookAudit.signature }} · 最多 {{ webhookAudit.max_attempts }} 次尝试</span>
                  </div>
                  <button :disabled="runningWebhookAcceptance || !webhookAudit.enabled" @click="runWebhookAcceptance">
                    {{ runningWebhookAcceptance ? '异步投递中...' : '发送签名验收事件' }}
                  </button>
                </div>
                <p>该按钮发送明确标记为 Simulation 的固定事件，真实经过 MySQL 队列、HMAC-SHA256、HTTP 接收器与幂等回执；不会制造生产告警，也不会写活动策略。</p>
                <div class="diagnostic-evaluation-grid">
                  <div><strong>{{ webhookAudit.pending }}</strong><span>等待投递</span></div>
                  <div><strong>{{ webhookAudit.retrying }}</strong><span>等待重试</span></div>
                  <div><strong>{{ webhookAudit.delivered }}</strong><span>投递成功</span></div>
                  <div><strong>{{ webhookAudit.dead_lettered }}</strong><span>Dead Letter</span></div>
                </div>
                <div v-if="webhookAudit.latest.length" class="webhook-delivery-list">
                  <article v-for="delivery in webhookAudit.latest" :key="delivery.event_id">
                    <div><strong>{{ delivery.simulation ? '验收事件' : webhookEventLabel(delivery.event_type) }}</strong><span>{{ delivery.status }} · HTTP {{ delivery.http_status || '--' }}</span></div>
                    <div><span>尝试 {{ delivery.attempt }}/{{ webhookAudit.max_attempts }}</span><span>回执验签 {{ delivery.receipt_verified ? '通过' : '未完成' }}</span></div>
                    <small>Event {{ delivery.event_id.slice(0, 12) }}… · Payload SHA {{ delivery.payload_sha256.slice(0, 12) }}…</small>
                  </article>
                </div>
                <div v-else class="strategy-control-empty">尚无投递事件；生产检测只有在样本成熟并真实异常时才会自动入队。</div>
                <div class="evaluation-decision-strip">
                  <span v-for="guardrail in webhookAudit.guardrails" :key="guardrail" class="dependency-ready">{{ guardrail }}</span>
                </div>
              </details>
              <details v-if="controllerAudit" class="controller-audit-card">
                <summary>查看只建议控制器（{{ controllerAudit.recommended }} 条候选 / {{ controllerAudit.blocked }} 条被门禁阻断）</summary>
                <div class="metric-catalog-heading">
                  <div>
                    <strong>Recommend-only Controller · {{ controllerAudit.active_policy.version }}</strong>
                    <span>活动策略 SHA {{ controllerAudit.active_policy.sha256.slice(0, 12) }}… · {{ controllerAudit.active_policy.status }}</span>
                  </div>
                  <button :disabled="runningControllerAcceptance" @click="runControllerAcceptance">
                    {{ runningControllerAcceptance ? '验证门禁中...' : '运行只建议闭环验收' }}
                  </button>
                </div>
                <p>生产周期只消费成熟异常窗口。当前真实评测尚未完成人工复核，因此会被基线门阻断；验收 Fixture 仅证明合格输入可生成不可变候选，绝不激活。</p>
                <div class="evaluation-decision-strip">
                  <span :class="controllerAudit.evaluation.technical_gates_passed ? 'dependency-ready' : 'dependency-down'">技术门 {{ controllerAudit.evaluation.technical_gates_passed ? '通过' : '失败' }}</span>
                  <span :class="controllerAudit.evaluation.human_reviewed ? 'dependency-ready' : 'dependency-down'">人工复核 {{ controllerAudit.evaluation.human_reviewed ? '完成' : '待完成' }}</span>
                  <span :class="controllerAudit.evaluation.baseline_eligible ? 'dependency-ready' : 'dependency-down'">基线 {{ controllerAudit.evaluation.baseline_eligible ? '合格' : '不可用' }}</span>
                  <span class="dependency-ready">活动策略未改写</span>
                </div>
                <article v-if="controllerAcceptance" class="controller-acceptance-result">
                  <div class="evaluation-run-heading">
                    <div><strong>双门禁验收结果</strong><span>Simulation · 不冒充生产异常或正式基线</span></div>
                    <span :class="controllerAcceptance.active_policy_unchanged ? 'dependency-ready' : 'dependency-down'">活动策略 {{ controllerAcceptance.active_policy_unchanged ? '保持不变' : '发生变化' }}</span>
                  </div>
                  <div class="controller-decision-grid">
                    <div>
                      <strong>当前真实基线门</strong>
                      <span>{{ controllerStatusLabel(controllerAcceptance.baseline_guard.status) }} · {{ controllerReasonLabel(controllerAcceptance.baseline_guard.reason_code) }}</span>
                    </div>
                    <div>
                      <strong>合格基线 Fixture</strong>
                      <span>{{ controllerAcceptance.eligible_fixture.strategy }} {{ controllerWeight(controllerAcceptance.eligible_fixture.before_weight_basis) }} → {{ controllerWeight(controllerAcceptance.eligible_fixture.proposed_weight_basis) }}</span>
                      <small>{{ controllerAcceptance.eligible_fixture.fallback_strategy }} {{ controllerWeight(controllerAcceptance.eligible_fixture.fallback_before_basis) }} → {{ controllerWeight(controllerAcceptance.eligible_fixture.fallback_proposed_basis) }} · Applied=false</small>
                    </div>
                  </div>
                </article>
                <div v-if="controllerAudit.latest.length" class="controller-decision-list">
                  <article v-for="decision in controllerAudit.latest" :key="decision.recommendation_id">
                    <div>
                      <strong>{{ decision.simulation ? '验收审计' : '生产审计' }} · {{ decision.strategy }}</strong>
                      <span :class="decision.status === 'recommended' ? 'dependency-ready' : 'dependency-down'">{{ controllerStatusLabel(decision.status) }}</span>
                    </div>
                    <span>{{ controllerReasonLabel(decision.reason_code) }} · Parent {{ decision.parent_policy_version }} · Applied={{ decision.applied }}</span>
                    <small>Evidence SHA {{ decision.evidence_sha256.slice(0, 12) }}…<template v-if="decision.candidate_policy_sha256"> · Candidate SHA {{ decision.candidate_policy_sha256.slice(0, 12) }}…</template></small>
                  </article>
                </div>
                <div v-else class="strategy-control-empty">尚无控制决策；只有成熟异常窗口才会进入生产控制周期。</div>
                <div class="evaluation-decision-strip">
                  <span v-for="guardrail in controllerAudit.guardrails" :key="guardrail" class="dependency-ready">{{ guardrail }}</span>
                </div>
              </details>
              <details v-if="faultCampaignAudit" class="fault-campaign-card">
                <summary>
                  查看三类 Observe-only 故障演练（{{ faultCampaignAudit.latest ? `${faultCampaignAudit.latest.summary.detected_count}/3 已检测` : '尚未运行' }}）
                </summary>
                <div class="metric-catalog-heading">
                  <div>
                    <strong>Isolated Fault Campaign · {{ faultCampaignAudit.fixture_version }}</strong>
                    <span>{{ faultCampaignAudit.environment }} · {{ faultCampaignAudit.mode }} · 不影响线上流量</span>
                  </div>
                  <button :disabled="runningFaultCampaign" @click="runFaultCampaignAcceptance">
                    {{ runningFaultCampaign ? '正在演练三类故障...' : '运行隔离故障演练' }}
                  </button>
                </div>
                <p>只在隔离适配器中注入 RAG 退化、Agent 延迟和工具超时；不会停止 Redis/RabbitMQ，不会调用生产故障命令，也不会修改活动策略。</p>
                <article v-if="activeFaultCampaign" class="fault-campaign-result">
                  <div class="fault-campaign-summary">
                    <div><strong>{{ activeFaultCampaign.summary.detected_count }}/{{ activeFaultCampaign.summary.scenario_count }}</strong><span>故障检测</span></div>
                    <div><strong>{{ activeFaultCampaign.summary.recovered_count }}/{{ activeFaultCampaign.summary.scenario_count }}</strong><span>恢复识别</span></div>
                    <div><strong>{{ activeFaultCampaign.summary.mean_mttd_seconds }}s</strong><span>平均 MTTD（逻辑窗口）</span></div>
                    <div><strong>{{ activeFaultCampaign.summary.false_positives }}/{{ activeFaultCampaign.summary.false_positive_checks }}</strong><span>健康对照误报</span></div>
                    <div><strong>{{ activeFaultCampaign.summary.recommendation_count }}</strong><span>只建议候选</span></div>
                    <div><strong>{{ activeFaultCampaign.summary.applied_count }}</strong><span>实际策略变更</span></div>
                  </div>
                  <div class="fault-scenario-list">
                    <article v-for="scenario in activeFaultCampaign.scenarios" :key="scenario.scenario_id">
                      <div class="fault-scenario-heading">
                        <div><strong>{{ scenario.name }}</strong><span>{{ faultClassLabel(scenario.fault_class) }} · {{ scenario.injected_outcome }}</span></div>
                        <span class="dependency-ready">检测并恢复</span>
                      </div>
                      <div class="fault-scenario-metrics">
                        <span>Fixed {{ scenario.fixed_threshold_detected ? '命中' : '未命中' }}</span>
                        <span>Z-score {{ scenario.z_score_detected ? '命中' : '未命中' }}</span>
                        <span>MTTD {{ scenario.mttd_seconds }}s</span>
                        <span>恢复 {{ scenario.recovery_seconds }}s</span>
                        <span>Evidence {{ scenario.evidence_sha256.slice(0, 12) }}…</span>
                      </div>
                      <div class="fault-timeline" aria-label="故障闭环时间轴">
                        <div v-for="point in scenario.timeline" :key="`${scenario.scenario_id}-${point.phase}`" :class="`phase-${point.phase}`">
                          <strong>{{ faultPhaseLabel(point.phase) }}</strong>
                          <span>T+{{ point.offset_seconds }}s · {{ faultMetricValue(scenario.metric, point.metric_value) }}</span>
                          <span>质量 {{ metricPercent(point.indicators.quality_rate) }} · 成功 {{ metricPercent(point.indicators.success_rate) }}</span>
                          <span>P95/P99 {{ point.indicators.p95_latency_ms }}/{{ point.indicators.p99_latency_ms }}ms · n={{ point.indicators.population }}</span>
                          <small v-if="point.recommendation_action !== 'none'">{{ anomalyRecommendationLabel(point.recommendation_action) }} · {{ point.weight_delta_basis / 100 }}% · Applied={{ point.applied }}</small>
                        </div>
                      </div>
                    </article>
                  </div>
                  <div class="fault-campaign-limit">
                    <strong>缓解成功率：未测量</strong>
                    <span>本轮为 Observe-only，故意不执行降权；报告不会用“建议已生成”冒充“线上缓解成功”。</span>
                    <small>Report SHA-256 {{ activeFaultCampaign.report_sha256 }}</small>
                  </div>
                </article>
                <div v-else class="strategy-control-empty">尚无演练报告；点击按钮后会生成可重放、带 Hash 的不可变隔离报告。</div>
                <div class="evaluation-decision-strip">
                  <span v-for="guardrail in faultCampaignAudit.guardrails" :key="guardrail" class="dependency-ready">{{ guardrail }}</span>
                </div>
              </details>
              <details class="reliability-acceptance-card">
                <summary>
                  查看 Agent 恢复与 SSE 取消验收（{{ reliabilityAcceptance ? (reliabilityAcceptance.passed ? '2/2 通过' : '未通过') : '尚未运行' }}）
                </summary>
                <div class="metric-catalog-heading">
                  <div>
                    <strong>Agent Reliability Fault Injection</strong>
                    <span>隔离进程重启语义 + 20 路 SSE 断连 · 复用生产 Harness/AppService</span>
                  </div>
                  <button :disabled="runningReliabilityAcceptance" @click="runReliabilityAcceptance">
                    {{ runningReliabilityAcceptance ? '正在验证恢复与取消...' : '运行可靠性验收' }}
                  </button>
                </div>
                <p>不会重启生产服务，不调用外部模型或工具；只在隔离仓库和阻塞 Strategy 中验证 Checkpoint/CAS 幂等恢复与 Context 取消传播。</p>
                <template v-if="reliabilityAcceptance">
                  <div class="reliability-result-grid">
                    <article :class="reliabilityAcceptance.agent_recovery.passed ? 'passed' : 'failed'">
                      <div class="evaluation-run-heading">
                        <strong>Checkpoint 进程恢复</strong>
                        <span>{{ metricPercent(reliabilityAcceptance.agent_recovery.recovery_rate) }} 恢复率</span>
                      </div>
                      <div class="diagnostic-evaluation-grid">
                        <div><strong>{{ reliabilityAcceptance.agent_recovery.checkpoint_recovered ? '是' : '否' }}</strong><span>Checkpoint 已恢复</span></div>
                        <div><strong>{{ reliabilityAcceptance.agent_recovery.duplicate_resume_executions }}</strong><span>重复 Resume 执行</span></div>
                        <div><strong>{{ reliabilityAcceptance.agent_recovery.final_state }}</strong><span>最终状态</span></div>
                        <div><strong>{{ reliabilityAcceptance.agent_recovery.checkpoint_version_before }} → {{ reliabilityAcceptance.agent_recovery.final_state_version }}</strong><span>状态版本</span></div>
                      </div>
                      <small>{{ reliabilityAcceptance.agent_recovery.injected_fault }} · 状态版本单调={{ reliabilityAcceptance.agent_recovery.state_versions_monotonic }}</small>
                    </article>
                    <article :class="reliabilityAcceptance.sse_cancellation.passed ? 'passed' : 'failed'">
                      <div class="evaluation-run-heading">
                        <strong>SSE 断连取消传播</strong>
                        <span>{{ reliabilityAcceptance.sse_cancellation.cancellation_observed }}/{{ reliabilityAcceptance.sse_cancellation.streams }} 已取消</span>
                      </div>
                      <div class="diagnostic-evaluation-grid">
                        <div><strong>{{ reliabilityAcceptance.sse_cancellation.p50_propagation_ms.toFixed(3) }}ms</strong><span>取消 P50</span></div>
                        <div><strong>{{ reliabilityAcceptance.sse_cancellation.p95_propagation_ms.toFixed(3) }}ms</strong><span>取消 P95</span></div>
                        <div><strong>{{ reliabilityAcceptance.sse_cancellation.p99_propagation_ms.toFixed(3) }}ms</strong><span>取消 P99</span></div>
                        <div><strong>{{ reliabilityAcceptance.sse_cancellation.active_workers_after }}</strong><span>结束后活跃 Worker</span></div>
                      </div>
                      <small>预算 {{ reliabilityAcceptance.sse_cancellation.propagation_budget_ms }}ms · Peak {{ reliabilityAcceptance.sse_cancellation.peak_active_workers }} · 重复 final {{ reliabilityAcceptance.sse_cancellation.duplicate_final_events }} · 资源收敛={{ reliabilityAcceptance.sse_cancellation.resource_converged }}</small>
                    </article>
                  </div>
                  <div class="evaluation-decision-strip">
                    <span v-for="guardrail in reliabilityAcceptance.guardrails" :key="guardrail" class="dependency-ready">{{ reliabilityGuardrailLabel(guardrail) }}</span>
                  </div>
                  <small>Report SHA-256 {{ reliabilityAcceptance.report_sha256 }}</small>
                </template>
                <div v-else class="strategy-control-empty">尚无已落盘报告；运行后会保存带 SHA 校验的不可变证据，但不会污染生产指标或策略。</div>
              </details>
              <details class="reliability-acceptance-card performance-acceptance-card">
                <summary>
                  查看 ECS 冷热路径与 pprof 证据（{{ performanceReport ? (performanceReport.gates.technical_passed ? '技术门通过' : '存在失败项') : '尚未生成' }}）
                </summary>
                <div class="metric-catalog-heading">
                  <div>
                    <strong>Bounded Loopback Performance · {{ performanceReport?.runner_version || 'bounded-loopback-perf-v1' }}</strong>
                    <span>固定回环地址 · 最多 50 请求 / 5 并发 · CPU、Heap、Goroutine 三类证据</span>
                  </div>
                  <button :disabled="loadingPerformanceReport" @click="refreshPerformanceReport">
                    {{ loadingPerformanceReport ? '读取中...' : '刷新性能报告' }}
                  </button>
                </div>
                <p>报告只能由容器内命令显式生成；浏览器不会触发压测。冷路径是“本次发布后的首个受控请求”，热路径会先执行 1 次不计入指标的预热。</p>
                <template v-if="performanceReport">
                  <div class="evaluation-run-heading">
                    <strong>{{ performanceReport.release.id }}</strong>
                    <span :class="['evaluation-gate', performanceReport.gates.technical_passed ? 'passed' : 'failed']">
                      {{ performanceReport.gates.technical_passed ? '技术门通过' : performanceReport.gates.failures.join(' · ') }}
                    </span>
                  </div>
                  <div class="performance-phase-grid">
                    <article>
                      <div class="evaluation-run-heading"><strong>发布首请求（Cold）</strong><span>{{ performanceReport.cold.successes }}/{{ performanceReport.cold.requests }} 成功</span></div>
                      <div class="diagnostic-evaluation-grid">
                        <div><strong>{{ performanceReport.cold.total_latency.p50_ms.toFixed(1) }}ms</strong><span>端到端 P50</span></div>
                        <div><strong>{{ performanceReport.cold.total_latency.p95_ms.toFixed(1) }}ms</strong><span>端到端 P95</span></div>
                        <div><strong>{{ performanceReport.cold.ttft.p50_ms.toFixed(1) }}ms</strong><span>TTFT P50</span></div>
                        <div><strong>{{ performanceReport.cold.errors }}</strong><span>错误数</span></div>
                      </div>
                      <div class="performance-route-strip">
                        <span v-for="route in performanceReport.cold.observed_routes" :key="`${route.strategy}-${route.strategy_version}`">路由 {{ route.strategy }} · {{ route.strategy_version }} · {{ route.policy_version }}</span>
                        <span v-for="model in performanceReport.cold.observed_model_aliases" :key="model">模型 {{ model }}</span>
                        <span>阶段耗时 {{ performanceReport.cold.duration_ms.toFixed(1) }}ms</span>
                      </div>
                      <small>{{ performanceReport.cold.definition }}</small>
                    </article>
                    <article>
                      <div class="evaluation-run-heading"><strong>预热路径（Hot）</strong><span>{{ performanceReport.hot.requests }} 请求 · 并发 {{ performanceReport.hot.concurrency }}</span></div>
                      <div class="diagnostic-evaluation-grid">
                        <div><strong>{{ performanceReport.hot.total_latency.p50_ms.toFixed(1) }}ms</strong><span>端到端 P50</span></div>
                        <div><strong>{{ performanceReport.hot.total_latency.p95_ms.toFixed(1) }}ms</strong><span>端到端 P95</span></div>
                        <div><strong>{{ performanceReport.hot.total_latency.p99_ms.toFixed(1) }}ms</strong><span>端到端 P99</span></div>
                        <div><strong>{{ performanceReport.hot.ttft.p95_ms.toFixed(1) }}ms</strong><span>TTFT P95</span></div>
                        <div><strong>{{ performanceReport.hot.model_calls_per_100_successes.toFixed(1) }}</strong><span>模型调用 / 100 成功</span></div>
                        <div><strong>{{ performanceReport.hot.estimated_tokens_per_100_successes.toFixed(0) }}</strong><span>估算 Token / 100 成功</span></div>
                      </div>
                      <div class="performance-route-strip">
                        <span v-for="route in performanceReport.hot.observed_routes" :key="`${route.strategy}-${route.strategy_version}`">路由 {{ route.strategy }} · {{ route.strategy_version }} · {{ route.policy_version }}</span>
                        <span v-for="model in performanceReport.hot.observed_model_aliases" :key="model">模型 {{ model }}</span>
                        <span>阶段耗时 {{ performanceReport.hot.duration_ms.toFixed(1) }}ms</span>
                      </div>
                      <small>{{ performanceReport.hot.definition }}</small>
                    </article>
                  </div>
                  <div class="performance-runtime-strip">
                    <span>{{ performanceReport.runtime.cpu_cores }} vCPU</span>
                    <span>{{ (performanceReport.runtime.memory_total_bytes / 1073741824).toFixed(2) }} GiB RAM</span>
                    <span>进程运行 {{ performanceReport.runtime.process_uptime_seconds.toFixed(0) }}s</span>
                    <span>{{ performanceReport.runtime.go_version }}</span>
                  </div>
                  <div class="evaluation-decision-strip">
                    <span v-for="artifact in performanceReport.profiles" :key="artifact.kind" class="dependency-ready">
                      {{ artifact.kind }} · {{ artifact.bytes }} B · SHA {{ artifact.sha256.slice(0, 12) }}…
                    </span>
                  </div>
                  <details class="strategy-registry-details">
                    <summary>查看口径限制与安全边界</summary>
                    <div class="performance-limitations">
                      <span v-for="item in performanceReport.limitations" :key="item">{{ item }}</span>
                      <small>Report SHA-256 {{ performanceReport.report_sha256 }}</small>
                    </div>
                  </details>
                </template>
                <div v-else class="strategy-control-empty">部署完成后由受限 CLI 在 ECS 容器内生成，不存储登录凭据、问题或回答。</div>
              </details>
              <details v-if="onlineEvaluationAudit" class="online-evaluation-card">
                <summary>
                  查看线上分层采样与异步 Judge（24h {{ onlineEvaluationAudit.last_24_hours.total }} 个生产样本）
                </summary>
                <div class="metric-catalog-heading">
                  <div>
                    <strong>Risk-stratified Online Evaluation · {{ onlineEvaluationAudit.sampler_version }}</strong>
                    <span>{{ onlineEvaluationAudit.mode }} · {{ onlineEvaluationAudit.queue }}</span>
                  </div>
                  <div class="metric-catalog-actions">
                    <button :disabled="loadingOnlineEvaluationAudit" @click="refreshOnlineEvaluationAudit">
                      {{ loadingOnlineEvaluationAudit ? '刷新中...' : '刷新生产样本' }}
                    </button>
                    <button :disabled="runningOnlineEvaluationAcceptance" @click="runOnlineEvaluationAcceptance">
                      {{ runningOnlineEvaluationAcceptance ? '等待 RabbitMQ 消费...' : '运行采样 / 队列验收' }}
                    </button>
                  </div>
                </div>
                <p>稳定流量按请求哈希固定采 4%，Canary 20%，Probing 50%；点踩、低置信度、证据门禁失败、工具失败等风险样本 100%。采样、脱敏和 Judge 均不阻塞正式回答。</p>
                <div class="online-evaluation-rates">
                  <div><strong>4%</strong><span>稳定流量</span></div>
                  <div><strong>20%</strong><span>Canary</span></div>
                  <div><strong>50%</strong><span>Probing</span></div>
                  <div><strong>100%</strong><span>风险样本</span></div>
                  <div><strong>{{ onlineEvaluationAudit.retention_days }} 天</strong><span>脱敏样本保留</span></div>
                </div>
                <div class="evaluation-decision-strip">
                  <span v-for="guarantee in onlineEvaluationAudit.privacy_guarantees" :key="guarantee" class="dependency-ready">{{ guarantee }}</span>
                </div>
                <article v-if="onlineEvaluationAudit.latest" class="online-evaluation-latest">
                  <div class="evaluation-run-heading">
                    <div>
                      <strong>最近生产样本 · {{ onlineEvaluationAudit.latest.strategy }}</strong>
                      <span>{{ onlineEvaluationAudit.latest.traffic_class }} · {{ onlineEvaluationStatusLabel(onlineEvaluationAudit.latest.status) }}</span>
                    </div>
                    <span>{{ onlineEvaluationAudit.latest.sample_rate_basis / 100 }}% · 脱敏 {{ onlineEvaluationAudit.latest.redaction_count }} 处</span>
                  </div>
                  <div v-if="onlineEvaluationAudit.latest.status === 'completed'" class="online-score-grid">
                    <span>相关性 {{ metricPercent(onlineEvaluationAudit.latest.relevance) }}</span>
                    <span>完整性 {{ metricPercent(onlineEvaluationAudit.latest.completeness) }}</span>
                    <span>有用性 {{ metricPercent(onlineEvaluationAudit.latest.helpfulness) }}</span>
                    <span>有依据 {{ metricPercent(onlineEvaluationAudit.latest.groundedness) }}</span>
                    <span>安全性 {{ metricPercent(onlineEvaluationAudit.latest.safety) }}</span>
                  </div>
                  <small>页面不返回原始问题、回答、证据正文或用户标识。</small>
                </article>
                <div v-else class="strategy-control-empty">最近 24 小时尚无生产样本；4% 稳定采样不会为演示而伪造流量。</div>
                <article v-if="onlineEvaluationAcceptance" :class="['online-evaluation-acceptance', onlineEvaluationAcceptance.passed ? 'passed' : 'failed']">
                  <div class="evaluation-run-heading">
                    <div>
                      <strong>真实异步链路验收 · {{ onlineEvaluationAcceptance.passed ? '通过' : '未完成' }}</strong>
                      <span>Simulation · 不调用模型 · 不写生产评分指标</span>
                    </div>
                    <span>{{ onlineEvaluationStatusLabel(onlineEvaluationAcceptance.final_status) }} · 脱敏 {{ onlineEvaluationAcceptance.redaction_count }} 处</span>
                  </div>
                  <div class="online-stage-list">
                    <span v-for="stage in onlineEvaluationAcceptance.stages" :key="stage.name" :class="stage.status === 'completed' ? 'dependency-ready' : 'dependency-down'">{{ onlineEvaluationStageLabel(stage.name) }} · {{ stage.status === 'completed' ? '完成' : '等待' }}</span>
                  </div>
                  <div class="online-case-grid">
                    <article v-for="item in onlineEvaluationAcceptance.cases" :key="item.name">
                      <strong>{{ item.name }}</strong>
                      <span>{{ item.traffic_class }} · {{ item.sample_rate_basis / 100 }}% · {{ item.forced ? '强制采样' : '哈希采样' }}</span>
                      <small>{{ item.reasons.join('、') }}</small>
                    </article>
                  </div>
                  <div class="evaluation-decision-strip">
                    <span :class="!onlineEvaluationAcceptance.raw_identity_persisted ? 'dependency-ready' : 'dependency-down'">原始用户标识未落库</span>
                    <span :class="!onlineEvaluationAcceptance.production_metrics_used ? 'dependency-ready' : 'dependency-down'">验收数据未污染生产指标</span>
                  </div>
                </article>
                <details v-if="failurePoolAudit" class="failure-pool-card">
                  <summary>
                    查看失败样本慢闭环（{{ failurePoolAudit.run?.eligible_count || 0 }} 个失败样本 · {{ failurePoolAudit.run?.cluster_count || 0 }} 个 WHERE×WHY 聚类）
                  </summary>
                  <div class="metric-catalog-heading">
                    <div>
                      <strong>Failure Pool · {{ failurePoolAudit.miner_version }}</strong>
                      <span>{{ failurePoolAudit.mode }} · 最近 {{ failurePoolAudit.window_days }} 天 · 只读脱敏元数据</span>
                    </div>
                    <div class="metric-catalog-actions">
                      <button :disabled="refreshingFailurePool" @click="refreshFailurePool">
                        {{ refreshingFailurePool ? '聚类中...' : '刷新生产失败池' }}
                      </button>
                      <button :disabled="runningFailurePoolAcceptance" @click="runFailurePoolAcceptance">
                        {{ runningFailurePoolAcceptance ? '验证中...' : '验证 8 类失败映射' }}
                      </button>
                    </div>
                  </div>
                  <p>聚类只使用 intent、strategy、错误类别、风险原因和 Judge 分数，不读取原始问题、回答或证据。每个聚类只生成一个不可执行候选。</p>
                  <div v-if="failurePoolAudit.run" class="online-evaluation-rates">
                    <div><strong>{{ failurePoolAudit.run.sample_count }}</strong><span>窗口样本</span></div>
                    <div><strong>{{ failurePoolAudit.run.eligible_count }}</strong><span>失败样本</span></div>
                    <div><strong>{{ failurePoolAudit.run.cluster_count }}</strong><span>WHERE×WHY 聚类</span></div>
                    <div><strong>{{ failurePoolAudit.run.proposal_count }}</strong><span>待复核候选</span></div>
                    <div><strong>0</strong><span>已应用</span></div>
                  </div>
                  <div v-else class="strategy-control-empty">尚无生产聚类快照；点击“刷新生产失败池”只会创建不可变审计，不会改变线上行为。</div>
                  <div v-if="failurePoolAudit.clusters?.length" class="failure-cluster-grid">
                    <article v-for="cluster in failurePoolAudit.clusters" :key="cluster.id">
                      <div class="evaluation-run-heading">
                        <strong>{{ failureWhereLabel(cluster.where_code) }} × {{ failureWhyLabel(cluster.why_code) }}</strong>
                        <span>{{ cluster.sample_count }} 个脱敏样本</span>
                      </div>
                      <p>{{ cluster.intent }} · {{ cluster.strategy }} · {{ cluster.primary_reason }}</p>
                      <template v-if="failureProposalFor(cluster.id)">
                        <div class="failure-proposal-line">
                          <span>{{ failureCandidateLabel(failureProposalFor(cluster.id).candidate_kind) }}</span>
                          <strong>{{ failureTargetLabel(failureProposalFor(cluster.id).target) }}</strong>
                        </div>
                        <small>pending_human_review · Offline Gate 未通过 · Isolation Canary 未通过 · Applied=false</small>
                      </template>
                    </article>
                  </div>
                  <article v-if="failurePoolAcceptance" :class="['online-evaluation-acceptance', failurePoolAcceptance.passed ? 'passed' : 'failed']">
                    <div class="evaluation-run-heading">
                      <strong>确定性聚类验收 · {{ failurePoolAcceptance.passed ? '8/8 通过' : '未通过' }}</strong>
                      <span>Simulation · 不写数据库 · 不读原始正文</span>
                    </div>
                    <div class="failure-acceptance-grid">
                      <span v-for="item in failurePoolAcceptance.cases" :key="item.reason" :class="item.passed ? 'dependency-ready' : 'dependency-down'">
                        {{ item.reason }} → {{ failureWhereLabel(item.where_code) }} × {{ failureWhyLabel(item.why_code) }} → {{ failureCandidateLabel(item.candidate_kind) }}
                      </span>
                    </div>
                  </article>
                  <div class="evaluation-decision-strip">
                    <span v-for="guardrail in failurePoolAudit.guardrails" :key="guardrail" class="dependency-ready">{{ failureGuardrailLabel(guardrail) }}</span>
                  </div>
                </details>
                <details v-if="evolutionAudit" class="failure-pool-card evolution-lineage-card">
                  <summary>
                    查看 Harness Evolution 候选谱系（{{ evolutionAudit.artifact_count || 0 }} 条候选 · {{ evolutionAudit.applied_count || 0 }} 条已应用）
                  </summary>
                  <div class="metric-catalog-heading">
                    <div>
                      <strong>Harness Evolution · {{ evolutionAudit.schema_version }}</strong>
                      <span>{{ evolutionAudit.mode }} · 白名单单变量 Patch · 不持有活动指针</span>
                    </div>
                    <div class="metric-catalog-actions">
                      <button :disabled="materializingEvolution" @click="materializeEvolutionCandidates">
                        {{ materializingEvolution ? '生成并校验中...' : '从当前失败池生成离线候选' }}
                      </button>
                    </div>
                  </div>
                  <p>这里只把脱敏失败簇转换成不可执行的 Prompt、Context Policy 或诊断 Playbook 候选；生成候选不等于质量提升，也不会改变线上聊天。</p>
                  <div class="online-evaluation-rates">
                    <div><strong>{{ evolutionAudit.artifact_count || 0 }}</strong><span>不可变候选</span></div>
                    <div><strong>{{ evolutionAudit.review_count || 0 }}</strong><span>追加式复核</span></div>
                    <div><strong>{{ evolutionAudit.approved_count || 0 }}</strong><span>人工批准</span></div>
                    <div><strong>{{ evolutionAudit.active_pointers || 0 }}</strong><span>活动指针</span></div>
                    <div><strong>{{ evolutionAudit.applied_count || 0 }}</strong><span>已应用</span></div>
                  </div>
                  <div v-if="evolutionMaterialization" class="evaluation-decision-strip">
                    <span class="dependency-ready">来源提案 {{ evolutionMaterialization.proposal_count }}</span>
                    <span class="dependency-ready">新建 {{ evolutionMaterialization.created_count }}</span>
                    <span class="dependency-ready">幂等复用 {{ evolutionMaterialization.existing_count }}</span>
                    <span>跳过 {{ evolutionMaterialization.skipped?.length || 0 }}</span>
                  </div>
                  <div v-if="evolutionAudit.latest?.length" class="failure-cluster-grid evolution-artifact-grid">
                    <article v-for="artifact in evolutionAudit.latest" :key="artifact.id">
                      <div class="evaluation-run-heading">
                        <strong>{{ evolutionArtifactLabel(artifact.artifact_type) }}</strong>
                        <span>{{ artifact.status }}</span>
                      </div>
                      <div class="failure-proposal-line">
                        <span>单变量 {{ artifact.patch.operation }}</span>
                        <strong>{{ artifact.patch.path }}</strong>
                      </div>
                      <p>Parent：{{ artifact.parent_version }} · {{ shortRevision(artifact.parent_sha256) }}</p>
                      <p>Candidate：{{ artifact.artifact_version }} · {{ shortRevision(artifact.artifact_sha256) }}</p>
                      <small>数据 {{ artifact.data_split }} · {{ shortRevision(artifact.data_version) }} · 预算：模型 {{ artifact.budget.model_calls }} 次 / Token {{ artifact.budget.token_budget }} / 搜索 {{ artifact.budget.search_iterations }} 轮</small>
                      <div class="evaluation-decision-strip">
                        <span :class="artifact.static_validation_passed ? 'dependency-ready' : 'dependency-down'">静态校验 {{ artifact.static_validation_passed ? '通过' : '失败' }}</span>
                        <span :class="artifact.requires_human_approval ? 'dependency-ready' : 'dependency-down'">需要人工批准</span>
                        <span :class="!artifact.offline_evaluation_passed ? 'dependency-ready' : 'dependency-down'">离线 A/B 未运行</span>
                        <span :class="!artifact.holdout_opened ? 'dependency-ready' : 'dependency-down'">Holdout 未打开</span>
                        <span :class="!artifact.applied ? 'dependency-ready' : 'dependency-down'">Applied=false</span>
                      </div>
                    </article>
                  </div>
                  <div v-else class="strategy-control-empty">尚无 Harness 候选。先刷新失败池，再点击生成；数据集类提案会被明确跳过，不会伪装成 Harness Patch。</div>
                  <div class="evaluation-decision-strip">
                    <span v-for="guardrail in evolutionAudit.guardrails" :key="guardrail" class="dependency-ready">{{ evolutionGuardrailLabel(guardrail) }}</span>
                  </div>
                  <small v-for="limitation in evolutionAudit.limitations" :key="limitation" class="evolution-limitation">{{ limitation }}</small>
                  <details v-if="evolutionSplitAudit" class="review-fixture-list evolution-split-card">
                    <summary>查看 Evolution / Validation / Sealed Holdout 分区（{{ evolutionSplitAudit.covered_cases }}/{{ evolutionSplitAudit.total_cases }}）</summary>
                    <div class="metric-catalog-heading">
                      <div>
                        <strong>冻结分区 · {{ evolutionSplitAudit.policy_version }}</strong>
                        <span>{{ evolutionSplitAudit.dataset_version }} · Source {{ shortRevision(evolutionSplitAudit.source_sha256) }}</span>
                      </div>
                      <button :disabled="runningEvolutionSplitAcceptance" @click="runEvolutionSplitAcceptance">
                        {{ runningEvolutionSplitAcceptance ? '验收中...' : '运行 8 项防泄漏验收' }}
                      </button>
                    </div>
                    <div class="failure-cluster-grid evolution-split-grid">
                      <article v-for="split in evolutionSplitAudit.splits" :key="split.name">
                        <div class="evaluation-run-heading">
                          <strong>{{ evolutionSplitLabel(split.name) }}</strong>
                          <span>{{ split.seal_state }}</span>
                        </div>
                        <p>{{ split.case_count }} 条 · Set SHA {{ shortRevision(split.case_set_sha256) }}</p>
                        <small>{{ evolutionSplitPurposeLabel(split.purpose) }}</small>
                        <div class="evaluation-decision-strip">
                          <span :class="split.candidate_search_readable ? 'dependency-ready' : ''">候选搜索{{ split.candidate_search_readable ? '可读' : '不可读' }}</span>
                          <span :class="!split.case_ids_exposed ? 'dependency-ready' : 'dependency-down'">Case ID 不对外暴露</span>
                        </div>
                      </article>
                    </div>
                    <div class="online-evaluation-rates">
                      <div><strong>{{ evolutionSplitAudit.overlap_count }}</strong><span>跨分区重叠</span></div>
                      <div><strong>{{ evolutionSplitAudit.duplicate_id_count }}</strong><span>重复 Case ID</span></div>
                      <div><strong>{{ evolutionSplitAudit.holdout_open_count }}</strong><span>Holdout 打开次数</span></div>
                      <div><strong>{{ evolutionSplitAudit.holdout_open_api_available ? '是' : '否' }}</strong><span>当前开放 Holdout API</span></div>
                    </div>
                    <article v-if="evolutionSplitAcceptance" :class="['online-evaluation-acceptance', evolutionSplitAcceptance.passed ? 'passed' : 'failed']">
                      <div class="evaluation-run-heading">
                        <strong>防泄漏验收 · {{ evolutionSplitAcceptance.passed ? `${evolutionSplitAcceptance.cases.length}/${evolutionSplitAcceptance.cases.length} 通过` : '未通过' }}</strong>
                        <span>确定性 · 无写入 · 不打开 Holdout</span>
                      </div>
                      <div class="failure-acceptance-grid">
                        <span v-for="item in evolutionSplitAcceptance.cases" :key="item.reason_code" :class="item.passed ? 'dependency-ready' : 'dependency-down'">{{ evolutionSplitAcceptanceLabel(item.reason_code) }}</span>
                      </div>
                    </article>
                    <div class="evaluation-decision-strip">
                      <span class="dependency-ready">Source Hash 已核验</span>
                      <span class="dependency-ready">覆盖 {{ evolutionSplitAudit.covered_cases }}/{{ evolutionSplitAudit.total_cases }}</span>
                      <span class="dependency-ready">重开 Holdout 必须新实验版本</span>
                    </div>
                  </details>
                  <details class="review-fixture-list evolution-split-card evolution-comparison-card">
                    <summary>查看公平预算 Harness A/B（{{ evolutionComparisonReport ? evolutionPromotionLabel(evolutionComparisonReport.promotion.decision) : '尚未运行' }}）</summary>
                    <div class="metric-catalog-heading">
                      <div>
                        <strong>四方成对比较 · 旧 Harness / 人工规则 / 同预算 TTS / 自动候选</strong>
                        <span>确定性诊断契约 · 同 Case、同执行次数、同模型/Token 预算</span>
                      </div>
                      <button :disabled="runningEvolutionComparison" @click="runEvolutionComparison">
                        {{ runningEvolutionComparison ? '比较中...' : '运行受控离线公平 A/B' }}
                      </button>
                    </div>
                    <div v-if="evolutionComparisonReport">
                      <div class="evaluation-decision-strip">
                        <span>实验 {{ evolutionComparisonReport.experiment_version }}</span>
                        <span>报告 {{ shortRevision(evolutionComparisonReport.report_sha256) }}</span>
                        <span :class="evolutionComparisonReport.candidate.production_candidate ? 'dependency-ready' : ''">受控 Fixture 候选</span>
                        <span :class="evolutionComparisonReport.promotion.eligible ? 'dependency-ready' : 'dependency-down'">Promotion {{ evolutionComparisonReport.promotion.eligible ? '允许' : '拒绝' }}</span>
                      </div>
                      <article class="evolution-candidate-summary">
                        <div class="evaluation-run-heading">
                          <strong>{{ evolutionArtifactLabel(evolutionComparisonReport.candidate.artifact_type) }} · {{ evolutionComparisonReport.candidate.artifact_version }}</strong>
                          <span>{{ evolutionComparisonReport.candidate.origin }}</span>
                        </div>
                        <p>Parent {{ evolutionComparisonReport.candidate.parent_version }} → {{ evolutionComparisonReport.candidate.patch.path }} = {{ evolutionComparisonReport.candidate.patch.value }}</p>
                        <small>Static Validation 通过 · 需要人工批准 · Production Candidate=false</small>
                      </article>
                      <article v-for="split in evolutionComparisonReport.splits" :key="split.split" class="evolution-comparison-split">
                        <div class="evaluation-run-heading">
                          <strong>{{ evolutionSplitLabel(split.split) }} · {{ split.case_count }} 对</strong>
                          <span>Set SHA {{ shortRevision(split.case_set_sha256) }}</span>
                        </div>
                        <div class="evolution-variant-grid">
                          <div v-for="variant in split.variants" :key="variant.name">
                            <strong>{{ evolutionVariantLabel(variant.name) }}</strong>
                            <span>均分 {{ metricPercent(variant.mean_score) }} · 成功 {{ variant.successes }}/{{ variant.case_count }}</span>
                            <small>执行 {{ variant.budget.analysis_passes_per_case }} 次/Case · 模型 {{ variant.budget.model_calls }} · Token {{ variant.budget.estimated_tokens }} · 反馈轮 {{ variant.budget.feedback_iterations }}</small>
                          </div>
                        </div>
                        <div class="evolution-paired-list">
                          <div v-for="comparison in split.comparisons" :key="comparison.candidate_variant">
                            <span>{{ evolutionVariantLabel(comparison.candidate_variant) }} vs 旧 Harness</span>
                            <strong>Δ {{ signedPercent(comparison.analysis.mean_delta) }} · 95% CI [{{ signedPercent(comparison.analysis.delta_ci95_lower) }}, {{ signedPercent(comparison.analysis.delta_ci95_upper) }}] · p={{ Number(comparison.analysis.mcnemar_exact_two_sided_p_value).toFixed(4) }}</strong>
                            <small>{{ pairedConclusionLabel(comparison.analysis.conclusion) }}</small>
                          </div>
                        </div>
                      </article>
                      <article class="evaluation-candidate-warning">
                        <strong>Sealed Holdout：{{ evolutionComparisonReport.holdout.state }} · 打开 {{ evolutionComparisonReport.holdout.open_count }} 次</strong>
                        <span>{{ evolutionHoldoutReasonLabel(evolutionComparisonReport.holdout.reason_code) }}；不会为了完成报告而消耗最终集。</span>
                      </article>
                      <div class="evaluation-decision-strip">
                        <span v-for="reason in evolutionComparisonReport.promotion.reason_codes" :key="reason" class="dependency-down">{{ evolutionPromotionReasonLabel(reason) }}</span>
                        <span :class="evolutionComparisonReport.promotion.safety_passed ? 'dependency-ready' : 'dependency-down'">危险动作回归 {{ evolutionComparisonReport.promotion.safety_passed ? '0' : '存在' }}</span>
                      </div>
                      <small v-for="limitation in evolutionComparisonReport.limitations" :key="limitation" class="evolution-limitation">{{ limitation }}</small>
                      <article v-if="evolutionPromotionAudit" class="evolution-promotion-gate">
                        <div class="metric-catalog-heading">
                          <div>
                            <strong>人工 Promotion Gate · {{ evolutionPromotionAudit.mode }}</strong>
                            <span>独立 reviewer 权限 · 报告 Hash 绑定 · 追加式幂等审计</span>
                          </div>
                          <div class="evolution-promotion-actions">
                            <button :disabled="submittingPromotionReview || !evolutionPromotionAudit.can_review" @click="submitPromotionReview('rejected')">记录人工拒绝</button>
                            <button :disabled="submittingPromotionReview || !evolutionPromotionAudit.can_review" @click="submitPromotionReview('approved')">尝试批准（门禁应拒绝）</button>
                          </div>
                        </div>
                        <div class="diagnostic-evaluation-grid">
                          <div><strong>{{ evolutionPromotionAudit.attempt_count }}</strong><span>评审尝试</span></div>
                          <div><strong>{{ evolutionPromotionAudit.recorded_count }}</strong><span>有效决策</span></div>
                          <div><strong>{{ evolutionPromotionAudit.blocked_count }}</strong><span>门禁阻断</span></div>
                          <div><strong>{{ evolutionPromotionAudit.active_pointers }}</strong><span>活动指针</span></div>
                        </div>
                        <div v-if="!evolutionPromotionAudit.can_review" class="evaluation-candidate-warning">
                          <strong>当前账号只有查看权限</strong>
                          <span>人工决策必须由单独配置的 harness_reviewer 执行，普通登录不会自动获得晋级权限。</span>
                        </div>
                        <div v-for="attempt in evolutionPromotionAudit.latest" :key="attempt.id" class="evolution-promotion-attempt">
                          <span>{{ evolutionPromotionDecisionLabel(attempt.requested_decision) }} · {{ evolutionPromotionOutcomeLabel(attempt.outcome) }}</span>
                          <strong>{{ evolutionPromotionAttemptReasonLabel(attempt.reason_code) }}</strong>
                          <small>Report {{ shortRevision(attempt.report_sha256) }} · Attempt {{ shortRevision(attempt.attempt_sha256) }} · Active pointer changed={{ attempt.active_pointer_changed }}</small>
                        </div>
                        <div class="evaluation-decision-strip">
                          <span class="dependency-ready">拒绝可审计</span>
                          <span class="dependency-ready">重复提交幂等</span>
                          <span class="dependency-ready">批准门不全则阻断</span>
                          <span class="dependency-ready">本阶段不影响线上流量</span>
                        </div>
                        <details class="evolution-control-acceptance">
                          <summary>查看控制状态机验收（{{ evolutionControlAcceptance ? `${evolutionControlAcceptance.passed_count}/${evolutionControlAcceptance.case_count}` : '尚未运行' }}）</summary>
                          <div class="metric-catalog-heading">
                            <div>
                              <strong>CAS 活动指针与单步回滚 · 确定性内存验收</strong>
                              <span>验证未来生产控制语义；不会创建活动指针，也不会让当前负收益候选进入 Shadow</span>
                            </div>
                            <button :disabled="runningEvolutionControlAcceptance" @click="runEvolutionControlAcceptance">
                              {{ runningEvolutionControlAcceptance ? '验收中...' : '运行 10 项 CAS / rollback 验收' }}
                            </button>
                          </div>
                          <div v-if="evolutionControlAcceptance">
                            <div class="diagnostic-evaluation-grid">
                              <div><strong>{{ evolutionControlAcceptance.passed_count }}/{{ evolutionControlAcceptance.case_count }}</strong><span>状态机用例通过</span></div>
                              <div><strong>{{ evolutionControlAcceptance.production_writes }}</strong><span>生产写入</span></div>
                              <div><strong>{{ evolutionControlAcceptance.production_active_pointers }}</strong><span>生产活动指针</span></div>
                              <div><strong>{{ shortRevision(evolutionControlAcceptance.report_sha256) }}</strong><span>验收报告 Hash</span></div>
                            </div>
                            <div class="failure-acceptance-grid">
                              <span v-for="item in evolutionControlAcceptance.cases" :key="item.reason_code" :class="item.passed ? 'dependency-ready' : 'dependency-down'">{{ evolutionControlAcceptanceLabel(item.reason_code) }}</span>
                            </div>
                            <small v-for="limitation in evolutionControlAcceptance.limitations" :key="limitation" class="evolution-limitation">{{ limitation }}</small>
                          </div>
                          <div v-else class="strategy-control-empty">尚无控制状态机验收报告；运行时只使用隔离的内存指针，不读写生产策略。</div>
                        </details>
                        <details v-if="evolutionShadowControlAudit" class="evolution-shadow-control">
                          <summary>查看隔离 Shadow / rollback 控制面（{{ evolutionShadowControlAudit.active_pointers.length }} 个指针 · {{ evolutionShadowControlAudit.blocked_count }} 次阻断）</summary>
                          <div class="metric-catalog-heading">
                            <div>
                              <strong>真实治理控制面 · {{ evolutionShadowControlAudit.scope }}</strong>
                              <span>追加式 MySQL 审计 · 独立 reviewer · CAS state version · 不接管线上路由</span>
                            </div>
                            <div class="evolution-promotion-actions">
                              <button :disabled="submittingShadowControl || !evolutionShadowControlAudit.can_control" @click="requestEvolutionShadow">请求当前候选进入 Shadow</button>
                              <button :disabled="submittingShadowControl || !evolutionShadowControlAudit.can_control" @click="rollbackEvolutionShadow">回滚隔离指针</button>
                            </div>
                          </div>
                          <div class="diagnostic-evaluation-grid">
                            <div><strong>{{ evolutionShadowControlAudit.event_count }}</strong><span>控制事件</span></div>
                            <div><strong>{{ evolutionShadowControlAudit.applied_count }}</strong><span>已执行</span></div>
                            <div><strong>{{ evolutionShadowControlAudit.blocked_count }}</strong><span>已阻断</span></div>
                            <div><strong>{{ evolutionShadowControlAudit.active_pointers.length }}</strong><span>隔离 Shadow 指针</span></div>
                          </div>
                          <article v-for="pointer in evolutionShadowControlAudit.active_pointers" :key="pointer.artifact_type" class="evolution-shadow-pointer">
                            <strong>{{ evolutionArtifactLabel(pointer.artifact_type) }} · {{ pointer.current_version }}</strong>
                            <span>State v{{ pointer.state_version }} · Previous {{ pointer.previous_version || '已消费' }} · {{ pointer.last_transition }}</span>
                          </article>
                          <div v-for="event in evolutionShadowControlAudit.latest" :key="event.id" class="evolution-promotion-attempt">
                            <span>{{ evolutionShadowOperationLabel(event.operation) }} · {{ evolutionShadowOutcomeLabel(event.outcome) }}</span>
                            <strong>{{ evolutionShadowReasonLabel(event.reason_code) }}</strong>
                            <small>Event {{ shortRevision(event.event_sha256) }} · Expected v{{ event.expected_state_version }} · Pointer changed={{ event.pointer_changed }}</small>
                          </div>
                          <div class="evaluation-decision-strip">
                            <span class="dependency-ready">Scope=isolated_shadow</span>
                            <span class="dependency-ready">Affects live traffic=false</span>
                            <span class="dependency-ready">阻断也保留审计</span>
                            <span class="dependency-ready">回滚不依赖旧报告</span>
                          </div>
                          <small v-for="limitation in evolutionShadowControlAudit.limitations" :key="limitation" class="evolution-limitation">{{ limitation }}</small>
                        </details>
                      </article>
                    </div>
                    <div v-else class="strategy-control-empty">尚无公平比较报告。运行后会保存到 Release 目录外；同一实验再次点击只复用原报告，不会重新打开 Holdout。</div>
                  </details>
                </details>
              </details>
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
              <article v-if="prometheusRuntime" :class="['prometheus-runtime-card', prometheusRuntime.status]">
                <div class="metric-catalog-heading">
                  <div>
                    <strong>生产 Prometheus · {{ prometheusRuntimeStatusLabel(prometheusRuntime.status) }}</strong>
                    <span>{{ prometheusRuntime.rules_version }} · SHA {{ prometheusRuntime.rules_sha256.slice(0, 16) }}…</span>
                  </div>
                  <span :class="['evaluation-gate', prometheusRuntime.status === 'ready' ? 'passed' : 'failed']">{{ prometheusRuntime.healthy_target_count }} / {{ prometheusRuntime.expected_targets }} Targets Up</span>
                </div>
                <div class="diagnostic-evaluation-grid">
                  <div><strong>{{ prometheusRuntime.target_count }} / {{ prometheusRuntime.expected_targets }}</strong><span>实际 / 预期抓取目标</span></div>
                  <div><strong>{{ prometheusRuntime.group_count }} / {{ prometheusRuntime.expected_groups }}</strong><span>Recording Groups</span></div>
                  <div><strong>{{ prometheusRuntime.rule_count }} / {{ prometheusRuntime.expected_rules }}</strong><span>聚合规则</span></div>
                  <div><strong>{{ prometheusRuntime.failed_rule_count }}</strong><span>失败规则</span></div>
                </div>
                <div class="metric-component-strip">
                  <span v-for="target in prometheusRuntime.targets" :key="target.job" :class="target.health === 'up' ? 'dependency-ready' : 'dependency-down'">
                    {{ target.component === 'index_worker' ? 'Index Worker' : 'Backend' }} {{ target.health === 'up' ? 'Up' : 'Down' }}
                  </span>
                  <span class="dependency-ready">72h / 128MB 双保留上限</span>
                  <span class="dependency-ready">loopback :9092</span>
                </div>
                <div class="metric-domain-grid prometheus-rule-grid">
                  <article v-for="group in prometheusRuntime.groups" :key="group.name">
                    <strong>{{ recordingGroupLabel(group.name) }}</strong>
                    <span>{{ group.healthy_rule_count }} / {{ group.rule_count }} 健康 · {{ group.interval_seconds }}s 执行</span>
                  </article>
                </div>
                <p>这里读取真实 Prometheus HTTP API；它证明抓取与聚合规则正常，不代表当前业务样本量已经达到异常判定门槛。</p>
              </article>
              <div v-else class="evaluation-candidate-warning">
                <strong>生产 Prometheus 快照暂不可用</strong>
                <span>指标目录仍可审计，但不会把运行时不可用伪装为健康。</span>
              </div>
              <article v-if="grafanaRuntime" :class="['prometheus-runtime-card', 'grafana-runtime-card', grafanaRuntime.status]">
                <div class="metric-catalog-heading">
                  <div>
                    <strong>Grafana 可观测看板 · {{ grafanaRuntimeStatusLabel(grafanaRuntime.status) }}</strong>
                    <span>Grafana {{ grafanaRuntime.grafana_version }} · {{ grafanaRuntime.dashboard.uid }}</span>
                  </div>
                  <span :class="['evaluation-gate', grafanaRuntime.status === 'ready' ? 'passed' : 'failed']">
                    {{ grafanaRuntime.dashboard.panel_count }} 面板 · {{ grafanaRuntime.dashboard.query_count }} 查询
                  </span>
                </div>
                <div class="grafana-dashboard-groups">
                  <article v-for="group in grafanaRuntime.dashboard.groups" :key="group.title">
                    <strong>{{ group.title }}</strong>
                    <span>{{ group.panel_count }} 个面板</span>
                  </article>
                </div>
                <div class="metric-component-strip">
                  <span class="dependency-ready">Dashboard SHA {{ grafanaRuntime.dashboard.dashboard_sha256.slice(0, 16) }}…</span>
                  <span class="dependency-ready">Prometheus datasource 不可编辑</span>
                  <span class="dependency-ready">Docker 未发布 9093</span>
                  <span class="dependency-ready">30s 刷新</span>
                </div>
                <p>完整 Dashboard 由 Git 中的供应文件生成，公网只展示脱敏摘要；“控制”面板显示观察和建议，不代表系统已经自动调权。</p>
              </article>
              <div v-else class="evaluation-candidate-warning">
                <strong>Grafana 运行摘要暂不可用</strong>
                <span>不会用静态 JSON 存在来冒充运行时健康；部署门会单独校验进程、Datasource 和 Dashboard UID。</span>
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
          <article v-if="evaluationCatalog.review_manifest" class="review-manifest-card">
            <div class="metric-catalog-heading">
              <div>
                <strong>人工复核 Manifest · {{ evaluationCatalog.review_manifest.manifest_version }}</strong>
                <span>SHA {{ evaluationCatalog.review_manifest.manifest_sha256.slice(0, 16) }}… · 绑定 Catalog / Slice / Fixture</span>
              </div>
              <span :class="['evaluation-gate', evaluationCatalog.review_manifest.passed ? 'passed' : 'failed']">
                {{ evaluationCatalog.review_manifest.passed ? '来源与 Hash 校验通过' : '复核 Manifest 失败' }}
              </span>
            </div>
            <div class="diagnostic-evaluation-grid">
              <div><strong>{{ evaluationCatalog.review_manifest.reviewed_cases }} / {{ evaluationCatalog.review_manifest.total_cases }}</strong><span>逐例人工复核</span></div>
              <div><strong>{{ evaluationCatalog.review_manifest.pending_cases }}</strong><span>pending_user</span></div>
              <div><strong>{{ evaluationCatalog.review_manifest.fixtures.length }}</strong><span>固定 Fixture Hash</span></div>
              <div><strong>{{ evaluationCatalog.review_manifest.catalog_matched ? '一致' : '不一致' }}</strong><span>Catalog Hash</span></div>
            </div>
            <div class="catalog-review-entry">
              <div>
                <strong>人工逐例复核队列</strong>
                <span>分页读取、断点续审、追加修订；不会直接改写冻结数据集</span>
              </div>
              <div class="catalog-review-entry-actions">
                <button :disabled="downloadingCatalogReviewEvidence" @click="downloadCatalogReviewEvidence('json')">
                  {{ downloadingCatalogReviewEvidence ? '导出中...' : '导出当前证据' }}
                </button>
                <button @click="humanReviewOpen = true">
                  打开专注复核工作台
                </button>
              </div>
            </div>
            <section v-if="catalogReviewOpen" class="catalog-review-workbench">
              <div v-if="catalogReviewWorkbench" class="catalog-review-body">
                <div class="metric-catalog-heading">
                  <div>
                    <strong>当前登录复核人进度</strong>
                    <span>Catalog SHA {{ catalogReviewWorkbench.catalog_sha256.slice(0, 16) }}… · Review Set {{ catalogReviewWorkbench.progress.review_set_sha256.slice(0, 16) }}…</span>
                  </div>
                  <span :class="['evaluation-gate', catalogReviewWorkbench.progress.ready_for_sealed_materialization ? 'passed' : 'failed']">
                    {{ catalogReviewWorkbench.progress.ready_for_sealed_materialization ? '可进入独立封存门' : '仍在人工复核' }}
                  </span>
                </div>
                <div class="diagnostic-evaluation-grid">
                  <div><strong>{{ catalogReviewWorkbench.progress.reviewed }} / {{ catalogReviewWorkbench.progress.total }}</strong><span>已复核</span></div>
                  <div><strong>{{ catalogReviewWorkbench.progress.approved }}</strong><span>标签通过</span></div>
                  <div><strong>{{ catalogReviewWorkbench.progress.rejected }}</strong><span>退回修正</span></div>
                  <div><strong>{{ catalogReviewWorkbench.progress.pending }}</strong><span>待复核</span></div>
                </div>
                <div class="catalog-review-filters">
                  <label>切片
                    <select v-model="catalogReviewSlice" @change="changeCatalogReviewFilter">
                      <option value="">全部切片</option>
                      <option v-for="slice in evaluationCatalog.slices" :key="slice.name" :value="slice.name">{{ evaluationSliceLabel(slice.name) }}</option>
                    </select>
                  </label>
                  <label>状态
                    <select v-model="catalogReviewStatus" @change="changeCatalogReviewFilter">
                      <option value="pending">待复核</option>
                      <option value="rejected">退回修正</option>
                      <option value="approved">标签通过</option>
                      <option value="reviewed">全部已复核</option>
                      <option value="all">全部状态</option>
                    </select>
                  </label>
                  <span>当前筛选 {{ catalogReviewWorkbench.filtered_total }} 条 · 第 {{ catalogReviewWorkbench.page }} / {{ catalogReviewPageCount }} 页</span>
                </div>
                <article v-if="currentCatalogReviewCase" class="catalog-review-case">
                  <div class="catalog-review-case-heading">
                    <div>
                      <strong>{{ currentCatalogReviewCase.id }} · {{ evaluationSliceLabel(currentCatalogReviewCase.slice) }}</strong>
                      <span>Case SHA {{ currentCatalogReviewCase.case_sha256.slice(0, 16) }}…</span>
                    </div>
                    <span v-if="currentCatalogReviewCase.review" :class="currentCatalogReviewCase.review.decision === 'approved' ? 'dependency-ready' : 'dependency-down'">
                      {{ catalogReviewDecisionLabel(currentCatalogReviewCase.review.decision) }} · revision {{ currentCatalogReviewCase.review.revision }}
                    </span>
                    <span v-else class="dependency-down">待复核</span>
                  </div>
                  <p class="catalog-review-prompt">{{ currentCatalogReviewCase.prompt }}</p>
                  <details class="catalog-review-payload" open>
                    <summary>核对完整输入、期望结果与边界字段</summary>
                    <pre>{{ formatCatalogReviewContent(currentCatalogReviewCase.content) }}</pre>
                  </details>
                  <div v-if="currentCatalogReviewCase.review" class="catalog-review-history">
                    当前结论：{{ catalogReviewDecisionLabel(currentCatalogReviewCase.review.decision) }} ·
                    {{ currentCatalogReviewCase.review.reason_codes.map(catalogReviewReasonLabel).join('、') }} ·
                    SHA {{ currentCatalogReviewCase.review.review_sha256.slice(0, 16) }}…
                  </div>
                  <div class="catalog-review-decision">
                    <label><input v-model="catalogReviewDecision" type="radio" value="approved"> 标签与期望结果正确</label>
                    <label><input v-model="catalogReviewDecision" type="radio" value="rejected"> 退回数据修正</label>
                    <select v-if="catalogReviewDecision === 'rejected'" v-model="catalogReviewRejectReason">
                      <option value="ambiguous_input">问题或输入有歧义</option>
                      <option value="expected_result_incorrect">期望结果不正确</option>
                      <option value="missing_context">缺少必要上下文</option>
                      <option value="schema_issue">Schema/字段问题</option>
                      <option value="unsafe_or_sensitive">不安全或包含敏感内容</option>
                    </select>
                  </div>
                  <label class="catalog-review-ack">
                    <input v-model="catalogReviewAcknowledged" type="checkbox">
                    我已逐项核对本例输入、期望结果和边界字段；该动作会追加一条审计修订。
                  </label>
                  <button class="catalog-review-submit" :disabled="submittingCatalogReview || !catalogReviewAcknowledged" @click="submitCatalogCaseReview">
                    {{ submittingCatalogReview ? '提交中...' : (catalogReviewDecision === 'approved' ? '确认本例标签通过' : '确认退回修正') }}
                  </button>
                </article>
                <div v-else class="evaluation-candidate-warning passed">
                  <strong>当前筛选没有待展示用例</strong>
                  <span>可切换切片或状态查看其他用例；这不等同于 Full 320 已全部通过。</span>
                </div>
                <div class="catalog-review-pagination">
                  <button :disabled="loadingCatalogReview || catalogReviewPage <= 1" @click="moveCatalogReviewPage(-1)">上一例</button>
                  <button :disabled="loadingCatalogReview || catalogReviewPage >= catalogReviewPageCount" @click="moveCatalogReviewPage(1)">下一例</button>
                </div>
                <p class="catalog-review-boundary">复核记录按当前登录用户隔离。即使 320/320 全部通过，也只允许进入独立封存、重跑评测和基线门，不会自动切流或改写 active policy。</p>
              </div>
              <div v-else class="evaluation-candidate-warning"><strong>复核台尚未加载</strong><span>请重试；加载失败不会改变任何复核状态。</span></div>
            </section>
            <details class="review-fixture-list">
              <summary>查看数据来源、Reviewer 状态与 Fixture Hash</summary>
              <div class="failure-acceptance-grid">
                <span v-for="slice in evaluationCatalog.review_manifest.slices" :key="slice.name" :class="slice.review_status === 'reviewed' ? 'dependency-ready' : 'dependency-down'">
                  {{ evaluationSliceLabel(slice.name) }} · {{ reviewStatusLabel(slice.review_status) }} · reviewer={{ slice.reviewer || '未指定' }} · {{ slice.source_category }}
                </span>
                <span v-for="fixture in evaluationCatalog.review_manifest.fixtures" :key="fixture.name" class="dependency-ready">
                  {{ fixture.name }} · SHA {{ fixture.sha256.slice(0, 12) }}…
                </span>
              </div>
            </details>
          </article>
          <div class="strategy-registry-grid evaluation-catalog-grid">
            <article v-for="slice in evaluationCatalog.slices" :key="slice.name">
              <div class="strategy-card-title">
                <strong>{{ evaluationSliceLabel(slice.name) }}</strong>
                <span :class="slice.passed ? 'dependency-ready' : 'dependency-down'">{{ slice.passed ? 'Hash/Schema 通过' : '失败' }}</span>
              </div>
              <p>{{ slice.actual_count }} / {{ slice.expected_count }} 条 · pending_user {{ slice.review_counts.pending_user || 0 }} · human {{ slice.review_counts.human || 0 }}</p>
              <small>SHA {{ slice.actual_sha256.slice(0, 12) }}… · {{ reviewStatusLabel(reviewSliceFor(slice.name)?.review_status) }}</small>
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
            <button
              v-if="message.role === 'assistant' && message.meta?.requestId && message.meta?.status === 'done'"
              class="answer-feedback-btn"
              :class="{ submitted: message.meta.feedbackStatus === 'submitted' }"
              :disabled="message.meta.feedbackStatus === 'submitting' || message.meta.feedbackStatus === 'submitted'"
              title="点踩会以 100% 采样进入脱敏在线评测和失败池，不会直接修改线上策略"
              @click="submitDownvote(message)"
            >
              {{ message.meta.feedbackStatus === 'submitted' ? '✓ 已进入失败池' : (message.meta.feedbackStatus === 'submitting' ? '提交中...' : '👎 回答未解决') }}
            </button>
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
import HumanReviewWorkbench from '../components/HumanReviewWorkbench.vue'

export default {
  name: 'AIChat',
  components: { HumanReviewWorkbench },
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
    const interviewDemoOpen = ref(false)
    const loadingInterviewDemo = ref(false)
    const interviewDemoLoadWarning = ref('')
    const interviewDemoStep = ref(0)
    const interviewDemoSteps = [
      {
        id: 'scenario-routing', shortTitle: '场景与路由', timebox: '00:00–00:40', title: '先说明为什么系统不是“套壳聊天”', badge: 'Intent + Policy',
        thesis: '场景固定为“研发与运维知识助手”：普通问答、项目知识问答和故障诊断共享入口，但实际路由、Shadow 判断和用户显式开关互相独立。',
        action: '在下方发送一个项目问题，指出回答下方“实际路由”与“影子判断”两行。',
        proof: 'Trace、strategy、policy version 与三级意图置信度同时可见；Shadow 不改真实结果。',
        boundary: '当前 Shadow 只记录建议和指标，不自主切流。',
        question: '为什么不让意图识别直接决定线上路由？',
        answer: '先用 Shadow 建立混淆矩阵、低置信样本和回归基线，再经固定策略、依赖健康、稳定分桶和人工门逐级放量；这样可以把分类错误与路由事故隔离。',
        workspace: 'chat', workspaceLabel: '回到正式聊天'
      },
      {
        id: 'grounded-rag', shortTitle: '证据 RAG', timebox: '00:40–01:35', title: '展示“检索到”不等于“允许回答”', badge: 'Hybrid + Evidence Gate',
        thesis: '文档经过版本化异步索引，查询同时使用结构化 key path、关键词和向量召回；最终由证据覆盖、冲突检测和引用校验决定是否调用模型。',
        action: '打开证据检索，用 JSON + YAML 跨文档问题比较“基于证据回答”和“深度分析回答”。',
        proof: '答案包含行号级引用、文档版本、Child/Parent 召回诊断；无证据时明确拒答。',
        boundary: '父子上下文 A/B 未证明净收益，候选保持 0% 权重。',
        question: 'Redis 在 RAG 中是不是权威数据库？',
        answer: '不是。MySQL 保存文档、版本、Chunk 与状态机，Redis 只承载短缓存和向量投影；Worker 启动时按 MySQL 权威状态对账并清除陈旧向量，Redis 故障不能改变业务真相。',
        workspace: 'knowledge', workspaceLabel: '打开证据检索'
      },
      {
        id: 'bounded-agents', shortTitle: '有限多 Agent', timebox: '01:35–02:30', title: '只在复杂度收益足够时拆 Agent', badge: 'MAX 2 + Budget',
        thesis: 'Planner 先判断独立故障域和知识核对需求；只有复杂任务才并行 KnowledgeAgent 与 DiagnosticAgent，并在统一预算、超时与引用合并器内收束。',
        action: '打开策略演算，输入同时包含配置核对、HTTP 502 与 Redis NOAUTH 的复合问题，先规划再运行协作 Shadow。',
        proof: '两个 Agent 并行、独立状态与预算可见；合并器只保留有引用 Claim，失败时显式降级。',
        boundary: '最多 2 个 Agent，禁止递归创建；协作结果仍不替换正式聊天。',
        question: '为什么不做完全自由的多 Agent 自主协作？',
        answer: '自由拓扑难以预算、取消、重放和归因。这里把拆分条件、Agent 上限、输出 Schema、总超时和降级策略都写进契约，先证明目标复杂样本的成对收益，再讨论是否晋级。',
        workspace: 'policy', workspaceLabel: '打开策略演算'
      },
      {
        id: 'governed-runtime', shortTitle: '工具与记忆', timebox: '02:30–03:20', title: '让工具和记忆成为受治理能力', badge: 'Schema + ACL + Audit',
        thesis: 'Tool Runtime 对 Registry、参数 Schema、意图、权限、副作用、预算、超时、熔断、缓存和审计统一治理；三级记忆分别处理 Working、Episodic 与 Profile。',
        action: '打开受治理工具，执行 MCP 发布证据或 Backend Ready；随后可查看三级记忆的来源、冲突和修正入口。',
        proof: 'ToolMessage 展示版本、耗时、参数 Hash、证据引用与缓存状态；原始参数和用户身份不进审计。',
        boundary: '无任意 Shell/URL/文件路径；Profile 记忆需用户可见、可改、可删。',
        question: 'MCP 和普通函数调用的区别是什么？',
        answer: 'MCP 解决协议发现与跨进程互操作，真正的安全性来自外层 Runtime：固定 allowlist、Schema、ACL、副作用等级、预算、超时、熔断和审计。协议接入不能替代治理。',
        workspace: 'tools', workspaceLabel: '打开受治理工具'
      },
      {
        id: 'eval-loop', shortTitle: '评测闭环', timebox: '03:20–05:00', title: '用证据决定“改不改”，而不是自动自嗨', badge: 'Eval + Observe + Gate',
        thesis: '离线 Full 320、成对 A/B、LLM-as-a-Judge、在线反馈、Prometheus 固定阈值与滑动 Z-score 汇入同一证据包；控制器当前只生成建议。',
        action: '打开评测总览，依次看 8/12 证据包、三类故障演练、Recommend-only 控制器与 Harness Evolution 负结果。',
        proof: '每条数字绑定样本量、Hash、CI/p 值或边界；负收益候选被 Gate 拒绝且活动策略不变。',
        boundary: '人工 0/320 与 Judge 0/30 尚未完成；不能宣称全部质量收益已上线。',
        question: '这算不算 Harness 自进化？',
        answer: '实现了失败聚类、白名单最小 Patch、三分区、公平预算 A/B、人工 Promotion 与隔离 Shadow 的受门禁演化链；当前候选为负收益并被拒绝，所以只能称“演化机制与拒绝门已验证”，不能称质量已自进化提升。',
        workspace: 'evaluation', workspaceLabel: '打开评测总览'
      }
    ]
    const currentInterviewDemoStep = computed(() => interviewDemoSteps[interviewDemoStep.value] || interviewDemoSteps[0])
    const evaluationCatalogOpen = ref(false)
    const humanReviewOpen = ref(false)
    const loadingEvaluationCatalog = ref(false)
    // The compact interview guide intentionally preloads only four reports.
    // Keep a separate marker for the full workbench so that a preloaded catalog
    // does not suppress G10, Judge, control-loop, or evolution requests.
    const evaluationWorkbenchLoaded = ref(false)
    const evaluationCatalog = ref(null)
    const evaluationRun = ref(null)
    const catalogReviewOpen = ref(false)
    const loadingCatalogReview = ref(false)
    const submittingCatalogReview = ref(false)
    const downloadingCatalogReviewEvidence = ref(false)
    const catalogReviewWorkbench = ref(null)
    const catalogReviewSlice = ref('')
    const catalogReviewStatus = ref('pending')
    const catalogReviewPage = ref(1)
    const catalogReviewDecision = ref('approved')
    const catalogReviewRejectReason = ref('expected_result_incorrect')
    const catalogReviewAcknowledged = ref(false)
    const catalogReviewIdempotencyKey = ref('')
    const currentCatalogReviewCase = computed(() => catalogReviewWorkbench.value?.cases?.[0] || null)
    const catalogReviewPageCount = computed(() => Math.max(1, Math.ceil((catalogReviewWorkbench.value?.filtered_total || 0) / (catalogReviewWorkbench.value?.page_size || 1))))
    const pairedComparison = ref(null)
    const judgeCalibrationAudit = ref(null)
    const loadingJudgeCalibration = ref(false)
    const submittingJudgeReview = ref(false)
    const interviewEvidence = ref(null)
    const downloadingInterviewEvidence = ref(false)
    const g10Review = ref(null)
    const downloadingG10Review = ref(false)
    const selectedResumeFactIDs = ref([])
    const resumeQualifierAcknowledged = ref(false)
    const submittingResumeConfirmation = ref(false)
    const g10ResumeSelectionValid = computed(() => selectedResumeFactIDs.value.length >= 3 && selectedResumeFactIDs.value.length <= 5 && resumeQualifierAcknowledged.value)
    const cleanupAudit = ref(null)
    const runningCleanupAudit = ref(false)
    const judgeCalibrationIndex = ref(0)
    const judgeCalibrationDraft = ref({ relevance: '', completeness: '', helpfulness: '', groundedness: '', safety: '' })
    const judgeCalibrationDimensions = [
      { value: 'relevance', label: '相关性' }, { value: 'completeness', label: '完整性' }, { value: 'helpfulness', label: '有用性' },
      { value: 'groundedness', label: '有依据' }, { value: 'safety', label: '安全性' }
    ]
    const judgeCalibrationScoreOptions = [
      { value: 0, label: '0 · 完全不满足' }, { value: 0.25, label: '0.25 · 较差' }, { value: 0.5, label: '0.50 · 部分满足' },
      { value: 0.75, label: '0.75 · 基本满足' }, { value: 1, label: '1.00 · 完全满足' }
    ]
    const currentJudgeCalibrationCase = computed(() => judgeCalibrationAudit.value?.cases?.[judgeCalibrationIndex.value] || null)
    const judgeCalibrationDraftComplete = computed(() => judgeCalibrationDimensions.every(dimension => judgeCalibrationDraft.value[dimension.value] !== ''))
    const metricCatalog = ref(null)
    const prometheusRuntime = ref(null)
    const grafanaRuntime = ref(null)
    const loadingAnomaly = ref(false)
    const anomalyResult = ref(null)
    const loadingProductionAnomaly = ref(false)
    const productionAnomaly = ref(null)
    const webhookAudit = ref(null)
    const runningWebhookAcceptance = ref(false)
    const controllerAudit = ref(null)
    const controllerAcceptance = ref(null)
    const runningControllerAcceptance = ref(false)
    const faultCampaignAudit = ref(null)
    const faultCampaignResult = ref(null)
    const runningFaultCampaign = ref(false)
    const reliabilityAcceptance = ref(null)
    const runningReliabilityAcceptance = ref(false)
    const performanceReport = ref(null)
    const loadingPerformanceReport = ref(false)
    const activeFaultCampaign = computed(() => faultCampaignResult.value || faultCampaignAudit.value?.latest || null)
    const onlineEvaluationAudit = ref(null)
    const onlineEvaluationAcceptance = ref(null)
    const runningOnlineEvaluationAcceptance = ref(false)
    const loadingOnlineEvaluationAudit = ref(false)
    const failurePoolAudit = ref(null)
    const failurePoolAcceptance = ref(null)
    const refreshingFailurePool = ref(false)
    const runningFailurePoolAcceptance = ref(false)
    const evolutionAudit = ref(null)
    const evolutionMaterialization = ref(null)
    const materializingEvolution = ref(false)
    const evolutionSplitAudit = ref(null)
    const evolutionSplitAcceptance = ref(null)
    const runningEvolutionSplitAcceptance = ref(false)
    const evolutionComparisonReport = ref(null)
    const runningEvolutionComparison = ref(false)
    const evolutionPromotionAudit = ref(null)
    const submittingPromotionReview = ref(false)
    const evolutionControlAcceptance = ref(null)
    const runningEvolutionControlAcceptance = ref(false)
    const evolutionShadowControlAudit = ref(null)
    const submittingShadowControl = ref(false)
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
      if (keep !== 'interview') interviewDemoOpen.value = false
      if (keep !== 'memory') memoryPreviewOpen.value = false
      if (keep !== 'tools') toolRuntimeOpen.value = false
      if (keep !== 'policy') policyControlOpen.value = false
      if (keep !== 'evaluation') {
        evaluationCatalogOpen.value = false
        catalogReviewOpen.value = false
      }
      if (keep !== 'knowledge') knowledgeSearchOpen.value = false
    }

    const toggleInterviewDemo = async () => {
      const opening = !interviewDemoOpen.value
      closeUtilityWorkspaces(opening ? 'interview' : '')
      interviewDemoOpen.value = opening
      if (!opening || loadingInterviewDemo.value) return
      try {
        loadingInterviewDemo.value = true
        interviewDemoLoadWarning.value = ''
        const [catalogResponse, runResponse, evidenceResponse, cleanupResponse] = await Promise.all([
          api.get('/evaluations/catalog/latest').catch(() => null),
          api.get('/evaluations/unified/latest').catch(() => null),
          api.get('/evaluations/interview-evidence/latest').catch(() => null),
          api.get('/evaluations/cleanup/latest').catch(() => null)
        ])
        evaluationCatalog.value = catalogResponse?.data || evaluationCatalog.value
        evaluationRun.value = runResponse?.data || evaluationRun.value
        interviewEvidence.value = evidenceResponse?.data || interviewEvidence.value
        cleanupAudit.value = cleanupResponse?.data || cleanupAudit.value
        if (!catalogResponse?.data) interviewDemoLoadWarning.value = 'Full 320 目录当前不可用；导览可继续，但不能展示为已验证。'
        else if (!evidenceResponse?.data) interviewDemoLoadWarning.value = '当前 Release 的完整证据包尚未就绪；请到评测总览刷新缺失的验收报告。'
      } finally {
        loadingInterviewDemo.value = false
      }
    }

    const openInterviewDemoWorkspace = async (workspace) => {
      interviewDemoOpen.value = false
      if (workspace === 'chat') {
        closeUtilityWorkspaces()
        await nextTick()
        messageInput.value?.focus()
        return
      }
      if (workspace === 'knowledge' && !knowledgeSearchOpen.value) await toggleKnowledgeSearch()
      if (workspace === 'policy' && !policyControlOpen.value) await togglePolicyControl()
      if (workspace === 'tools' && !toolRuntimeOpen.value) await toggleToolRuntime()
      if (workspace === 'evaluation' && !evaluationCatalogOpen.value) await toggleEvaluationCatalog()
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
        meta: { status: 'streaming', strategy: '固定路由', strategyVersion: '', policyVersion: '加载中', traceId: '', requestId: '', question, intentShadow: null }
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
              requestId: payload.request_id || '',
              strategy: payload.strategy || '固定路由',
              strategyVersion: payload.strategy_version || '',
              policyVersion: payload.policy_version || '',
              question,
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
            message.meta.confidence = Number(payload.confidence || 0)
            message.meta.resolved = Boolean(payload.resolved)
            message.meta.needsUserInput = Boolean(payload.needs_user_input)
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
              sessMsgs[lastIndex].meta = { ...currentMessages.value[aiMessageIndex].meta }
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
              requestId: response.data.request_id || '', question,
              strategy: response.data.strategy || '固定路由', policyVersion: response.data.policy_version || '',
              strategyVersion: response.data.strategy_version || '', intent: response.data.intent || '',
              confidence: Number(response.data.confidence || 0), resolved: Boolean(response.data.resolved),
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
              requestId: response.data.request_id || '', question,
              strategy: response.data.strategy || '固定路由', policyVersion: response.data.policy_version || '',
              strategyVersion: response.data.strategy_version || '', intent: response.data.intent || '',
              confidence: Number(response.data.confidence || 0), resolved: Boolean(response.data.resolved),
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

    const submitDownvote = async (message) => {
      const meta = message?.meta
      if (!meta?.requestId || !meta?.traceId || meta.feedbackStatus === 'submitting' || meta.feedbackStatus === 'submitted') return
      meta.feedbackStatus = 'submitting'
      currentMessages.value = [...currentMessages.value]
      try {
        const response = await api.post(`/chat/messages/${encodeURIComponent(meta.requestId)}/feedback`, {
          trace_id: meta.traceId,
          question: meta.question || '',
          answer: message.content || '',
          confidence: Number(meta.confidence || 0),
          resolved: Boolean(meta.resolved),
          feedback: 'user_downvote'
        })
        meta.feedbackStatus = 'submitted'
        meta.feedbackSampleId = response.data?.sample_id || ''
        ElMessage.success(response.data?.created ? '反馈已可靠写入，并以 100% 采样进入异步评测' : '这条回答已经反馈过，没有重复入队')
      } catch (error) {
        meta.feedbackStatus = 'error'
        ElMessage.error(error.response?.data?.message || '反馈提交失败，请稍后重试')
      } finally {
        currentMessages.value = [...currentMessages.value]
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
      if (!evaluationCatalogOpen.value || evaluationWorkbenchLoaded.value || loadingEvaluationCatalog.value) return
      try {
        loadingEvaluationCatalog.value = true
        const [catalogResponse, runResponse, pairedResponse, judgeCalibrationResponse, interviewEvidenceResponse, g10ReviewResponse, cleanupAuditResponse, metricCatalogResponse, prometheusRuntimeResponse, grafanaRuntimeResponse, productionAnomalyResponse, webhookAuditResponse, controllerAuditResponse, faultCampaignAuditResponse, reliabilityResponse, onlineEvaluationResponse, failurePoolResponse, evolutionResponse, evolutionSplitResponse, evolutionComparisonResponse, evolutionPromotionResponse, evolutionControlResponse, evolutionShadowControlResponse, performanceResponse] = await Promise.all([
          api.get('/evaluations/catalog/latest'),
          api.get('/evaluations/unified/latest'),
          api.get('/evaluations/paired/latest').catch(() => null),
          api.get('/evaluations/judge-calibration/latest').catch(() => null),
          api.get('/evaluations/interview-evidence/latest').catch(() => null),
          api.get('/evaluations/g10/latest').catch(() => null),
          api.get('/evaluations/cleanup/latest').catch(() => null),
          api.get('/evaluations/metrics/catalog'),
          api.get('/evaluations/metrics/runtime').catch(() => null),
          api.get('/evaluations/metrics/dashboard').catch(() => null),
          api.get('/evaluations/anomaly/production/latest').catch(() => null),
          api.get('/evaluations/webhooks/latest').catch(() => null),
          api.get('/evaluations/controller/latest').catch(() => null),
          api.get('/evaluations/fault-campaigns/latest').catch(() => null),
          api.get('/evaluations/reliability/latest').catch(() => null),
          api.get('/evaluations/online/latest').catch(() => null),
          api.get('/evaluations/failure-pool/latest').catch(() => null),
          api.get('/evaluations/evolution/latest').catch(() => null),
          api.get('/evaluations/evolution/splits/latest').catch(() => null),
          api.get('/evaluations/evolution/comparison/latest').catch(() => null),
          api.get('/evaluations/evolution/promotion/latest').catch(() => null),
          api.get('/evaluations/evolution/promotion/control/latest').catch(() => null),
          api.get('/evaluations/evolution/promotion/shadow/latest').catch(() => null),
          api.get('/evaluations/performance/latest').catch(() => null)
        ])
        evaluationCatalog.value = catalogResponse.data
        evaluationRun.value = runResponse.data
        pairedComparison.value = pairedResponse?.data || null
        judgeCalibrationAudit.value = judgeCalibrationResponse?.data || null
        if (judgeCalibrationAudit.value) initializeJudgeCalibrationSelection()
        interviewEvidence.value = interviewEvidenceResponse?.data || null
        g10Review.value = g10ReviewResponse?.data || null
        hydrateG10ResumeSelection()
        cleanupAudit.value = cleanupAuditResponse?.data || null
        metricCatalog.value = metricCatalogResponse.data.report
        prometheusRuntime.value = prometheusRuntimeResponse?.data?.snapshot || null
        grafanaRuntime.value = grafanaRuntimeResponse?.data?.snapshot || null
        productionAnomaly.value = productionAnomalyResponse?.data || null
        webhookAudit.value = webhookAuditResponse?.data || null
        controllerAudit.value = controllerAuditResponse?.data || null
        faultCampaignAudit.value = faultCampaignAuditResponse?.data || null
        reliabilityAcceptance.value = reliabilityResponse?.data || null
        onlineEvaluationAudit.value = onlineEvaluationResponse?.data || null
        failurePoolAudit.value = failurePoolResponse?.data || null
        evolutionAudit.value = evolutionResponse?.data || null
        evolutionSplitAudit.value = evolutionSplitResponse?.data || null
        evolutionComparisonReport.value = evolutionComparisonResponse?.data || null
        evolutionPromotionAudit.value = evolutionPromotionResponse?.data || null
        evolutionControlAcceptance.value = evolutionControlResponse?.data || null
        evolutionShadowControlAudit.value = evolutionShadowControlResponse?.data || null
        performanceReport.value = performanceResponse?.data || null
        evaluationWorkbenchLoaded.value = true
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

    const reviewSliceFor = (name) => evaluationCatalog.value?.review_manifest?.slices?.find(item => item.name === name) || null

    const reviewStatusLabel = (status) => ({
      pending_user: '待用户逐例复核', reviewed: '已完成人工复核', rejected: '人工拒绝'
    }[status] || status || '复核状态缺失')

    const loadCatalogReviewWorkbench = async () => {
      if (loadingCatalogReview.value) return
      try {
        loadingCatalogReview.value = true
        const response = await api.get('/evaluations/catalog/reviews', {
          params: {
            slice: catalogReviewSlice.value || undefined,
            status: catalogReviewStatus.value,
            page: catalogReviewPage.value,
            page_size: 1
          }
        })
        catalogReviewWorkbench.value = response.data
        if (!response.data?.cases?.length && response.data?.filtered_total > 0 && catalogReviewPage.value > 1) {
          catalogReviewPage.value = Math.max(1, Math.ceil(response.data.filtered_total / response.data.page_size))
          loadingCatalogReview.value = false
          await loadCatalogReviewWorkbench()
        }
      } catch (error) {
        catalogReviewWorkbench.value = null
        ElMessage.error(error.response?.data?.message || '逐例复核工作台暂不可用')
      } finally {
        loadingCatalogReview.value = false
      }
    }

    const closeHumanReviewWorkbench = async () => {
      humanReviewOpen.value = false
      try {
        const response = await api.get('/evaluations/g10/latest')
        g10Review.value = response.data
        hydrateG10ResumeSelection()
      } catch (_) {
        // G10 may not have been loaded yet; the full evaluation workbench keeps its own explicit error path.
      }
    }

    const toggleCatalogReviewWorkbench = async () => {
      catalogReviewOpen.value = !catalogReviewOpen.value
      if (catalogReviewOpen.value) await loadCatalogReviewWorkbench()
    }

    const changeCatalogReviewFilter = async () => {
      catalogReviewPage.value = 1
      catalogReviewAcknowledged.value = false
      catalogReviewIdempotencyKey.value = ''
      await loadCatalogReviewWorkbench()
    }

    const moveCatalogReviewPage = async (offset) => {
      const target = catalogReviewPage.value + offset
      if (target < 1 || target > catalogReviewPageCount.value) return
      catalogReviewPage.value = target
      catalogReviewAcknowledged.value = false
      catalogReviewIdempotencyKey.value = ''
      await loadCatalogReviewWorkbench()
    }

    const submitCatalogCaseReview = async () => {
      const item = currentCatalogReviewCase.value
      if (!item || submittingCatalogReview.value || !catalogReviewAcknowledged.value) return
      if (!catalogReviewIdempotencyKey.value) {
        const randomPart = window.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(16).slice(2)}`
        catalogReviewIdempotencyKey.value = `catalog-review-${randomPart}`
      }
      try {
        submittingCatalogReview.value = true
        const reasonCodes = catalogReviewDecision.value === 'approved' ? ['label_verified'] : [catalogReviewRejectReason.value]
        const response = await api.post('/evaluations/catalog/reviews', {
          mode: 'human_catalog_case_review',
          catalog_sha256: catalogReviewWorkbench.value.catalog_sha256,
          case_id: item.id,
          case_sha256: item.case_sha256,
          expected_revision: item.review?.revision || 0,
          decision: catalogReviewDecision.value,
          reason_codes: reasonCodes,
          idempotency_key: catalogReviewIdempotencyKey.value,
          acknowledgment: 'I_REVIEWED_CASE_AND_EXPECTED_RESULT'
        })
        ElMessage.success(`${response.data.created ? '已追加' : '已复用'} ${item.id} 的人工复核记录`)
        catalogReviewAcknowledged.value = false
        catalogReviewIdempotencyKey.value = ''
        await loadCatalogReviewWorkbench()
      } catch (error) {
        if (error.response?.status === 409) {
          catalogReviewIdempotencyKey.value = ''
          await loadCatalogReviewWorkbench()
        }
        ElMessage.error(error.response?.data?.message || '逐例复核提交失败')
      } finally {
        submittingCatalogReview.value = false
      }
    }

    const catalogReviewDecisionLabel = (decision) => ({ approved: '标签通过', rejected: '退回修正' }[decision] || decision)
    const catalogReviewReasonLabel = (reason) => ({
      label_verified: '已核对标签', ambiguous_input: '输入有歧义', expected_result_incorrect: '期望结果不正确',
      missing_context: '缺少上下文', schema_issue: 'Schema 问题', unsafe_or_sensitive: '不安全或敏感'
    }[reason] || reason)
    const formatCatalogReviewContent = (content) => JSON.stringify(content || {}, null, 2)

    const downloadCatalogReviewEvidence = async (format) => {
      if (downloadingCatalogReviewEvidence.value || !['json', 'markdown'].includes(format)) return
      try {
        downloadingCatalogReviewEvidence.value = true
        const response = await api.get('/evaluations/catalog/reviews/evidence', { params: { format }, responseType: 'blob' })
        const blob = new Blob([response.data], { type: format === 'json' ? 'application/json' : 'text/markdown;charset=utf-8' })
        const url = URL.createObjectURL(blob)
        const link = document.createElement('a')
        link.href = url
        link.download = `gopherai-full-320-review-evidence.${format === 'json' ? 'json' : 'md'}`
        document.body.appendChild(link)
        link.click()
        link.remove()
        URL.revokeObjectURL(url)
        ElMessage.success('当前人工复核证据已下载；未完成状态不会被包装成正式基线')
      } catch (error) {
        ElMessage.error(error.response?.data?.message || '人工复核证据下载失败')
      } finally {
        downloadingCatalogReviewEvidence.value = false
      }
    }

    const evaluationStatusLabel = (status) => ({
      technical_candidate: '技术候选 · 不可切流', rejected: '技术门拒绝', baseline_eligible: '可冻结基线 · 仍不可自动切流'
    }[status] || status)

    const evaluationFailureLabel = (code) => ({
      misclassification: '普通误分类', severe_misroute: '严重误路由', citation_gap: '引用覆盖缺口',
      verification_gap: '验证步骤缺口', retrieval_miss: '检索漏召回', unsupported_answer: '无证据作答',
      runtime_error: '运行错误', unsafe_action: '危险动作', nondeterministic_replay: '重放不一致'
    }[code] || code)

    const pairedComparisonLabel = (name) => ({
      collaboration_target_quality: '多 Agent 复杂诊断质量',
      parent_context_target_quality: '父子 RAG 跨文档质量'
    }[name] || name)

    const pairedConclusionLabel = (conclusion) => ({
      candidate_better: '候选有统计收益', candidate_worse: '候选显著退化', inconclusive: '未证明差异'
    }[conclusion] || conclusion)

    const judgeCalibrationSliceLabel = (slice) => ({
      rag_single_fact: 'RAG 单事实', rag_cross_document: 'RAG 跨文档', insufficient_evidence: '证据不足',
      diagnosis: '故障诊断', tool_governance: '工具治理', memory: '三级记忆'
    }[slice] || slice)

    const initializeJudgeCalibrationSelection = (preferredIndex) => {
      const cases = judgeCalibrationAudit.value?.cases || []
      if (!cases.length) return
      let index = Number.isInteger(preferredIndex) ? preferredIndex : cases.findIndex(item => !item.human_scores)
      if (index < 0 || index >= cases.length) index = 0
      selectJudgeCalibrationCase(index)
    }

    const selectJudgeCalibrationCase = (index) => {
      const cases = judgeCalibrationAudit.value?.cases || []
      if (!Number.isInteger(index) || index < 0 || index >= cases.length) return
      judgeCalibrationIndex.value = index
      const scores = cases[index].human_scores
      judgeCalibrationDraft.value = scores
        ? { relevance: scores.relevance, completeness: scores.completeness, helpfulness: scores.helpfulness, groundedness: scores.groundedness, safety: scores.safety }
        : { relevance: '', completeness: '', helpfulness: '', groundedness: '', safety: '' }
    }

    const loadJudgeCalibration = async () => {
      if (loadingJudgeCalibration.value) return
      try {
        loadingJudgeCalibration.value = true
        const response = await api.get('/evaluations/judge-calibration/latest')
        judgeCalibrationAudit.value = response.data
        initializeJudgeCalibrationSelection(judgeCalibrationIndex.value)
        ElMessage.success('已读取真实 Judge 报告与当前账号人工复核进度')
      } catch (error) {
        ElMessage.error(error.response?.data?.message || 'Judge 真实评分报告尚未生成')
      } finally {
        loadingJudgeCalibration.value = false
      }
    }

    const submitJudgeCalibrationReview = async () => {
      if (submittingJudgeReview.value || !judgeCalibrationDraftComplete.value || !currentJudgeCalibrationCase.value) return
      try {
        submittingJudgeReview.value = true
        const currentID = currentJudgeCalibrationCase.value.id
        await api.post('/evaluations/judge-calibration/reviews', { case_id: currentID, scores: judgeCalibrationDraft.value })
        const currentIndex = judgeCalibrationIndex.value
        const response = await api.get('/evaluations/judge-calibration/latest')
        judgeCalibrationAudit.value = response.data
        const nextPending = response.data.cases.findIndex((item, index) => index > currentIndex && !item.human_scores)
        initializeJudgeCalibrationSelection(nextPending >= 0 ? nextPending : currentIndex)
        ElMessage.success('人工评分已追加到不可变审计记录')
      } catch (error) {
        ElMessage.error(error.response?.data?.message || '人工评分保存失败')
      } finally {
        submittingJudgeReview.value = false
      }
    }

    const interviewEvidenceStatusLabel = (status) => ({
      resume_ready: '简历指标就绪', candidate_only: '技术候选 · 待人工', negative_result: '负结果 · 保留',
      calibration_pending: '人工校准未完成', technical_gate_failed: '技术门失败', verified_negative_control_result: '负结果与治理证据就绪'
    }[status] || status)

    const interviewEvidenceMetricLabel = (name) => ({
      catalog_validation_rate: '目录校验', execution_coverage: '执行覆盖', completion_rate: '执行完成',
      baseline_success_rate: '基线成功率', candidate_success_rate: '候选成功率', paired_mean_quality_delta: '成对质量差',
      hot_success_rate: '热路径成功率', hot_total_latency_p95: '端到端 P95', hot_ttft_p95: 'TTFT P95',
      estimated_tokens_per_100: '估算 Token/100 请求', judge_technical_completion: 'Judge 技术完成',
      human_review_completion: '人工复核', linear_weighted_kappa: '线性加权 κ',
      fault_detection_rate: '故障检测', fault_recovery_rate: '恢复识别', healthy_false_positive_rate: '健康误报',
      mean_mttd_seconds: '平均 MTTD', recommendations_created: '生成建议', recommendations_applied: '实际应用',
      agent_recovery_rate: 'Agent 恢复', duplicate_resume_executions: '重复 Resume', sse_cancellation_rate: 'SSE 取消传播',
      cancel_propagation_p95: '取消传播 P95', active_workers_after: '结束活跃 Worker', metric_family_count: '指标族',
      required_metric_contract_coverage: '核心契约覆盖', forbidden_label_hits: '高基数标签命中', series_budget_utilization: '序列预算使用',
      grafana_panel_count: 'Grafana 面板', grafana_query_count: 'PromQL 查询', grafana_group_count: '看板分组',
      grafana_public_exposure: '公网暴露', control_recommended_total: '累计建议', control_blocked_total: '累计阻断',
      recent_control_applied: '最近实际应用', acceptance_recommended_present: '建议分支已验收', acceptance_blocked_present: '阻断分支已验收',
      production_lineage_artifacts: '生产谱系候选', split_coverage: '分区覆盖', holdout_open_count: 'Holdout 打开次数',
      control_state_machine_acceptance: 'CAS/回滚状态机验收', human_rejections_recorded: '人工拒绝记录', approval_attempts_blocked: '批准阻断',
      shadow_control_events_blocked: 'Shadow 控制阻断', isolated_shadow_active_pointers: '隔离活动指针',
      evolution_candidate_mean_delta: 'Evolution 候选质量差', validation_candidate_mean_delta: 'Validation 候选质量差',
      cleanup_verified_rate: '清理审计闭环', cleanup_removed_count: '已删除并复核', cleanup_retained_boundaries: '保留运行边界',
      cleanup_blocked_count: '清理阻断', retired_entry_calls_24h: '旧入口 24h 调用',
      retired_entry_observation_coverage: '零调用窗口覆盖', tracked_source_count: '受控源码文件'
    }[name] || name)

    const formatInterviewEvidenceMetric = (metric) => {
      if (metric.numerator !== undefined && metric.denominator !== undefined) {
        return `${metric.numerator}/${metric.denominator}（${metricPercent(metric.value)}）`
      }
      if (metric.unit === 'ms') return `${metric.value < 1 ? metric.value.toFixed(3) : metric.value.toFixed(0)}ms`
      if (metric.unit === 'seconds') return `${metric.value.toFixed(0)}s`
      if (metric.unit === 'boolean') return metric.value === 1 ? '是' : '否'
      if (metric.unit === 'ratio') {
        const interval = metric.ci95 ? `，95% CI [${metricPercent(metric.ci95.lower)}, ${metricPercent(metric.ci95.upper)}]` : ''
        const pValue = metric.p_value !== undefined ? `，p=${metric.p_value.toFixed(4)}` : ''
        return `${metricPercent(metric.value)}${interval}${pValue}`
      }
      if (metric.unit === 'coefficient') return metric.value.toFixed(4)
      return `${metric.value}`
    }

    const downloadInterviewEvidence = async (format) => {
      if (downloadingInterviewEvidence.value || !['json', 'markdown'].includes(format)) return
      try {
        downloadingInterviewEvidence.value = true
        const response = await api.get('/evaluations/interview-evidence/latest', { params: { format }, responseType: 'blob' })
        const blob = new Blob([response.data], { type: format === 'json' ? 'application/json' : 'text/markdown;charset=utf-8' })
        const url = URL.createObjectURL(blob)
        const link = document.createElement('a')
        link.href = url
        link.download = `gopherai-interview-evidence.${format === 'json' ? 'json' : 'md'}`
        document.body.appendChild(link)
        link.click()
        link.remove()
        URL.revokeObjectURL(url)
        ElMessage.success('面试证据包已下载')
      } catch (error) {
        ElMessage.error(error.response?.data?.message || '面试证据包下载失败')
      } finally {
        downloadingInterviewEvidence.value = false
      }
    }

    const g10ReviewStatusLabel = (status) => ({
      g10_blocked_by_human_and_environment_gates: '人工与环境门阻断',
      g10_pending_user_and_environment_gates: '等待用户与环境门',
      g10_blocked_by_product_and_environment_gates: '产品与环境门阻断',
      g10_deferred_by_environment_gate: '仅环境门延期'
    }[status] || status || '状态未知')

    const g10GateStatusLabel = (status) => ({
      passed: '通过', blocked: '阻断', pending_user: '待用户确认', deferred_environment: '环境延期'
    }[status] || status || '未知')

    const g10CatalogRerunLabel = (state) => ({
      not_applicable: '未运行（等待封存）',
      not_started: '未运行',
      running: '运行中',
      execution_failed: '执行失败',
      technical_gate_failed: '技术门失败',
      technical_passed: '技术通过（未晋级）'
    }[state] || state || '状态未知')

    const g10GateStatusClass = (status) => ({
      ready: status === 'passed',
      blocked: status === 'blocked',
      pending: status === 'pending_user' || status === 'deferred_environment'
    })

    const downloadG10Review = async (format) => {
      if (downloadingG10Review.value || !['json', 'markdown'].includes(format)) return
      try {
        downloadingG10Review.value = true
        const response = await api.get('/evaluations/g10/latest', { params: { format }, responseType: 'blob' })
        const blob = new Blob([response.data], { type: format === 'json' ? 'application/json' : 'text/markdown;charset=utf-8' })
        const url = URL.createObjectURL(blob)
        const link = document.createElement('a')
        link.href = url
        link.download = `gopherai-g10-release-review.${format === 'json' ? 'json' : 'md'}`
        document.body.appendChild(link)
        link.click()
        link.remove()
        URL.revokeObjectURL(url)
        ElMessage.success('G10 发布事实核验已下载')
      } catch (error) {
        ElMessage.error(error.response?.data?.message || 'G10 发布事实核验下载失败')
      } finally {
        downloadingG10Review.value = false
      }
    }

    const hydrateG10ResumeSelection = () => {
      const confirmation = g10Review.value?.resume_confirmation
      if (confirmation?.current_binding && Array.isArray(confirmation.selected_fact_ids)) {
        selectedResumeFactIDs.value = [...confirmation.selected_fact_ids]
      }
    }

    const submitG10ResumeConfirmation = async () => {
      if (!g10Review.value || !g10ResumeSelectionValid.value || submittingResumeConfirmation.value) return
      const orderedIDs = g10Review.value.resume_facts.map(fact => fact.statement_id).filter(id => selectedResumeFactIDs.value.includes(id))
      const orderedIndexes = g10Review.value.resume_facts.map((fact, index) => selectedResumeFactIDs.value.includes(fact.statement_id) ? index : -1).filter(index => index >= 0)
      try {
        submittingResumeConfirmation.value = true
        const response = await api.post('/evaluations/g10/resume-confirmations', {
          mode: 'human_resume_fact_confirmation',
          fact_set_sha256: g10Review.value.resume_fact_set_sha256,
          selected_fact_ids: orderedIDs,
          idempotency_key: `resume-${g10Review.value.resume_fact_set_sha256.slice(0, 32)}-${orderedIndexes.join('-')}`,
          acknowledgment: 'I_CONFIRM_RESUME_FACTS_WITH_REQUIRED_QUALIFIERS'
        })
        g10Review.value = response.data.review
        hydrateG10ResumeSelection()
        resumeQualifierAcknowledged.value = false
        ElMessage.success(response.data.reused ? '相同事实确认已幂等复用' : '简历事实确认已追加记录；生产与产品总门仍独立生效')
      } catch (error) {
        ElMessage.error(error.response?.data?.message || '简历事实确认失败')
      } finally {
        submittingResumeConfirmation.value = false
      }
    }

    const runCleanupAudit = async () => {
      if (runningCleanupAudit.value) return
      try {
        runningCleanupAudit.value = true
        const response = await api.post('/evaluations/cleanup/acceptance')
        cleanupAudit.value = response.data
        if (response.data.summary.cleanup_complete) {
          const evidenceResponse = await api.get('/evaluations/interview-evidence/latest').catch(() => null)
          interviewEvidence.value = evidenceResponse?.data || interviewEvidence.value
          const g10Response = await api.get('/evaluations/g10/latest').catch(() => null)
          g10Review.value = g10Response?.data || g10Review.value
          hydrateG10ResumeSelection()
          ElMessage.success(evidenceResponse?.data ? '清理闭环完成，并已刷新可复现面试证据包' : '清理闭环完成：授权候选已删除，必要协议边界仍保留')
        }
        else if (response.data.summary.deletion_plan_ready) ElMessage.success('清理前审计通过：候选仍将通过独立提交删除')
        else ElMessage.warning('清理前审计完成，但观测覆盖、调用量或源码引用仍有阻断')
      } catch (error) {
        ElMessage.error(error.response?.data?.message || '清理前审计失败')
      } finally {
        runningCleanupAudit.value = false
      }
    }

    const cleanupObservationLabel = (status) => ({
      zero_calls_verified: '24h 窗口零调用且覆盖达标', calls_observed: '观察到旧入口调用', insufficient_coverage: '采样覆盖不足，不能判零'
    }[status] || status)

    const cleanupCandidateStatusLabel = (status) => ({
      removed_verified: '已删除并复核', eligible_for_deletion: '可进入独立删除提交', retained_required: '保留',
      retained_observation_sentinel: '保留观测哨兵', blocked_active_reference: '仍有外部引用', blocked_user_approval_required: '等待授权',
      blocked_required_artifact_missing: '必要边界缺失'
    }[status] || status)

    const metricDomainLabel = (domain) => ({
      platform: '平台入口', intent: '意图识别', knowledge_rag: '知识与 RAG', agent_harness: 'Agent Harness',
      memory: '三级记忆', tool_governance: '工具治理', multi_agent: '多 Agent', evaluation: '评测与反馈', control_plane: '策略控制面'
    }[domain] || domain)

    const metricTypeLabel = (type) => ({ counter: 'Counter', histogram: 'Histogram', gauge: 'Gauge' }[type] || type)

    const prometheusRuntimeStatusLabel = (status) => ({ ready: '运行正常', warming: '正在预热', degraded: '运行降级' }[status] || '状态未知')

    const grafanaRuntimeStatusLabel = (status) => ({ ready: '运行正常', version_mismatch: '版本不匹配' }[status] || '状态未知')

    const recordingGroupLabel = (name) => ({
      'gopherai-scrape-and-request-5m': '抓取与请求 · 5m',
      'gopherai-agent-tool-10m': 'Agent 与工具 · 10m',
      'gopherai-rag-quality-control-15m': 'RAG 与控制 · 15m',
      'gopherai-evaluation-feedback-30m': '评测与反馈 · 30m'
    }[name] || name)

    const anomalyMetricLabel = (metric) => ({
      rag_grounded_answer_rate: 'RAG 有依据回答率', request_p95_latency_seconds: '请求 P95 延迟'
    }[metric] || metric)

    const anomalyRecommendationLabel = (action) => ({
      none: '不产生策略建议', reduce_candidate_weight: '建议候选策略降权（未执行）'
    }[action] || action)

    const anomalyDecisionLabel = (status) => ({
      anomalous: '检测到退化', healthy: '窗口健康', insufficient_data: '数据不足 · 暂不判定',
      insufficient_window: '历史窗口不足 · 暂不判定'
    }[status] || '状态未知')

    const anomalyDecisionClass = (analysis) => ({
      anomalous: 'anomaly-detected', healthy: 'anomaly-healthy', insufficient_data: 'anomaly-insufficient', insufficient_window: 'anomaly-insufficient'
    }[analysis?.decision_status] || 'anomaly-insufficient')

    const anomalySignalStatusLabel = (status) => ({
      suppressed: '已抑制', healthy: '健康', anomalous: '异常', warning: '告警', critical: '严重', insufficient_window: '窗口不足'
    }[status] || status)

    const productionWindowStatusLabel = (status) => ({
      ready: '窗口可判定', warming: '采样预热中', anomalous: '检测到退化'
    }[status] || '状态未知')

    const productionDataStatusLabel = (status) => ({
      observed: '已观测', no_series: '暂无序列', no_finite_value: '暂无有限值'
    }[status] || status)

    const productionMetricValue = (series) => {
      if (series?.data_status !== 'observed') return '--'
      if (series.metric === 'rag_grounded_answer_rate') return `${(Number(series.latest.value) * 100).toFixed(1)}%`
      if (series.metric === 'request_p95_latency_seconds') return `${Number(series.latest.value).toFixed(3)}s`
      return Number(series.latest.value).toFixed(3)
    }

    const webhookEventLabel = (eventType) => ({
      opened: '异常开启', updated: '异常更新', resolved: '异常恢复', control_action: '控制建议', rollback: '回滚'
    }[eventType] || eventType)

    const controllerStatusLabel = (status) => ({ recommended: '已生成不可变候选', blocked: '已被门禁阻断' }[status] || status)

    const controllerReasonLabel = (reason) => ({
      candidate_created: '候选已生成但未激活', baseline_not_eligible: '正式基线不合格',
      strategy_not_in_active_policy: '目标策略不在活动策略中', healthy_fallback_unavailable: '没有健康 fallback',
      exploration_floor_guard: '触及 5% 探索下限'
    }[reason] || reason)

    const controllerWeight = (basis) => `${(Number(basis || 0) / 100).toFixed(0)}%`

    const loadControllerAudit = async () => {
      const response = await api.get('/evaluations/controller/latest')
      controllerAudit.value = response.data
      return response.data
    }

    const runControllerAcceptance = async () => {
      if (runningControllerAcceptance.value) return
      try {
        runningControllerAcceptance.value = true
        const response = await api.post('/evaluations/controller/acceptance', { scenario: 'recommend_only_guardrails' })
        controllerAcceptance.value = response.data
        await loadControllerAudit()
        if (response.data?.active_policy_unchanged && response.data?.eligible_fixture?.status === 'recommended' && response.data?.eligible_fixture?.applied === false) {
          ElMessage.success('控制器已生成不可变候选，活动策略版本与哈希保持不变')
        } else {
          ElMessage.error('控制器验收未证明活动策略不可写')
        }
      } catch (error) {
        ElMessage.error(error.response?.data?.message || '只建议控制器验收暂时不可用')
      } finally {
        runningControllerAcceptance.value = false
      }
    }

    const loadFaultCampaignAudit = async () => {
      const response = await api.get('/evaluations/fault-campaigns/latest')
      faultCampaignAudit.value = response.data
      return response.data
    }

    const runFaultCampaignAcceptance = async () => {
      if (runningFaultCampaign.value) return
      try {
        runningFaultCampaign.value = true
        const response = await api.post('/evaluations/fault-campaigns/acceptance', { scenario: 'three_failure_classes' })
        faultCampaignResult.value = response.data
        await loadFaultCampaignAudit()
        const summary = response.data?.summary
        if (summary?.detected_count === 3 && summary?.recovered_count === 3 && summary?.false_positives === 0 && summary?.applied_count === 0) {
          ElMessage.success('三类隔离故障均被检测并识别恢复，线上策略变更为 0')
        } else {
          ElMessage.error('故障演练未同时满足检测、恢复、零误报和零落地门禁')
        }
      } catch (error) {
        ElMessage.error(error.response?.data?.message || '三类隔离故障演练暂时不可用')
      } finally {
        runningFaultCampaign.value = false
      }
    }

    const runReliabilityAcceptance = async () => {
      if (runningReliabilityAcceptance.value) return
      try {
        runningReliabilityAcceptance.value = true
        const response = await api.post('/evaluations/reliability/acceptance', {})
        reliabilityAcceptance.value = response.data
        if (response.data?.passed) {
          const evidenceResponse = await api.get('/evaluations/interview-evidence/latest').catch(() => null)
          interviewEvidence.value = evidenceResponse?.data || interviewEvidence.value
          ElMessage.success('Agent Checkpoint 恢复与 SSE 取消传播均已通过，证据包已刷新')
        }
        else ElMessage.warning('可靠性验收未全部通过，请查看恢复与取消指标')
      } catch (error) {
        ElMessage.error(error.response?.data?.message || '可靠性故障验收暂不可用')
      } finally {
        runningReliabilityAcceptance.value = false
      }
    }

    const refreshPerformanceReport = async () => {
      if (loadingPerformanceReport.value) return
      try {
        loadingPerformanceReport.value = true
        const response = await api.get('/evaluations/performance/latest')
        performanceReport.value = response.data
        ElMessage.success('已读取 ECS 性能与 pprof 证据报告')
      } catch (error) {
        ElMessage.warning(error.response?.data?.message || '尚未生成有效的 ECS 性能报告')
      } finally {
        loadingPerformanceReport.value = false
      }
    }

    const reliabilityGuardrailLabel = (value) => ({
      isolated_repository: '隔离仓库', same_production_harness_service: '复用生产 Harness', same_production_stream_service: '复用生产流式服务',
      no_external_model_calls: '不调用外部模型', no_tool_calls: '不调用工具', no_production_state_write: '不写生产状态',
      bounded_500ms_cancel_gate: '取消 P95 ≤ 500ms'
    }[value] || value)

    const onlineEvaluationStatusLabel = (status) => ({
      pending: '等待 Outbox', evaluating: 'Judge 处理中', completed: '评测完成', judge_failed: 'Judge 失败（未伪造分数）', dead: '进入死信'
    }[status] || status)

    const onlineEvaluationStageLabel = (stage) => ({
      mysql_outbox: 'MySQL Outbox', rabbitmq: 'RabbitMQ', online_eval_consumer: '独立 Consumer', deterministic_completion: '确定性完成'
    }[stage] || stage)

    const loadOnlineEvaluationAudit = async () => {
      const response = await api.get('/evaluations/online/latest')
      onlineEvaluationAudit.value = response.data
      return response.data
    }

    const refreshOnlineEvaluationAudit = async () => {
      if (loadingOnlineEvaluationAudit.value) return
      try {
        loadingOnlineEvaluationAudit.value = true
        await loadOnlineEvaluationAudit()
        ElMessage.success('已刷新最近 24 小时生产样本')
      } catch (error) {
        ElMessage.error(error.response?.data?.message || '在线评测生产样本刷新失败')
      } finally {
        loadingOnlineEvaluationAudit.value = false
      }
    }

    const runOnlineEvaluationAcceptance = async () => {
      if (runningOnlineEvaluationAcceptance.value) return
      try {
        runningOnlineEvaluationAcceptance.value = true
        const response = await api.post('/evaluations/online/acceptance', {})
        onlineEvaluationAcceptance.value = response.data
        await loadOnlineEvaluationAudit()
        if (response.data?.passed && response.data?.final_status === 'completed' && response.data?.raw_identity_persisted === false) {
          ElMessage.success('分层采样、脱敏、Outbox、RabbitMQ 与独立消费链路均已通过')
        } else {
          ElMessage.warning('采样规则已验证，但异步消费链路尚未完成，请检查 Worker 与 RabbitMQ')
        }
      } catch (error) {
        ElMessage.error(error.response?.data?.message || '在线评测异步链路验收暂不可用')
      } finally {
        runningOnlineEvaluationAcceptance.value = false
      }
    }

    const refreshFailurePool = async () => {
      if (refreshingFailurePool.value) return
      try {
        refreshingFailurePool.value = true
        const response = await api.post('/evaluations/failure-pool/refresh', {})
        failurePoolAudit.value = response.data
        ElMessage.success(response.data?.created ? '已从生产在线评测样本创建新的不可变失败聚类快照' : '输入样本未变化，复用已有聚类快照')
      } catch (error) {
        ElMessage.error(error.response?.data?.message || '失败样本聚类暂不可用')
      } finally {
        refreshingFailurePool.value = false
      }
    }

    const runFailurePoolAcceptance = async () => {
      if (runningFailurePoolAcceptance.value) return
      try {
        runningFailurePoolAcceptance.value = true
        const response = await api.post('/evaluations/failure-pool/acceptance', {})
        failurePoolAcceptance.value = response.data
        if (response.data?.passed) ElMessage.success('8 类 WHERE×WHY 映射与不可执行候选边界均已通过')
        else ElMessage.warning('失败样本聚类验收未全部通过')
      } catch (error) {
        ElMessage.error(error.response?.data?.message || '失败样本聚类验收暂不可用')
      } finally {
        runningFailurePoolAcceptance.value = false
      }
    }

    const failureProposalFor = (clusterId) => failurePoolAudit.value?.proposals?.find(item => item.cluster_id === clusterId) || null

    const materializeEvolutionCandidates = async () => {
      if (materializingEvolution.value) return
      try {
        materializingEvolution.value = true
        const response = await api.post('/evaluations/evolution/materialize', { source: 'latest_failure_pool' })
        evolutionMaterialization.value = response.data
        evolutionAudit.value = response.data.audit
        const skipped = response.data?.skipped?.length || 0
        ElMessage.success(`Harness 离线候选：新建 ${response.data.created_count}，幂等复用 ${response.data.existing_count}，安全跳过 ${skipped}`)
      } catch (error) {
        ElMessage.error(error.response?.data?.message || '当前失败池无法生成合法 Harness 候选')
      } finally {
        materializingEvolution.value = false
      }
    }

    const evolutionArtifactLabel = (value) => ({
      prompt_template: 'Prompt Template', context_policy: 'Context Policy', diagnostic_playbook: 'Diagnostic Playbook'
    }[value] || value)

    const evolutionGuardrailLabel = (value) => ({
      failure_metadata_only: '只读失败元数据', allowlisted_artifact_types: '仅白名单 Artifact', single_variable_patch: '单变量 Patch',
      static_validation_required: '必须静态校验', append_only_lineage: '追加式谱系', human_approval_required: '必须人工批准',
      no_active_pointer: 'Evolver 无活动指针', no_source_or_permission_changes: '禁止源码与权限变更'
    }[value] || value)

    const runEvolutionSplitAcceptance = async () => {
      if (runningEvolutionSplitAcceptance.value) return
      try {
        runningEvolutionSplitAcceptance.value = true
        const response = await api.post('/evaluations/evolution/splits/acceptance', { mode: 'deterministic_no_write' })
        evolutionSplitAcceptance.value = response.data
        evolutionSplitAudit.value = response.data.audit
        if (response.data?.passed) ElMessage.success('8 项数据分区、防泄漏与 Holdout 重开门禁全部通过')
        else ElMessage.warning('Harness 数据分区验收未全部通过')
      } catch (error) {
        ElMessage.error(error.response?.data?.message || 'Harness 数据分区验收暂不可用')
      } finally {
        runningEvolutionSplitAcceptance.value = false
      }
    }

    const evolutionSplitLabel = (value) => ({ evolution: 'Evolution 搜索集', validation: 'Validation 选择集', sealed_holdout: 'Sealed Holdout 最终集' }[value] || value)

    const evolutionSplitPurposeLabel = (value) => ({
      candidate_search_and_feedback: '仅用于候选搜索与反馈', post_freeze_model_selection: '候选冻结后才可用于模型选择',
      one_time_final_generalization_check: '仅用于一次最终泛化检查'
    }[value] || value)

    const evolutionSplitAcceptanceLabel = (value) => ({
      source_hash_verified: '来源 Catalog Hash 一致', unique_full_coverage: '40 条唯一且完整覆盖', zero_overlap: '三分区零重叠',
      evolution_search_allowed: '候选搜索仅允许 Evolution', validation_search_denied: '候选搜索拒绝 Validation',
      holdout_search_denied: '候选搜索拒绝 Holdout', candidate_freeze_required: 'Validation 强制候选冻结',
      new_experiment_required: 'Holdout 重开强制新实验版本'
    }[value] || value)

    const runEvolutionComparison = async () => {
      if (runningEvolutionComparison.value) return
      try {
        runningEvolutionComparison.value = true
        const response = await api.post('/evaluations/evolution/comparison/run', { mode: 'controlled_offline_contract_comparison' })
        evolutionComparisonReport.value = response.data.report
        if (response.data.reused) ElMessage.success('同一实验已存在，已复用原报告且没有重新打开 Holdout')
        else ElMessage.success('公平 A/B 已完成；候选未证明收益，Holdout 保持 sealed')
      } catch (error) {
        ElMessage.error(error.response?.data?.message || 'Harness 公平 A/B 暂不可用')
      } finally {
        runningEvolutionComparison.value = false
      }
    }

    const submitPromotionReview = async (decision) => {
      if (submittingPromotionReview.value || !evolutionComparisonReport.value || !evolutionPromotionAudit.value?.can_review) return
      const report = evolutionComparisonReport.value
      try {
        submittingPromotionReview.value = true
        const response = await api.post('/evaluations/evolution/promotion/review', {
          mode: 'human_gate_no_activation',
          experiment_version: report.experiment_version,
          candidate_sha256: report.candidate.artifact_sha256,
          report_sha256: report.report_sha256,
          decision,
          reason_code: decision === 'rejected' ? 'candidate_no_measured_gain' : 'human_approval_requested',
          idempotency_key: `promotion-${report.report_sha256.slice(0, 32)}-${decision}`,
          acknowledgment: 'I_CONFIRM_HUMAN_PROMOTION_REVIEW'
        })
        const auditResponse = await api.get('/evaluations/evolution/promotion/latest')
        evolutionPromotionAudit.value = auditResponse.data
        const attempt = response.data.attempt
        if (attempt.outcome === 'blocked') ElMessage.warning(`批准请求已被门禁阻断：${evolutionPromotionAttemptReasonLabel(attempt.reason_code)}`)
        else if (response.data.reused) ElMessage.success('相同人工决策已存在，本次幂等复用且活动指针未变化')
        else ElMessage.success('人工拒绝已追加记录，活动指针仍为 0')
      } catch (error) {
        ElMessage.error(error.response?.data?.message || 'Harness 人工晋级评审暂不可用')
      } finally {
        submittingPromotionReview.value = false
      }
    }

    const evolutionPromotionDecisionLabel = (value) => ({ approved: '请求批准', rejected: '人工拒绝' }[value] || value)
    const evolutionPromotionOutcomeLabel = (value) => ({ recorded: '已记录', blocked: '已阻断' }[value] || value)
    const evolutionPromotionAttemptReasonLabel = (value) => ({
      candidate_no_measured_gain: '候选未证明可测量收益', risk_not_acceptable: '风险不可接受', needs_more_evidence: '需要更多证据',
      controlled_fixture_not_promotable: '受控 Fixture 不是生产候选', offline_promotion_gate_failed: '离线收益门未通过',
      human_labels_pending: '人工标签未完成', safety_regression: '检测到安全回归', sealed_holdout_not_passed: 'Sealed Holdout 未通过',
      human_approved_for_shadow: '人工批准进入隔离 Shadow'
    }[value] || value)

    const runEvolutionControlAcceptance = async () => {
      if (runningEvolutionControlAcceptance.value) return
      try {
        runningEvolutionControlAcceptance.value = true
        const response = await api.post('/evaluations/evolution/promotion/control/acceptance')
        evolutionControlAcceptance.value = response.data
        if (response.data?.passed_count === response.data?.case_count) ElMessage.success('10 项 CAS / rollback 状态机验收全部通过，生产写入与活动指针均为 0')
        else ElMessage.warning('Harness 控制状态机验收未全部通过')
      } catch (error) {
        ElMessage.error(error.response?.data?.message || 'Harness 控制状态机验收暂不可用')
      } finally {
        runningEvolutionControlAcceptance.value = false
      }
    }

    const evolutionControlAcceptanceLabel = (value) => ({
      human_approval_required: '无人工批准不能激活', offline_gate_required: '离线收益门必须通过',
      shadow_gate_required: '隔离 Shadow 必须通过', safety_gate_required: '安全回归门必须通过',
      parent_version_mismatch: '候选父版本必须匹配活动指针', cas_activation_succeeded: '合法候选可原子切换',
      state_version_conflict: '过期 State Version 被拒绝', single_cas_winner: '并发切换只有一个赢家',
      rollback_restored_previous: '回滚恢复父版本', rollback_target_consumed: '回滚目标消费后不可反复切换'
    }[value] || value)

    const refreshEvolutionShadowControl = async () => {
      const response = await api.get('/evaluations/evolution/promotion/shadow/latest')
      evolutionShadowControlAudit.value = response.data
    }

    const requestEvolutionShadow = async () => {
      if (submittingShadowControl.value || !evolutionComparisonReport.value || !evolutionShadowControlAudit.value?.can_control) return
      const report = evolutionComparisonReport.value
      const pointer = evolutionShadowControlAudit.value.active_pointers.find(item => item.artifact_type === report.candidate.artifact_type)
      try {
        submittingShadowControl.value = true
        const response = await api.post('/evaluations/evolution/promotion/shadow/request', {
          mode: 'governed_isolated_shadow', experiment_version: report.experiment_version, artifact_type: report.candidate.artifact_type,
          candidate_version: report.candidate.artifact_version, candidate_sha256: report.candidate.artifact_sha256, report_sha256: report.report_sha256,
          expected_state_version: pointer?.state_version || 0, idempotency_key: `shadow-${report.report_sha256.slice(0, 24)}-${evolutionPromotionAudit.value?.attempt_count || 0}`,
          acknowledgment: 'I_CONFIRM_HARNESS_CONTROL_OPERATION'
        })
        await refreshEvolutionShadowControl()
        const event = response.data.event
        if (event.outcome === 'blocked') ElMessage.warning(`隔离 Shadow 请求已被门禁阻断：${evolutionShadowReasonLabel(event.reason_code)}`)
        else if (response.data.reused) ElMessage.success('相同隔离 Shadow 请求已幂等复用')
        else ElMessage.success('候选已原子切换到隔离 Shadow；线上聊天流量未改变')
      } catch (error) {
        ElMessage.error(error.response?.data?.message || '隔离 Shadow 请求暂不可用')
      } finally {
        submittingShadowControl.value = false
      }
    }

    const rollbackEvolutionShadow = async () => {
      if (submittingShadowControl.value || !evolutionComparisonReport.value || !evolutionShadowControlAudit.value?.can_control) return
      const artifactType = evolutionComparisonReport.value.candidate.artifact_type
      const pointer = evolutionShadowControlAudit.value.active_pointers.find(item => item.artifact_type === artifactType)
      const expectedVersion = pointer?.state_version || 0
      try {
        submittingShadowControl.value = true
        const response = await api.post('/evaluations/evolution/promotion/shadow/rollback', {
          mode: 'governed_isolated_shadow', artifact_type: artifactType, expected_state_version: expectedVersion,
          idempotency_key: `rollback-${artifactType}-${expectedVersion}`, acknowledgment: 'I_CONFIRM_HARNESS_CONTROL_OPERATION'
        })
        await refreshEvolutionShadowControl()
        const event = response.data.event
        if (event.outcome === 'blocked') ElMessage.warning(`隔离回滚请求已被门禁阻断：${evolutionShadowReasonLabel(event.reason_code)}`)
        else if (response.data.reused) ElMessage.success('相同回滚请求已幂等复用')
        else ElMessage.success('隔离 Shadow 指针已原子回滚；线上聊天流量未改变')
      } catch (error) {
        ElMessage.error(error.response?.data?.message || '隔离 Shadow 回滚暂不可用')
      } finally {
        submittingShadowControl.value = false
      }
    }

    const evolutionShadowOperationLabel = (value) => ({ shadow_admission: 'Shadow 准入', rollback: '隔离回滚' }[value] || value)
    const evolutionShadowOutcomeLabel = (value) => ({ applied: '已执行', blocked: '已阻断' }[value] || value)
    const evolutionShadowReasonLabel = (value) => ({
      controlled_fixture_not_promotable: '受控 Fixture 不是生产候选', offline_promotion_gate_failed: '离线收益门未通过',
      safety_regression: '安全回归门未通过', sealed_holdout_not_passed: 'Sealed Holdout 未通过', human_approval_required: '缺少已记录的人工批准',
      isolated_shadow_failed: '隔离 Shadow 探针未通过', pointer_cas_conflict: '活动指针版本冲突', isolated_shadow_activated: '已进入隔离 Shadow',
      rollback_completed: '已恢复直接父版本', rollback_unavailable: '没有可用的单步回滚目标'
    }[value] || value)

    const evolutionVariantLabel = (value) => ({
      frozen_harness: '旧 Harness', human_rule_candidate: '人工规则候选', same_budget_test_time_scaling: '同预算 TTS', evolved_candidate: '自动演化候选'
    }[value] || value)

    const evolutionPromotionLabel = (value) => ({ rejected: '候选被拒绝', promoted: '允许晋级', pending: '等待评测' }[value] || value)

    const evolutionHoldoutReasonLabel = (value) => ({ upstream_gain_gate_failed: 'Evolution / Validation 上游收益门未通过，Holdout 保持密封' }[value] || value)

    const evolutionPromotionReasonLabel = (value) => ({
      evolution_gain_not_demonstrated: 'Evolution 未证明正收益', validation_gain_not_demonstrated: 'Validation 未证明正收益',
      human_labels_pending: '人工标签待复核', controlled_fixture_not_promotable: '受控 Fixture 不可晋级', safety_regression: '存在安全回归'
    }[value] || value)

    const signedPercent = (value) => {
      const number = Number(value || 0) * 100
      return `${number > 0 ? '+' : ''}${number.toFixed(1)}%`
    }

    const failureWhereLabel = (value) => ({
      answer_quality: '回答质量', agent_budget: 'Agent 预算', tool_runtime: '工具运行时', retrieval_evidence_gate: '检索证据门',
      answer_generation: '答案生成', task_resolution: '任务解决', request_execution: '请求执行'
    }[value] || value)

    const failureWhyLabel = (value) => ({
      user_rejected_answer: '用户明确否定', execution_budget_exceeded: '执行预算耗尽', governed_tool_failure: '受治理工具失败',
      insufficient_or_missing_evidence: '证据缺失或不足', low_model_confidence: '模型低置信', unresolved_response: '任务未解决',
      request_error: '请求错误', judge_score_below_threshold: 'Judge 低于阈值'
    }[value] || value)

    const failureCandidateLabel = (value) => ({ dataset: '数据集候选', prompt: 'Prompt 候选', rule: '规则候选', parameter: '参数候选' }[value] || value)

    const failureTargetLabel = (value) => ({
      user_rejected_answer_case: '用户拒绝回答边界用例', rag_insufficient_evidence_case: 'RAG 证据不足用例',
      tool_failure_recovery_rule: '工具失败恢复规则', context_budget_allocation: '上下文预算分配',
      low_confidence_boundary_case: '低置信边界用例', resolution_output_contract: '任务解决输出契约',
      request_fallback_classification: '请求降级分类规则', grounded_answer_contract: '有依据回答契约'
    }[value] || value)

    const failureGuardrailLabel = (value) => ({
      metadata_only_clustering: '只用元数据聚类', raw_content_not_read: '不读取原始正文', immutable_candidates: '候选不可变',
      human_review_required: '必须人工复核', offline_gate_required: '必须离线回归', isolated_canary_required: '必须隔离 Canary',
      no_active_policy_write: '禁止写活动策略'
    }[value] || value)

    const faultClassLabel = (faultClass) => ({
      rag_degradation: 'RAG 退化', agent_latency: 'Agent 延迟', tool_failure: '工具失败'
    }[faultClass] || faultClass)

    const faultPhaseLabel = (phase) => ({
      before: '基线', injected: '注入', detected: '检测', recommendation: '建议', probe: '恢复探针', recovered: '恢复'
    }[phase] || phase)

    const faultMetricValue = (metric, value) => {
      if (metric === 'rag_grounded_answer_rate' || metric === 'tool_success_rate') return metricPercent(value)
      if (metric === 'agent_p95_latency_seconds') return `${Number(value).toFixed(2)}s`
      return Number(value).toFixed(3)
    }

    const loadWebhookAudit = async () => {
      const response = await api.get('/evaluations/webhooks/latest')
      webhookAudit.value = response.data
      return response.data
    }

    const runWebhookAcceptance = async () => {
      if (runningWebhookAcceptance.value) return
      try {
        runningWebhookAcceptance.value = true
        await api.post('/evaluations/webhooks/acceptance', { scenario: 'signed_loopback_delivery' })
        await new Promise(resolve => setTimeout(resolve, 5600))
        const audit = await loadWebhookAudit()
        const latest = audit.latest?.[0]
        if (latest?.simulation && latest.status === 'delivered' && latest.receipt_verified) {
          ElMessage.success('签名 Webhook 已异步送达，幂等回执验签通过')
        } else {
          ElMessage.info('验收事件已入队；可稍后重新打开本区域查看投递状态')
        }
      } catch (error) {
        ElMessage.error(error.response?.data?.message || 'Webhook 验收暂时不可用')
      } finally {
        runningWebhookAcceptance.value = false
      }
    }

    const loadProductionAnomaly = async () => {
      if (loadingProductionAnomaly.value) return
      try {
        loadingProductionAnomaly.value = true
        const response = await api.get('/evaluations/anomaly/production/latest')
        productionAnomaly.value = response.data
        if (response.data?.status === 'anomalous') ElMessage.warning('生产窗口检测到退化，但没有修改线上策略')
        else if (response.data?.status === 'ready') ElMessage.success('生产窗口已读取，当前没有触发降权建议')
        else ElMessage.info('生产窗口仍在积累样本，当前保持未决')
      } catch (error) {
        ElMessage.error(error.response?.data?.message || '生产异常窗口暂时不可用')
      } finally {
        loadingProductionAnomaly.value = false
      }
    }

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
      interviewDemoOpen,
      loadingInterviewDemo,
      interviewDemoLoadWarning,
      interviewDemoStep,
      interviewDemoSteps,
      currentInterviewDemoStep,
      evaluationCatalogOpen,
      humanReviewOpen,
      closeHumanReviewWorkbench,
      loadingEvaluationCatalog,
      evaluationCatalog,
      evaluationRun,
      catalogReviewOpen,
      loadingCatalogReview,
      submittingCatalogReview,
      downloadingCatalogReviewEvidence,
      catalogReviewWorkbench,
      catalogReviewSlice,
      catalogReviewStatus,
      catalogReviewPage,
      catalogReviewDecision,
      catalogReviewRejectReason,
      catalogReviewAcknowledged,
      currentCatalogReviewCase,
      catalogReviewPageCount,
      pairedComparison,
      judgeCalibrationAudit,
      loadingJudgeCalibration,
      submittingJudgeReview,
      interviewEvidence,
      downloadingInterviewEvidence,
      g10Review,
      downloadingG10Review,
      selectedResumeFactIDs,
      resumeQualifierAcknowledged,
      submittingResumeConfirmation,
      g10ResumeSelectionValid,
      cleanupAudit,
      runningCleanupAudit,
      judgeCalibrationIndex,
      judgeCalibrationDraft,
      judgeCalibrationDimensions,
      judgeCalibrationScoreOptions,
      currentJudgeCalibrationCase,
      judgeCalibrationDraftComplete,
      metricCatalog,
      prometheusRuntime,
      grafanaRuntime,
      loadingAnomaly,
      anomalyResult,
      anomalyScenarios,
      loadingProductionAnomaly,
      productionAnomaly,
      webhookAudit,
      runningWebhookAcceptance,
      controllerAudit,
      controllerAcceptance,
      runningControllerAcceptance,
      faultCampaignAudit,
      faultCampaignResult,
      runningFaultCampaign,
      reliabilityAcceptance,
      runningReliabilityAcceptance,
      performanceReport,
      loadingPerformanceReport,
      activeFaultCampaign,
      onlineEvaluationAudit,
      onlineEvaluationAcceptance,
      runningOnlineEvaluationAcceptance,
      loadingOnlineEvaluationAudit,
      failurePoolAudit,
      failurePoolAcceptance,
      refreshingFailurePool,
      runningFailurePoolAcceptance,
      evolutionAudit,
      evolutionMaterialization,
      materializingEvolution,
      evolutionSplitAudit,
      evolutionSplitAcceptance,
      runningEvolutionSplitAcceptance,
      evolutionComparisonReport,
      runningEvolutionComparison,
      evolutionPromotionAudit,
      submittingPromotionReview,
      submitPromotionReview,
      evolutionControlAcceptance,
      runningEvolutionControlAcceptance,
      runEvolutionControlAcceptance,
      evolutionControlAcceptanceLabel,
      evolutionShadowControlAudit,
      submittingShadowControl,
      requestEvolutionShadow,
      rollbackEvolutionShadow,
      evolutionShadowOperationLabel,
      evolutionShadowOutcomeLabel,
      evolutionShadowReasonLabel,
      evolutionPromotionDecisionLabel,
      evolutionPromotionOutcomeLabel,
      evolutionPromotionAttemptReasonLabel,
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
      toggleInterviewDemo,
      openInterviewDemoWorkspace,
      searchKnowledge,
      answerKnowledge,
      toggleParentContextEvaluation,
      toggleEvaluationCatalog,
      evaluationSliceLabel,
      reviewSliceFor,
      reviewStatusLabel,
      toggleCatalogReviewWorkbench,
      changeCatalogReviewFilter,
      moveCatalogReviewPage,
      submitCatalogCaseReview,
      catalogReviewDecisionLabel,
      catalogReviewReasonLabel,
      formatCatalogReviewContent,
      downloadCatalogReviewEvidence,
      evaluationStatusLabel,
      evaluationFailureLabel,
      pairedComparisonLabel,
      pairedConclusionLabel,
      judgeCalibrationSliceLabel,
      selectJudgeCalibrationCase,
      loadJudgeCalibration,
      submitJudgeCalibrationReview,
      interviewEvidenceStatusLabel,
      interviewEvidenceMetricLabel,
      formatInterviewEvidenceMetric,
      downloadInterviewEvidence,
      g10ReviewStatusLabel,
      g10GateStatusLabel,
      g10CatalogRerunLabel,
      g10GateStatusClass,
      downloadG10Review,
      submitG10ResumeConfirmation,
      runCleanupAudit,
      cleanupObservationLabel,
      cleanupCandidateStatusLabel,
      metricDomainLabel,
      metricTypeLabel,
      prometheusRuntimeStatusLabel,
      grafanaRuntimeStatusLabel,
      recordingGroupLabel,
      anomalyMetricLabel,
      anomalyRecommendationLabel,
      anomalyDecisionLabel,
      anomalyDecisionClass,
      anomalySignalStatusLabel,
      productionWindowStatusLabel,
      productionDataStatusLabel,
      productionMetricValue,
      webhookEventLabel,
      loadWebhookAudit,
      runWebhookAcceptance,
      controllerStatusLabel,
      controllerReasonLabel,
      controllerWeight,
      loadControllerAudit,
      runControllerAcceptance,
      loadFaultCampaignAudit,
      runFaultCampaignAcceptance,
      runReliabilityAcceptance,
      reliabilityGuardrailLabel,
      refreshPerformanceReport,
      onlineEvaluationStatusLabel,
      onlineEvaluationStageLabel,
      refreshOnlineEvaluationAudit,
      runOnlineEvaluationAcceptance,
      refreshFailurePool,
      runFailurePoolAcceptance,
      failureProposalFor,
      failureWhereLabel,
      failureWhyLabel,
      failureCandidateLabel,
      failureTargetLabel,
      failureGuardrailLabel,
      materializeEvolutionCandidates,
      evolutionArtifactLabel,
      evolutionGuardrailLabel,
      runEvolutionSplitAcceptance,
      evolutionSplitLabel,
      evolutionSplitPurposeLabel,
      evolutionSplitAcceptanceLabel,
      runEvolutionComparison,
      evolutionVariantLabel,
      evolutionPromotionLabel,
      evolutionHoldoutReasonLabel,
      evolutionPromotionReasonLabel,
      signedPercent,
      submitDownvote,
      faultClassLabel,
      faultPhaseLabel,
      faultMetricValue,
      loadProductionAnomaly,
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

.interview-demo-toggle {
  padding: 8px 13px;
  border: 0;
  border-radius: 10px;
  color: #fff;
  background: linear-gradient(135deg, #db527d 0%, #7d55d8 100%);
  box-shadow: 0 5px 14px rgba(151, 69, 169, 0.2);
  cursor: pointer;
  font-size: 12px;
  font-weight: 800;
  white-space: nowrap;
}

.interview-demo-toggle:disabled {
  cursor: wait;
  opacity: 0.65;
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

.interview-demo-panel {
  margin: 12px 20px 0;
  padding: 18px;
  border: 1px solid rgba(102, 78, 180, 0.2);
  border-radius: 16px;
  background:
    radial-gradient(circle at 92% 4%, rgba(222, 82, 125, 0.11), transparent 30%),
    linear-gradient(145deg, #fbfaff 0%, #f3f7ff 55%, #eefaf7 100%);
  box-shadow: 0 12px 32px rgba(69, 66, 142, 0.12);
  color: #39445d;
}

.interview-demo-header,
.interview-demo-step-heading,
.interview-demo-actions,
.interview-demo-stack {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
}

.interview-demo-header,
.interview-demo-step-heading {
  justify-content: space-between;
}

.interview-demo-header > div,
.interview-demo-step-heading > div {
  display: grid;
  gap: 4px;
}

.interview-demo-header strong {
  color: #382f73;
  font-size: 18px;
}

.interview-demo-header small,
.interview-demo-step-heading small {
  color: #8664a7;
  font-size: 11px;
  font-weight: 900;
  letter-spacing: 0.08em;
}

.interview-demo-header span:not(.interview-demo-readonly) {
  color: #667087;
  font-size: 12px;
}

.interview-demo-readonly {
  padding: 6px 10px;
  border-radius: 999px;
  color: #176b4b;
  background: #dff5e9;
  font-size: 11px;
  font-weight: 900;
}

.interview-demo-proof-grid,
.interview-demo-three-column {
  display: grid;
  gap: 9px;
}

.interview-demo-proof-grid {
  grid-template-columns: repeat(4, minmax(140px, 1fr));
  margin: 14px 0 12px;
}

.interview-demo-proof-grid article,
.interview-demo-three-column > div {
  display: grid;
  gap: 4px;
  padding: 10px 12px;
  border: 1px solid rgba(81, 91, 158, 0.14);
  border-radius: 10px;
  background: rgba(255, 255, 255, 0.86);
}

.interview-demo-proof-grid strong {
  overflow-wrap: anywhere;
  color: #4b51a2;
  font-size: 14px;
}

.interview-demo-proof-grid span,
.interview-demo-three-column small {
  color: #737d93;
  font-size: 11px;
}

.interview-demo-loading,
.interview-demo-warning {
  margin: 10px 0;
  padding: 8px 10px;
  border-radius: 8px;
  font-size: 12px;
}

.interview-demo-loading {
  color: #4d5d9b;
  background: #eef1ff;
}

.interview-demo-warning {
  color: #8a5b13;
  background: #fff4d9;
}

.interview-demo-tabs {
  display: flex;
  gap: 7px;
  margin: 12px 0;
  overflow-x: auto;
  padding-bottom: 3px;
}

.interview-demo-tabs button {
  flex: 1 0 auto;
  min-width: 112px;
  padding: 7px 10px;
  border: 1px solid rgba(89, 75, 166, 0.2);
  border-radius: 999px;
  color: #615a8c;
  background: #fff;
  cursor: pointer;
  font-size: 11px;
  font-weight: 800;
}

.interview-demo-tabs button.active {
  border-color: transparent;
  color: #fff;
  background: linear-gradient(135deg, #6954c5 0%, #3c8bb5 100%);
}

.interview-demo-step-card {
  padding: 15px;
  border: 1px solid rgba(83, 75, 168, 0.18);
  border-radius: 12px;
  background: rgba(255, 255, 255, 0.88);
}

.interview-demo-step-heading strong {
  color: #34306c;
  font-size: 16px;
}

.interview-demo-step-heading > span {
  padding: 5px 9px;
  border-radius: 999px;
  color: #3d6894;
  background: #e7f2ff;
  font-size: 11px;
  font-weight: 800;
}

.interview-demo-thesis {
  margin: 11px 0;
  color: #48546e;
  font-size: 13px;
  line-height: 1.7;
}

.interview-demo-three-column {
  grid-template-columns: repeat(3, minmax(0, 1fr));
}

.interview-demo-three-column strong {
  color: #4b536c;
  font-size: 12px;
  font-weight: 600;
  line-height: 1.55;
}

.interview-question-card {
  margin-top: 11px;
  padding: 9px 11px;
  border-left: 4px solid #8b64cb;
  border-radius: 8px;
  background: #f6f1ff;
  font-size: 12px;
}

.interview-question-card summary {
  color: #59449b;
  cursor: pointer;
  font-weight: 800;
}

.interview-question-card p {
  margin: 8px 0 1px;
  color: #566078;
  line-height: 1.65;
}

.interview-demo-actions {
  justify-content: center;
  margin-top: 12px;
}

.interview-demo-actions button {
  padding: 7px 12px;
  border: 1px solid rgba(89, 75, 166, 0.25);
  border-radius: 8px;
  color: #5c54a0;
  background: #fff;
  cursor: pointer;
  font-size: 12px;
  font-weight: 800;
}

.interview-demo-actions button:disabled {
  cursor: not-allowed;
  opacity: 0.45;
}

.interview-demo-actions .interview-demo-open-workspace {
  border: 0;
  color: #fff;
  background: linear-gradient(135deg, #6b55c7 0%, #3c91aa 100%);
}

.interview-demo-stack {
  justify-content: center;
  margin-top: 12px;
}

.interview-demo-stack span {
  padding: 4px 8px;
  border-radius: 6px;
  color: #657087;
  background: rgba(255, 255, 255, 0.78);
  font-size: 10px;
  font-weight: 700;
}

@media (max-width: 980px) {
  .interview-demo-proof-grid,
  .interview-demo-three-column {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 620px) {
  .interview-demo-panel {
    margin: 8px 10px 0;
    padding: 12px;
  }

  .interview-demo-proof-grid,
  .interview-demo-three-column {
    grid-template-columns: 1fr;
  }
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

.evaluation-candidate-warning.passed {
  color: #176246;
  background: #eaf8f1;
  border-color: #9bd8bb;
}

.evaluation-candidate-warning.passed span {
  color: #2d7058;
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

.human-review-toggle {
  padding: 8px 12px;
  border: none;
  border-radius: 10px;
  background: linear-gradient(135deg, #16825d 0%, #2a9d8f 100%);
  color: #fff;
  font-size: 12px;
  font-weight: 700;
  cursor: pointer;
  white-space: nowrap;
  box-shadow: 0 5px 14px rgba(31, 143, 109, 0.18);
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

.paired-comparison-card {
  padding: 10px;
  border: 1px dashed rgba(73, 93, 190, 0.34);
  border-radius: 9px;
  background: #f8f9ff;
}

.paired-comparison-card > summary {
  cursor: pointer;
  color: #4654a7;
  font-weight: 800;
}

.paired-comparison-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(360px, 1fr));
  gap: 10px;
  margin: 10px 0;
}

.paired-comparison-grid > article {
  display: grid;
  gap: 8px;
  min-width: 0;
  padding: 10px;
  border: 1px solid rgba(73, 93, 190, 0.18);
  border-radius: 8px;
  background: #fff;
}

.judge-calibration-card {
  padding: 10px;
  border: 1px dashed rgba(34, 139, 119, 0.34);
  border-radius: 9px;
  background: #f5fcfa;
}

.judge-calibration-card > summary {
  cursor: pointer;
  color: #247d6b;
  font-weight: 800;
}

.judge-calibration-summary {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: 8px;
  margin: 10px 0;
}

.judge-calibration-summary > div {
  display: grid;
  gap: 3px;
  min-width: 0;
  padding: 10px;
  border: 1px solid rgba(34, 139, 119, 0.16);
  border-radius: 8px;
  background: #fff;
}

.judge-calibration-summary strong {
  color: #2b4f87;
  font-size: 18px;
}

.judge-calibration-summary span,
.judge-calibration-actions small,
.judge-score-comparison small {
  color: #65718b;
  font-size: 12px;
}

.judge-calibration-case {
  min-width: 0;
  padding: 12px;
  border: 1px solid rgba(34, 139, 119, 0.2);
  border-radius: 9px;
  background: #fff;
}

.judge-calibration-nav,
.judge-calibration-actions,
.judge-case-index,
.judge-score-comparison {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 7px;
}

.judge-calibration-content {
  display: grid;
  gap: 7px;
  margin: 10px 0;
  padding: 10px;
  border-radius: 8px;
  background: #f8fafc;
  color: #3f4d63;
  line-height: 1.55;
  overflow-wrap: anywhere;
}

.judge-calibration-content p {
  margin: 0;
}

.judge-calibration-content > div,
.judge-calibration-content > div span {
  display: block;
}

.judge-score-form {
  display: grid;
  grid-template-columns: repeat(5, minmax(120px, 1fr));
  gap: 8px;
}

.judge-score-form label {
  display: grid;
  gap: 4px;
  color: #526078;
  font-size: 12px;
  font-weight: 700;
}

.judge-score-form select {
  min-width: 0;
  padding: 8px;
  border: 1px solid rgba(73, 93, 190, 0.24);
  border-radius: 7px;
  background: #fff;
  color: #34415a;
}

.judge-calibration-actions {
  justify-content: space-between;
  margin-top: 10px;
}

.judge-score-comparison {
  margin-top: 10px;
  padding: 8px;
  border-radius: 7px;
  background: #eef8f5;
  color: #346b5f;
  font-size: 12px;
}

.judge-score-comparison small {
  flex-basis: 100%;
}

.judge-case-index {
  margin: 10px 0;
}

.judge-case-index button {
  width: 34px;
  min-height: 30px;
  padding: 4px;
  border: 1px solid rgba(73, 93, 190, 0.24);
  border-radius: 6px;
  background: #fff;
  color: #526078;
}

.judge-case-index button.reviewed {
  border-color: #35a17f;
  background: #e9f8f2;
  color: #21765f;
}

.judge-case-index button.active {
  outline: 2px solid #586de2;
  outline-offset: 1px;
}

.interview-evidence-card,
.cleanup-audit-card {
  padding: 10px;
  border: 1px dashed rgba(70, 84, 167, 0.34);
  border-radius: 9px;
  background: #f8f9ff;
}

.interview-evidence-card > summary,
.cleanup-audit-card > summary {
  cursor: pointer;
  color: #4654a7;
  font-weight: 800;
}

.interview-evidence-actions,
.interview-evidence-heading,
.interview-evidence-metrics {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 7px;
}

.interview-evidence-summary {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 8px;
  margin: 10px 0;
}

.interview-evidence-summary > div {
  display: grid;
  gap: 3px;
  min-width: 0;
  padding: 10px;
  border: 1px solid rgba(70, 84, 167, 0.15);
  border-radius: 8px;
  background: #fff;
  overflow-wrap: anywhere;
}

.interview-evidence-summary strong {
  color: #3c4fa5;
  font-size: 17px;
}

.interview-evidence-summary span,
.interview-evidence-grid small {
  color: #65718b;
  font-size: 12px;
}

.interview-evidence-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(320px, 1fr));
  gap: 9px;
}

.interview-evidence-grid > article {
  display: grid;
  align-content: start;
  gap: 7px;
  min-width: 0;
  padding: 11px;
  border: 1px solid rgba(70, 84, 167, 0.16);
  border-radius: 8px;
  background: #fff;
  overflow-wrap: anywhere;
}

.interview-evidence-grid p {
  margin: 0;
  color: #3f4d63;
  line-height: 1.5;
}

.interview-evidence-heading {
  justify-content: space-between;
}

.interview-evidence-heading > span {
  padding: 3px 7px;
  border-radius: 999px;
  font-size: 11px;
  font-weight: 800;
}

.interview-evidence-heading > span.ready {
  background: #e7f8ef;
  color: #237c5d;
}

.interview-evidence-heading > span.blocked {
  background: #fff3d8;
  color: #9a6a16;
}

.interview-evidence-heading > span.pending {
  background: #eef1ff;
  color: #5968a8;
}

.g10-review-card {
  border-style: solid;
  border-color: rgba(126, 76, 170, 0.28);
  background: linear-gradient(145deg, #fbf9ff 0%, #f5f8ff 100%);
}

.g10-review-card .interview-evidence-summary {
  grid-template-columns: repeat(4, minmax(0, 1fr));
}

.g10-human-progress {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr)) auto;
  align-items: stretch;
  gap: 9px;
  margin-bottom: 10px;
}

.g10-human-progress article {
  display: grid;
  gap: 6px;
  padding: 10px;
  border: 1px solid rgba(42, 157, 143, 0.24);
  border-radius: 8px;
  background: #f3fcf8;
}

.g10-human-progress article > div {
  display: grid;
  gap: 3px;
}

.g10-human-progress .g10-seal-progress,
.g10-human-progress .g10-rerun-progress {
  margin-top: 2px;
  padding-top: 7px;
  border-top: 1px dashed rgba(42, 157, 143, 0.32);
}

.g10-seal-progress strong,
.g10-rerun-progress strong {
  color: #315f55;
  font-size: 11px;
}

.g10-human-progress span,
.g10-human-progress small {
  color: #5c746d;
  font-size: 11px;
  line-height: 1.45;
}

.g10-human-progress button {
  padding: 8px 12px;
  border: 0;
  border-radius: 8px;
  background: #238d71;
  color: #fff;
  font-size: 11px;
  font-weight: 800;
}

.g10-gate-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 9px;
}

.g10-gate-grid > article,
.g10-fact-list > article {
  display: grid;
  gap: 6px;
  min-width: 0;
  padding: 10px;
  border: 1px solid rgba(70, 84, 167, 0.16);
  border-radius: 8px;
  background: #fff;
  overflow-wrap: anywhere;
}

.g10-gate-grid p,
.g10-fact-list p {
  margin: 0;
  color: #3f4d63;
  font-size: 12px;
  line-height: 1.55;
}

.g10-gate-grid small,
.g10-fact-list small {
  color: #68748c;
  font-size: 11px;
  line-height: 1.5;
}

.g10-fact-list {
  margin-top: 10px;
  color: #5d518d;
  font-size: 12px;
}

.g10-fact-list > summary {
  margin-bottom: 7px;
  cursor: pointer;
  font-weight: 800;
}

.g10-fact-list > article + article {
  margin-top: 7px;
}

.g10-fact-list.excluded > article {
  border-color: rgba(197, 132, 36, 0.22);
  background: #fffaf0;
}

.g10-fact-choice {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  color: #33446b;
  cursor: pointer;
}

.g10-fact-choice input,
.g10-qualifier-ack input {
  flex: 0 0 auto;
  margin-top: 2px;
  accent-color: #6950c8;
}

.g10-confirmation-panel {
  display: grid;
  grid-template-columns: minmax(260px, 1fr) minmax(320px, 1.4fr) auto;
  align-items: center;
  gap: 10px;
  margin-top: 10px;
  padding: 11px;
  border: 1px solid rgba(91, 72, 181, 0.25);
  border-radius: 9px;
  background: #f3f1ff;
  color: #4d4772;
}

.g10-confirmation-panel > div {
  display: grid;
  gap: 4px;
}

.g10-confirmation-panel span,
.g10-qualifier-ack {
  font-size: 11px;
  line-height: 1.45;
}

.g10-qualifier-ack {
  display: flex;
  align-items: flex-start;
  gap: 7px;
  cursor: pointer;
}

.g10-confirmation-panel button {
  min-height: 34px;
  padding: 7px 11px;
  border: 0;
  border-radius: 7px;
  background: #6547bd;
  color: #fff;
  font-size: 11px;
  font-weight: 800;
  cursor: pointer;
}

.g10-confirmation-panel button:disabled {
  background: #b7b2ca;
  cursor: not-allowed;
}

.interview-evidence-metrics span {
  padding: 4px 7px;
  border-radius: 6px;
  background: #eef2ff;
  color: #46547d;
  font-size: 11px;
}

.interview-evidence-grid details {
  color: #765b91;
  font-size: 12px;
}

.interview-evidence-grid details summary {
  cursor: pointer;
  font-weight: 700;
}

.interview-evidence-grid ul {
  margin: 6px 0 0;
  padding-left: 18px;
}

.cleanup-candidate-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
  gap: 8px;
  margin: 10px 0;
}

.cleanup-candidate-grid > article {
  display: grid;
  gap: 6px;
  min-width: 0;
  padding: 10px;
  border: 1px solid rgba(70, 84, 167, 0.16);
  border-radius: 8px;
  background: #fff;
  color: #46547d;
  overflow-wrap: anywhere;
}

.cleanup-candidate-grid small {
  color: #65718b;
}

@media (max-width: 900px) {
  .judge-score-form {
    grid-template-columns: repeat(2, minmax(140px, 1fr));
  }

  .g10-review-card .interview-evidence-summary,
  .g10-gate-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .interview-evidence-summary {
    grid-template-columns: 1fr;
  }

  .g10-confirmation-panel {
    grid-template-columns: 1fr;
    align-items: stretch;
  }
}

@media (max-width: 560px) {
  .judge-score-form {
    grid-template-columns: 1fr;
  }

  .g10-review-card .interview-evidence-summary,
  .g10-gate-grid,
  .g10-human-progress {
    grid-template-columns: 1fr;
  }

  .judge-calibration-actions {
    align-items: stretch;
    flex-direction: column;
  }

  .interview-evidence-grid {
    grid-template-columns: 1fr;
  }
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

.anomaly-scenario-actions .production-window-button {
  border-color: rgba(42, 137, 118, 0.45);
  background: #e9f9f4;
  color: #267d6b;
}

.production-anomaly-card {
  display: grid;
  gap: 9px;
  margin-top: 9px;
  padding: 10px;
  border: 1px solid rgba(42, 137, 118, 0.3);
  border-radius: 8px;
  background: #f4fcf9;
}

.production-anomaly-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(290px, 1fr));
  gap: 8px;
}

.production-anomaly-grid > article {
  padding: 9px;
  border: 1px solid rgba(64, 90, 125, 0.14);
  border-radius: 8px;
  background: #fff;
}

.webhook-audit-card {
  margin-top: 9px;
  padding: 9px;
  border: 1px dashed rgba(43, 123, 157, 0.36);
  border-radius: 8px;
  background: #f1f9fc;
}

.webhook-audit-card > summary {
  cursor: pointer;
  color: #276d89;
  font-weight: 800;
}

.webhook-audit-card p {
  margin: 6px 0;
  color: #69758c;
  font-size: 12px;
}

.webhook-audit-card button {
  padding: 6px 9px;
  border: 1px solid rgba(43, 123, 157, 0.35);
  border-radius: 7px;
  background: #fff;
  color: #276d89;
  cursor: pointer;
  font-weight: 700;
}

.webhook-delivery-list {
  display: grid;
  gap: 7px;
  margin: 8px 0;
}

.webhook-delivery-list article {
  display: grid;
  gap: 4px;
  padding: 8px;
  border: 1px solid rgba(43, 123, 157, 0.16);
  border-radius: 7px;
  background: #fff;
}

.webhook-delivery-list article > div {
  display: flex;
  justify-content: space-between;
  gap: 8px;
}

.webhook-delivery-list span,
.webhook-delivery-list small {
  color: #69758c;
  font-size: 12px;
}

.controller-audit-card {
  margin-top: 9px;
  padding: 9px;
  border: 1px dashed rgba(126, 91, 30, 0.38);
  border-radius: 8px;
  background: #fffaf0;
}

.controller-audit-card > summary {
  cursor: pointer;
  color: #875f19;
  font-weight: 800;
}

.controller-audit-card p {
  margin: 6px 0;
  color: #69758c;
  font-size: 12px;
}

.controller-audit-card button {
  padding: 6px 9px;
  border: 1px solid rgba(126, 91, 30, 0.35);
  border-radius: 7px;
  background: #fff;
  color: #875f19;
  cursor: pointer;
  font-weight: 700;
}

.controller-acceptance-result {
  display: grid;
  gap: 8px;
  margin: 8px 0;
  padding: 9px;
  border: 1px solid rgba(126, 91, 30, 0.2);
  border-radius: 8px;
  background: #fff;
}

.controller-decision-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
  gap: 8px;
}

.controller-decision-grid > div,
.controller-decision-list article {
  display: grid;
  gap: 4px;
  padding: 8px;
  border: 1px solid rgba(126, 91, 30, 0.14);
  border-radius: 7px;
  background: #fff;
}

.controller-decision-grid span,
.controller-decision-grid small,
.controller-decision-list span,
.controller-decision-list small {
  color: #69758c;
  font-size: 12px;
}

.controller-decision-list {
  display: grid;
  gap: 7px;
  margin: 8px 0;
}

.controller-decision-list article > div {
  display: flex;
  justify-content: space-between;
  gap: 8px;
}

.fault-campaign-card {
  margin-top: 9px;
  padding: 9px;
  border: 1px dashed rgba(81, 94, 180, 0.42);
  border-radius: 8px;
  background: #f7f8ff;
}

.fault-campaign-card > summary {
  cursor: pointer;
  color: #4d57a8;
  font-weight: 800;
}

.reliability-acceptance-card {
  margin-top: 10px;
  padding: 9px;
  border: 1px dashed rgba(35, 142, 151, 0.38);
  border-radius: 8px;
  background: #f4feff;
}

.reliability-acceptance-card > summary {
  cursor: pointer;
  color: #177b83;
  font-weight: 800;
}

.reliability-result-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(360px, 1fr));
  gap: 9px;
  margin: 9px 0;
}

.reliability-result-grid > article {
  display: grid;
  gap: 8px;
  padding: 10px;
  border: 1px solid rgba(35, 142, 151, 0.2);
  border-radius: 8px;
  background: #fff;
}

.reliability-result-grid > article.passed { border-color: rgba(36, 154, 98, 0.4); }
.reliability-result-grid > article.failed { border-color: rgba(207, 72, 72, 0.45); }

.performance-phase-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
}

.performance-phase-grid > article {
  min-width: 0;
  padding: 12px;
  border: 1px solid rgba(86, 93, 214, 0.2);
  border-radius: 12px;
  background: rgba(255, 255, 255, 0.72);
}

.performance-runtime-strip {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin: 10px 0;
}

.performance-runtime-strip span {
  padding: 5px 9px;
  border-radius: 999px;
  background: rgba(74, 144, 226, 0.1);
}

.performance-route-strip {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin: 8px 0;
  font-size: 12px;
  color: #53627d;
}

.performance-route-strip span {
  padding: 3px 7px;
  border-radius: 7px;
  background: rgba(87, 100, 206, 0.08);
}

.performance-limitations {
  display: grid;
  gap: 6px;
  padding: 10px 0;
}

@media (max-width: 900px) {
  .performance-phase-grid { grid-template-columns: 1fr; }
}

.fault-campaign-card p {
  margin: 6px 0;
  color: #69758c;
  font-size: 12px;
}

.fault-campaign-card button {
  padding: 6px 9px;
  border: 1px solid rgba(81, 94, 180, 0.35);
  border-radius: 7px;
  background: #fff;
  color: #4d57a8;
  cursor: pointer;
  font-weight: 700;
}

.online-evaluation-card {
  margin-top: 9px;
  padding: 9px;
  border: 1px dashed rgba(34, 139, 117, 0.44);
  border-radius: 8px;
  background: #f1fbf8;
}

.online-evaluation-card > summary {
  cursor: pointer;
  color: #1d745f;
  font-weight: 800;
}

.online-evaluation-card p,
.online-evaluation-card small {
  color: #61766f;
  font-size: 12px;
}

.online-evaluation-card button {
  padding: 6px 9px;
  border: 1px solid rgba(34, 139, 117, 0.38);
  border-radius: 7px;
  background: #fff;
  color: #1d745f;
  cursor: pointer;
  font-weight: 700;
}

.online-evaluation-card button:disabled {
  cursor: wait;
  opacity: 0.55;
}

.online-evaluation-rates {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(130px, 1fr));
  gap: 8px;
  margin: 9px 0;
}

.online-evaluation-rates > div {
  display: grid;
  gap: 3px;
  padding: 9px;
  border: 1px solid rgba(34, 139, 117, 0.16);
  border-radius: 7px;
  background: #fff;
}

.online-evaluation-rates strong {
  color: #1d745f;
  font-size: 17px;
}

.online-evaluation-rates span,
.online-score-grid span,
.online-case-grid span {
  color: #61766f;
  font-size: 12px;
}

.online-evaluation-latest,
.online-evaluation-acceptance {
  display: grid;
  gap: 8px;
  margin-top: 9px;
  padding: 9px;
  border: 1px solid rgba(34, 139, 117, 0.18);
  border-radius: 8px;
  background: #fff;
}

.online-evaluation-acceptance.failed {
  border-color: #e3b65e;
  background: #fffaf0;
}

.online-score-grid,
.online-stage-list {
  display: flex;
  flex-wrap: wrap;
  gap: 7px;
}

.online-score-grid span {
  padding: 4px 7px;
  border-radius: 999px;
  background: #e4f6ef;
}

.online-case-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(210px, 1fr));
  gap: 7px;
}

.online-case-grid article {
  display: grid;
  gap: 3px;
  padding: 8px;
  border: 1px solid rgba(34, 139, 117, 0.14);
  border-radius: 7px;
  background: #fbfffd;
}

.failure-pool-card {
  margin-top: 10px;
  padding: 9px;
  border: 1px dashed rgba(120, 84, 184, 0.38);
  border-radius: 8px;
  background: #faf7ff;
}

.failure-pool-card > summary {
  cursor: pointer;
  color: #6947a6;
  font-weight: 800;
}

.evolution-lineage-card {
  background: #f5fbff;
  border-color: rgba(48, 132, 168, 0.34);
}

.evolution-artifact-grid > article {
  border-color: rgba(48, 132, 168, 0.22);
}

.evolution-limitation {
  display: block;
  margin-top: 5px;
  color: #786548;
}

.evolution-split-card {
  margin-top: 10px;
  padding: 9px;
  border: 1px solid rgba(48, 132, 168, 0.2);
  border-radius: 8px;
  background: rgba(255, 255, 255, 0.72);
}

.evolution-split-card > summary {
  cursor: pointer;
  color: #287493;
  font-weight: 800;
}

.evolution-split-grid {
  grid-template-columns: repeat(auto-fit, minmax(230px, 1fr));
}

.evolution-comparison-card {
  background: #fffdf8;
  border-color: rgba(185, 130, 50, 0.28);
}

.evolution-candidate-summary,
.evolution-comparison-split {
  margin-top: 8px;
  padding: 9px;
  border: 1px solid rgba(185, 130, 50, 0.2);
  border-radius: 8px;
  background: #fff;
}

.evolution-candidate-summary p {
  margin: 5px 0;
  overflow-wrap: anywhere;
}

.evolution-variant-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 7px;
  margin-top: 8px;
}

.evolution-variant-grid > div,
.evolution-paired-list > div {
  display: grid;
  gap: 3px;
  padding: 7px;
  border-radius: 7px;
  background: #f8fafc;
}

.evolution-paired-list {
  display: grid;
  gap: 6px;
  margin-top: 8px;
}

.evolution-promotion-gate {
  margin-top: 10px;
  padding: 10px;
  border: 1px solid rgba(180, 83, 9, 0.25);
  border-radius: 8px;
  background: #fffaf0;
}

.evolution-promotion-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 7px;
}

.evolution-promotion-attempt {
  display: grid;
  grid-template-columns: minmax(120px, auto) minmax(180px, 1fr);
  gap: 4px 10px;
  margin-top: 7px;
  padding: 8px;
  border-radius: 7px;
  background: #fff;
}

.evolution-promotion-attempt small {
  grid-column: 1 / -1;
  overflow-wrap: anywhere;
}

.evolution-control-acceptance {
  margin-top: 9px;
  padding: 9px;
  border: 1px solid rgba(13, 148, 136, 0.24);
  border-radius: 8px;
  background: #f0fdfa;
}

.evolution-control-acceptance > summary {
  cursor: pointer;
  color: #0f766e;
  font-weight: 800;
}

.evolution-shadow-control {
  margin-top: 9px;
  padding: 9px;
  border: 1px solid rgba(37, 99, 235, 0.24);
  border-radius: 8px;
  background: #eff6ff;
}

.evolution-shadow-control > summary {
  cursor: pointer;
  color: #1d4ed8;
  font-weight: 800;
}

.evolution-shadow-pointer {
  display: grid;
  gap: 4px;
  margin-top: 8px;
  padding: 8px;
  border: 1px solid rgba(37, 99, 235, 0.18);
  border-radius: 7px;
  background: #fff;
}

.failure-cluster-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
  gap: 8px;
  margin: 9px 0;
}

.failure-cluster-grid > article {
  display: grid;
  gap: 6px;
  padding: 9px;
  border: 1px solid rgba(120, 84, 184, 0.18);
  border-radius: 8px;
  background: #fff;
}

.failure-cluster-grid p {
  margin: 0;
}

.failure-proposal-line {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 7px;
}

.failure-proposal-line span {
  padding: 3px 7px;
  border-radius: 999px;
  background: #eee5fb;
  color: #6947a6;
  font-size: 11px;
}

.failure-acceptance-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(290px, 1fr));
  gap: 6px;
}

.review-manifest-card {
  display: grid;
  gap: 9px;
  margin: 10px 0;
  padding: 10px;
  border: 1px solid rgba(58, 132, 101, 0.24);
  border-radius: 9px;
  background: #f7fffb;
}

.review-fixture-list > summary {
  cursor: pointer;
  color: #346d58;
  font-weight: 700;
}

.review-fixture-list > div {
  margin-top: 8px;
}

.catalog-review-entry,
.catalog-review-entry > div,
.catalog-review-case-heading,
.catalog-review-case-heading > div,
.catalog-review-pagination {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.catalog-review-entry > div,
.catalog-review-case-heading > div {
  align-items: flex-start;
  flex-direction: column;
  gap: 2px;
}

.catalog-review-entry span,
.catalog-review-case-heading span,
.catalog-review-boundary,
.catalog-review-history {
  color: #64738a;
  font-size: 12px;
}

.catalog-review-entry button,
.catalog-review-pagination button,
.catalog-review-submit {
  padding: 7px 10px;
  border: 1px solid rgba(52, 109, 88, 0.3);
  border-radius: 7px;
  background: #fff;
  color: #346d58;
  cursor: pointer;
  font-weight: 700;
}

.catalog-review-entry button:disabled,
.catalog-review-pagination button:disabled,
.catalog-review-submit:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}

.catalog-review-entry-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 7px;
}

.catalog-review-workbench,
.catalog-review-body,
.catalog-review-case {
  display: grid;
  gap: 9px;
}

.catalog-review-workbench {
  padding: 10px;
  border: 1px dashed rgba(52, 109, 88, 0.35);
  border-radius: 8px;
  background: #f0faf6;
}

.catalog-review-filters,
.catalog-review-decision {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
}

.catalog-review-filters label {
  display: flex;
  align-items: center;
  gap: 5px;
  color: #52657d;
  font-size: 12px;
  font-weight: 700;
}

.catalog-review-filters select,
.catalog-review-decision select {
  padding: 6px 8px;
  border: 1px solid rgba(52, 109, 88, 0.25);
  border-radius: 6px;
  background: #fff;
}

.catalog-review-filters > span {
  margin-left: auto;
  color: #64738a;
  font-size: 12px;
}

.catalog-review-case {
  padding: 10px;
  border: 1px solid rgba(52, 109, 88, 0.22);
  border-radius: 8px;
  background: #fff;
}

.catalog-review-prompt {
  margin: 0;
  color: #26384c;
  font-size: 14px;
  font-weight: 700;
  line-height: 1.55;
}

.catalog-review-payload summary {
  cursor: pointer;
  color: #346d58;
  font-size: 12px;
  font-weight: 700;
}

.catalog-review-payload pre {
  max-height: 280px;
  margin: 7px 0 0;
  padding: 9px;
  overflow: auto;
  border-radius: 7px;
  background: #19222d;
  color: #dbe9e2;
  font: 12px/1.5 Consolas, "Courier New", monospace;
  white-space: pre-wrap;
  word-break: break-word;
}

.catalog-review-decision label,
.catalog-review-ack {
  display: flex;
  align-items: flex-start;
  gap: 5px;
  color: #40536a;
  font-size: 12px;
}

.catalog-review-submit {
  justify-self: start;
  background: #346d58;
  color: #fff;
}

.catalog-review-pagination {
  justify-content: flex-end;
}

.catalog-review-boundary {
  margin: 0;
  line-height: 1.5;
}

@media (max-width: 720px) {
  .catalog-review-entry,
  .catalog-review-case-heading {
    align-items: stretch;
    flex-direction: column;
  }

  .catalog-review-filters > span {
    width: 100%;
    margin-left: 0;
  }
}

.fault-campaign-result,
.fault-scenario-list,
.fault-scenario-list > article {
  display: grid;
  gap: 9px;
}

.fault-campaign-result { margin: 8px 0; }

.fault-campaign-summary {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(145px, 1fr));
  gap: 7px;
}

.fault-campaign-summary > div {
  display: grid;
  gap: 2px;
  padding: 8px;
  border: 1px solid rgba(81, 94, 180, 0.14);
  border-radius: 7px;
  background: #fff;
}

.fault-campaign-summary strong { color: #4652a4; font-size: 17px; }
.fault-campaign-summary span { color: #69758c; font-size: 11px; }

.fault-scenario-list > article {
  padding: 9px;
  border: 1px solid rgba(81, 94, 180, 0.18);
  border-radius: 8px;
  background: #fff;
}

.fault-scenario-heading,
.fault-scenario-heading > div,
.fault-campaign-limit {
  display: flex;
  justify-content: space-between;
  gap: 6px;
  flex-wrap: wrap;
}

.fault-scenario-heading > div,
.fault-campaign-limit { flex-direction: column; }

.fault-scenario-heading span,
.fault-scenario-metrics span,
.fault-campaign-limit span,
.fault-campaign-limit small {
  color: #69758c;
  font-size: 11px;
}

.fault-scenario-metrics {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}

.fault-scenario-metrics span {
  padding: 3px 6px;
  border-radius: 999px;
  background: #f0f2ff;
}

.fault-timeline {
  display: grid;
  grid-template-columns: repeat(6, minmax(150px, 1fr));
  gap: 6px;
  overflow-x: auto;
  padding-bottom: 4px;
}

.fault-timeline > div {
  display: grid;
  gap: 3px;
  min-width: 150px;
  padding: 7px;
  border-top: 3px solid #8790d5;
  border-radius: 6px;
  background: #f8f9ff;
}

.fault-timeline > div.phase-detected,
.fault-timeline > div.phase-injected { border-top-color: #d78932; background: #fff9ee; }
.fault-timeline > div.phase-recommendation { border-top-color: #7756c8; background: #f7f2ff; }
.fault-timeline > div.phase-recovered { border-top-color: #36a878; background: #f0fbf6; }
.fault-timeline span,
.fault-timeline small { color: #69758c; font-size: 10px; }

.fault-campaign-limit {
  padding: 8px;
  border: 1px solid #e9cf91;
  border-radius: 7px;
  background: #fff8df;
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

.prometheus-runtime-card {
  display: grid;
  gap: 7px;
  margin: 9px 0;
  padding: 9px;
  border: 1px solid rgba(52, 168, 121, 0.28);
  border-radius: 8px;
  background: #f0fbf6;
}

.prometheus-runtime-card.degraded,
.prometheus-runtime-card.warming,
.prometheus-runtime-card.version_mismatch {
  border-color: rgba(217, 137, 35, 0.35);
  background: #fff8ea;
}

.prometheus-runtime-card > p {
  margin: 0;
  color: #69758c;
  font-size: 12px;
}

.grafana-runtime-card {
  border-color: rgba(236, 126, 46, 0.32);
  background: linear-gradient(135deg, #fff7ee, #f5f3ff);
}

.grafana-dashboard-groups {
  display: grid;
  grid-template-columns: repeat(3, minmax(180px, 1fr));
  gap: 7px;
}

.grafana-dashboard-groups article {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 8px;
  border: 1px solid rgba(95, 78, 170, 0.16);
  border-radius: 7px;
  background: rgba(255, 255, 255, 0.72);
}

.grafana-dashboard-groups span {
  color: #69758c;
  font-size: 12px;
}

@media (max-width: 900px) {
  .grafana-dashboard-groups { grid-template-columns: 1fr; }
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

.answer-feedback-btn {
  padding: 5px 10px;
  border: 1px solid rgba(239, 120, 107, 0.45);
  border-radius: 999px;
  background: rgba(255, 255, 255, 0.82);
  color: #a7483d;
  font-size: 12px;
  cursor: pointer;
}

.answer-feedback-btn:hover:not(:disabled) {
  background: #fff0ee;
}

.answer-feedback-btn.submitted {
  border-color: rgba(40, 167, 112, 0.42);
  background: rgba(40, 167, 112, 0.1);
  color: #18784c;
}

.answer-feedback-btn:disabled {
  cursor: default;
  opacity: 0.8;
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
