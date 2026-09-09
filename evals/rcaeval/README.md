# RCAEval 已知故障案例辅助排查实验

这是 GopherAI 的独立、只读实验，不是生产故障注入或自动修复系统。

## 来源与范围

- 官方数据：[RCAEval](https://huggingface.co/datasets/phamquiluan/RCAEval)，数据卡许可证 MIT；作者提供的微服务故障注入观测，不是客户真实事故。
- 固定 revision：`afeacb11bcc94dadfd1c8f483ee4377b2b8b614e`，子集 RE2-OB / Online Boutique。
- 限定服务：checkoutservice、currencyservice；支持类型：CPU 压力、内存压力、网络延迟。
- 参考库为这六个组合的第 1 次运行；开发集为第 2 次运行中的 6 个范围内 + 3 个范围外案例；留出集为第 3 次运行中的 6 个范围内 + 6 个范围外案例。
- 共 27 例、81 个原始 Parquet、277,840,487 bytes。原始文件只保存在本地缓存，不上传 GitHub 或 ECS；本目录 `sources.json` 保留原始路径、文件 SHA-256、字节数和分组。
- 下载器 `scripts/eval/prepare_rcaeval.py` 固定 revision、两路并发、1 GiB 原始数据上限。运行需要 Python、requests、numpy、pandas、pyarrow；在线后端没有 Python 依赖。

原始目录名包含答案，不能作为在线模型/匹配器输入。本地转换器分开写出观测、参考库和评分标签。特征函数只接收匿名 ID 与文件，不接收故障类型、正确服务或注入时间。

## 两条互相独立的链路

2026-09-09 新增真正调用云端模型的自主排查入口。原 A/B/C 规则及 `holdout.json` 不变；下文旧成绩属于规则版，不能当作新 Agent 的成绩。

### D：自主排查 Agent

```text
服务清单 + 只读工具契约 + 预算（不提供当前答案）
  → LLM 选择下一项工具与服务
  → Tool Runtime 校验并查询当前观测 / 历史参考
  → 实际新证据回到模型；保留简短假设变化与引用
  → 模型决定继续查询、修正假设或结束
  → Go 校验最终证据归属、候选范围与未验证边界
  → 独立评分器核对结果；失败不冒充正确拒答
```

单 Agent，最多8次模型请求、6次工具额度、180秒；不是预先固定工具顺序。每次调用记录 Tool Runtime 审计。可以探索其他服务作为排除项，但最终已知模式仍限定2个服务、3个类型。跨服务对照不能替代候选自身的详细指标及另一类证据。不会执行修复。

既有百炼轻量配置为 qwen-turbo 时，此入口默认使用 qwen-plus，其他聊天/RAG 不变。可在服务端设置 `GOPHERAI_RCA_MODEL`；需要既有 `OPENAI_API_KEY`、模型 BaseURL 配置及网络，不能离线伪造模型结果。

```bash
# 在配置已就绪的环境运行；输出必须是新路径，不覆盖旧结果。
go run ./cmd/rca-agent-eval -split development -output /path/to/new-agent-development.json
go run ./cmd/rca-agent-eval -split holdout -output /path/to/new-agent-replay.json
```

旧12例已经公开查看过答案，新增 Agent 的运行叫“已有案例回放”，不是新盲测。`agent-replay.json` 单独保存一次完整12例回放，包含错误、误接纳、模型调用、Token、每轮实际选择和证据。报告绑定数据、Prompt、执行器源码 Hash，版本失配不能展示旧成绩。执行器测试用替身模型只验证控制流，不计入质量分数。

开发运行也保留：`agent-development-first.json`、`agent-development-second.json`、`agent-development-turbo.json`、`agent-development-plus-initial.json`。包含时间顺序上的失败，不删坏结果、不给原有规则增益换名字。后续报告解释见部署经验记录与增量规格。

### A/B/C：原确定性规则链路

```text
匿名窗口 ID
  → 固定窗口聚合：最早/最晚三分之一，不使用 inject_time
  → Tool Runtime 读取 metrics
  → Tool Runtime 检索 6 条参考案例（只有 C 方案）
  → Tool Runtime 读取 logs / traces
  → 指标轮廓匹配、证据准入、候选排序（最多 3 个）
  → 当前依据 + 历史差异 + 尚未执行的验证建议
  → 诊断结束后，独立评分器读取官方标签
```

本版是确定性、有界的案例辅助诊断工作流，LLM 调用为 0；没有自由规划、递归创建 Agent 或拓扑因果推断。五个排名维度为 cpu、mem、latency-90、socket、workload，使用窗口中位数和 log2 变化。日志/调用链是真实补充观测，但不参与本版排名。

匹配器只导入观测与参考库。`internal/rcascoring` 独立持有答案，不能被 `internal/rcaexperiment` 导入。已知参考标签是允许的历史先验，留出标签不能回流。前端报告允许人工查看全部留出结果，因此它是可重放演示，不是对用户保密的考试。

参考库刻意采用只读嵌入 JSON，不新增数据库/向量基础设施，也不污染生产历史案例库。在线每次工具调用通过现有 Tool Runtime：限定 benchmark target 和 case_id、RBAC、Schema、1 MiB 返回体、单工具 1 秒、最多 4 次、请求总超时 60 秒；服务端同一时刻最多一个诊断请求。审计写入已有 MySQL ToolAudit，并记录 Trace。只读后续建议不冒充已经执行的核验。

## 三组对照与第一轮结果

开发集用于调参数，`policy-freeze.json` 在首次留出运行前生成。`holdout.json` 保留首次完整留出输出，不删失败、不挑最佳运行。

| 方案 | 范围内服务 Top-1 | 范围内服务+类型 | 范围外拒答 | 范围外误接纳 | 执行错误 |
| --- | --- | --- | --- | --- | --- |
| A 原文本错误规则 | 0/6 | 0/6 | 6/6 | 0/6 | 0 |
| B 观测特征规则 | 5/6 | 5/6 | 1/6 | 5/6 | 0 |
| C 历史案例增强 | 6/6 | 6/6 | 2/6 | 4/6 | 0 |

A 是原有文本规则缺少结构化遥测定位的边界对照，不是对所有传统 RCA 算法的结论。B/C 使用相同当前指标和资源优先规则，C 同时增加历史相似性和参考尺度准入；这是案例增强机制的整体对照，不是单独一种权重的严格消融。

关键案例：`rca-e5abfe8ebd`，官方标签 currencyservice / cpu。B 未正确定位，C 参考历史尺度后正确定位。用于解释为什么只按统一固定幅度规则可能遗漏某服务的故障。不要把这个一例增益称为统计显著优势。

反例：`rca-bba0bc25ab`，官方标签 checkoutservice / disk；C 误判成内存压力。`rca-90376d73d9`（checkoutservice / loss）则返回证据不足。页面同时保留两者，避免只演示成功例。

## 如何复现

Go 依赖就绪后，在仓库根目录运行；不需要 MySQL、Redis 或 LLM 服务：

```bash
go test ./internal/rcaexperiment ./internal/rcascoring ./controller/rcaexperiment
go run ./cmd/rca-eval -split development -output .codex-tmp/rca-dev-replay.json
go run ./cmd/rca-eval -split holdout -output .codex-tmp/rca-holdout-replay.json
```

保持已封存 `holdout.json` 不变，重放写新文件。时间、UUID Trace 等自然变化，匹配/评分应相同。报告绑定数据 Hash 和匹配器源码 Hash；API 遇到版本不一致会拒绝展示旧报告。`.gitattributes` 保留哈希工件字节，避免 Windows/Git 行尾转换使发布后报告失配。

文件索引：

- `internal/rcaexperiment/data/observations.json`：匿名、全服务有界观测。
- `internal/rcaexperiment/data/references.json`：6 条公开参考案例的标签和来源。
- `internal/rcascoring/data/answers.json`：独立评分真值。
- `development-frozen.json`：最终开发集运行结果。
- `policy-freeze.json`：首次留出前冻结信息。
- `holdout.json`：首次留出运行、逐例候选、引用、工具状态和评分。去除了可按 Hash 找回的重复遥测，保留诊断证据。
- `sources.json`：27 例的来源、分组和原始文件摘要；不能作为匹配器输入。

## 原规则版演示验收

1. 登录后打开 `/dashboard`，点击 M-10“自主排查实验”卡片（M-09之后），进入 `/dashboard/rca-experiment`。
2. 保持“原留出集回放”，选择 `rca-34a5398b52`，展开“规则对照”并运行案例增强诊断。预期首候选 checkoutservice / CPU 压力，有当前指标引用、参考案例、差异和未执行的确认建议；展开答案核对应正确。
3. 选择 `rca-e5abfe8ebd`，分别运行 B 与 C；观察前者未正确定位、后者定位 currencyservice / CPU 压力，不只比较文案。
4. 查看真实工具轨迹：C 共四次、成功状态、Trace 和 MySQL 审计。此处读取的是 RCAEval 快照，不是现有 ECS 的运行状态。
5. 运行 `rca-bba0bc25ab` 并核对答案，预期明确显示范围外误接纳；运行 `rca-90376d73d9`，预期证据不足。最后查看页面下方冻结对照表。

## 能说与不能说

可以说：GopherAI 在这两个服务、三类已知故障的小型公开实验上，能够基于当前观测与历史案例给出候选、引用和辅助排查建议，且工具调用受限、可审计。

不能说：生产准确率 100%、未知故障识别已解决、具备任意系统因果根因定位、自动修复有效，或这些是经过验证的真实事故处置案例。六个已知留出例来自同配方不同运行，范围外误接纳 4/6 是当前明显局限；需要扩大数据、独立评测及隔离环境修复验证后才可扩大能力声明。
