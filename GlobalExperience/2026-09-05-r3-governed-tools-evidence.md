# R3 受治理工具全生命周期证据包

## 1. 冻结结论

- 证据日期：2026-09-05
- 最终代码提交：`236f66ca9933f4ce9ece0580aaf68eae7b4045d1`
- 云端 Release：`20260905081443-236f66ca9933`
- 发布包 SHA-256：`7511f4637c605e9de7952f97a8f1e4e0579edcb1c39fe612618258c5c8083172`
- 分支：`add_eico`，已推送 `origin/add_eico`
- 30 条候选评测报告内容 SHA-256：`df073ce7de9374e48f207236b52e2aa39e46b812295d0df341f83430dc64e3ed`
- 结论：M6-01～M6-22 的 R3 技术范围已闭环，G6 技术门通过；评测标签仍为 `pending_user`，因此 `baseline_eligible=false`，不能把候选报告当作简历正式基线。

## 2. 统一边界

公开 ToolAgent 只能看到五个只读工具：

| 工具 | 真实用途 | 调用方不能控制的边界 |
|---|---|---|
| `deployment_manifest_lookup@1.0.0` | 当前发布清单 | 固定文件、64 KiB、字段白名单 |
| `service_health_snapshot@1.0.0` | Backend/Worker live/ready | 固定服务、地址与端口 |
| `bounded_log_signature@1.0.0` | 有界日志故障签名 | 固定日志、签名枚举、20 条/256 KiB、脱敏 |
| `mcp_deployment_evidence@1.0.0` | 回环 MCP 发布证据 | 固定 MCP 工具名、无调用参数、严格返回 Schema |
| `official_document_search@1.0.0` | Go/Redis/RabbitMQ/Prometheus 官网证据 | 固定文档 ID，不接收 URL/域名/路径 |

唯一写工具 `confirm_resolution@1.0.0` 不进入公共目录，只能由服务端在用户明确确认解决后，以 `internal_write` 权限调用；外部写工具始终拒绝。

所有候选调用都依次经过：精确 Registry → 严格 Schema → Intent/Permission/SideEffect → 调用预算 → timeout/cancel → 幂等重试/熔断/cache/stale → 输出上限 → 脱敏审计 → 低基数指标。Planner 和 MCP Adapter 都没有旁路执行权。

## 3. 生命周期证据矩阵

| 能力 | 生产代码/契约证据 | 确定性证据 | 真实云端证据 | 结果 |
|---|---|---|---|---|
| 工具选择 | 最多 2 步、普通问题零调用、危险问题拒绝 | 30 条集 selection 100% | Manifest + Health、官网文档 + Health 组合计划成功 | 通过 |
| Schema | 未知字段、错类型、超长、枚举外值 fail-closed | Schema 100%；错参最多修复 2 次 | 官网工具只接受 `document_id/query`，不接受 URL | 通过 |
| 授权/副作用 | 权限、Intent、副作用均由服务端构造 | Authorization 100%，危险执行率 0% | 公共目录只有 read-only；确认解决才执行内部写 | 通过 |
| 超时/取消 | 所有 attempt 共用父 Context 总预算 | timeout/cancel 用例通过 | 官网抓取出现 1 次真实 3500ms `TOOL_TIMEOUT`，后续新调用成功 | 通过 |
| 幂等重试 | 仅 `idempotent + retryable` 可重试 | 临时失败/非幂等零误重试用例通过 | 官网超时与成功都留下独立 ToolMessage/审计/指标 | 通过 |
| 熔断 | closed/open/half-open 单探针状态机 | 可控时钟迁移与 Race 通过 | 故障演练恢复后 circuit 为 closed | 通过 |
| Cache/stale | principal 隔离；先治理后读 cache；stale 有显式原因 | TTL、隔离、窗口外失败、窗口内 stale 全通过 | 短停 MCP 后 stale fallback，恢复后重新 fresh | 通过 |
| HITL/幂等写 | Proposal/Confirm 后才进入原子事务 | 只读/外部写拒绝、重复确认单效果 | 页面确认案例后 `confirmed → indexed`，审计 strategy 为 `human_confirmed_action_v1` | 通过 |
| 错名/重复动作 | 未知工具不猜；ActionGuard 使用 canonical args hash | 修复门与无进展门均 100% | 页面公开预算与被裁掉候选动作数 | 通过 |
| 审计/指标 | 原始参数、输出、principal 不持久化；固定标签 | 审计覆盖/确定性重放 100% | Args/User hash 64、Trace 36；MySQL 跨发布，Prometheus 按进程 | 通过 |

## 4. 官方文档 Search/Fetch 的真实证据

固定 allowlist 为 Go Context cancellation、Redis ACL、RabbitMQ DLX、Prometheus alerting。生产 HTTP Client：

- 禁用环境代理，只允许 HTTPS；只允许同 host 最多 3 次重定向。
- 每次连接前解析 DNS，并拒绝 loopback、私网、链路本地、组播、测试网段等目标，避免 DNS rebinding/SSRF。
- 仅接受 HTML/纯文本，解压后最大 256 KiB，最多 5 个、每个 360 rune 的可见文本摘录。
- HTML 会丢弃 script/style/svg/noscript/template；输出规范 URL、抓取源 host、正文 SHA-256、字节数、匹配数和 evidence ref。
- RabbitMQ 官网从当前阿里云环境返回 403，因此抓取源固定为 RabbitMQ 官方网站仓库的版本化原文，输出的规范 URL 仍是 `https://www.rabbitmq.com/docs/dlx`；调用方不能替换二者。

首个真实四文档批次全部成功，耗时约 922～1339ms。最终 Release 再次调用 Redis ACL，耗时 1333ms、5 个命中、169843 字节，MySQL 审计成功；Prometheus 为 accepted=1、success=1、miss=1、circuit closed=1。

实现后云端核查发现该新工具遗漏在 Prometheus 工具名白名单中，调用被折叠为 `tool="unknown"`。提交 `48623166` 修复并增加全生产工具标签回归测试，随后最终 Release 已证明 `tool="official_document_search"` 独立可见。这是一次“线上观测发现缺口 → 修复 → 重新发布验证”的小闭环。

## 5. 旧 Skill 停流与审计

天气、日期、计算器、翻译、摘要、RAG Skill、递归 Agent Skill、普通用户 Skill API/UI 已在用户明确授权后，由基线提交 `a4bf5146` 物理删除；本阶段不为形式上的开关门禁复活无场景代码。

提交 `236f66ca` 增加退役哨兵：`/api/v1/skill` 及其子路径始终返回 HTTP 410、`LEGACY_SKILL_RETIRED` 和替代入口 `/api/v1/tools/catalog`，并只记录固定标签 `gopherai_legacy_entry_attempts_total{entry="skill_api"}`。云端从 0 发送一次受控探针后变为 1，没有执行旧 Skill。恢复方案是 Git/发布包回退，不是在线恢复废弃能力。

## 6. 评测口径与限制

30 条候选集的工具选择、Schema、授权、韧性、安全、审计、确定性重放、错参有界修复、重复动作终止均为 100%；危险动作执行率 0%，未知工具执行 0 次，P95 仅代表本地 Fixture 契约执行耗时。

必须同时说明：

- 30 条标签尚未由用户逐条复核，不能生成正式 baseline。
- Fixture 证明状态机和治理契约，不等于真实公网、MySQL 或容器故障注入。
- 唯一线上实例没有为展示 open circuit 而连续破坏服务；真实故障演练只短停可恢复的回环 MCP。
- ToolAgent 是有限候选计划，不是获得 Shell、SQL 写、部署或容器重启权限的自主运维 Agent。

## 7. 构建与发布门

- `go test ./...`、`go vet ./...` 通过。
- Tool Runtime、ToolAgent、Evaluation、Controller、Observability、Router 的定向 Race 通过。
- Vue production build 通过；只有既有 bundle size/browser database 提示。
- Backend、Index Worker live/ready、MCP、Vue 编译/HTTP、四个唯一进程全部通过。
- 最终 ready 组件：MySQL、RabbitMQ、Redis cache、Redis vector、Model Config 全部 up。

