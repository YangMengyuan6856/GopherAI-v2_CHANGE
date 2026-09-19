# RCAEval 已知故障自主排查评测集（v2）

这是 GopherAI DevSupport 的一个小型、可复现、只读故障诊断评测。它用于回答一个边界明确的问题：给定一段不含答案标签的微服务观测窗口，系统能否通过受治理的 Agent 工具调用，定位已知故障服务、给出候选故障类型，并列出证据与还需要确认的内容。

它不是生产事故数据，不执行故障注入、修复或回滚，也不证明对未见过的系统和故障具备泛化能力。

## 当前固定范围

本轮只保留 RCAEval 的 RE2-OB（Online Boutique）中的 18 个已知故障案例：

| 维度 | 范围 |
| --- | --- |
| 服务 | `emailservice`、`productcatalogservice` |
| 故障类型 | `cpu`（CPU 压力）、`mem`（内存压力）、`delay`（网络延迟） |
| 每个服务/故障组合 | 3 次独立重复运行 |
| 第 1 次运行 | `reference`：只读历史参考库，6 例 |
| 第 2 次运行 | `development`：开发调参集，6 例 |
| 第 3 次运行 | `holdout`：最终留出集，6 例 |
| 总数 | 18 例；每个 split 6 例 |

`holdout` 是当前代码和页面统一使用的留出名称；它对应本实验所说的 evaluation。参考、开发、留出三组没有重复 ID，且每个 split 都覆盖两个服务 × 三种故障。

固定数据来源：

- 仓库：[phamquiluan/RCAEval](https://huggingface.co/datasets/phamquiluan/RCAEval)
- 固定 revision：`afeacb11bcc94dadfd1c8f483ee4377b2b8b614e`
- 选择规则与工件摘要：[`v2-manifest.json`](./v2-manifest.json)

## 数据边界

原始 Parquet 只在本地准备数据时使用，不提交到 Git，也不上传 ECS。`scripts/eval/prepare_rcaeval.py` 会在指定缓存目录下载并校验原始文件，然后将每个案例转换成不超过服务端预算的匿名观测摘要。

在线 Agent 能看到：

- 不含业务答案的 opaque case ID；
- 时间范围和最早/最晚三分之一窗口摘要；
- 服务级 metrics、logs、traces 摘要；
- 每个来源的匿名模态名（`metrics.parquet`、`logs.parquet`、`traces.parquet`）、SHA-256 和字节数。

在线 Agent 不会看到：

- 原始目录名中的服务、故障类型和重复编号；
- `inject_time.txt`；
- 评分真值、原始路径和来源映射。

评分真值独立保存在 `internal/rcascoring/data/answers.json`。原始案例映射和完整来源摘要保存在 `evals/rcaeval/sources.json`，只供审计、复现和离线评分使用，不能作为 Agent 工具输入。公开参考库 `internal/rcaexperiment/data/references.json` 只包含第 1 次运行的 6 个已知组合；留出真值不会回流到参考检索。

## 数据准备

准备环境需要 Python、`requests`、`numpy`、`pandas`、`pyarrow`。缓存目录应放在仓库之外，例如：

```powershell
$cache = "$env:TEMP\gopherai-rcaeval-v2-cache"
python scripts/eval/prepare_rcaeval.py --cache $cache
```

脚本固定 revision、最多两路下载并发和 1 GiB 原始数据上限。重复运行会复用已校验文件。脚本只写入以下小型静态工件：

- `internal/rcaexperiment/data/observations.json`：18 条匿名观测窗口；
- `internal/rcaexperiment/data/references.json`：6 条历史参考案例；
- `internal/rcascoring/data/answers.json`：18 条独立评分真值；
- `evals/rcaeval/sources.json`：18 条来源和原始文件摘要。

当前 v2 工件的 SHA-256、字节数和选择约束以 `v2-manifest.json` 为准。生成后可执行：

```powershell
python -c "import json,collections; d=json.load(open('internal/rcaexperiment/data/observations.json',encoding='utf-8')); a=json.load(open('internal/rcascoring/data/answers.json',encoding='utf-8')); assert len(d['observations'])==18 and len(d['catalog'])==18; assert collections.Counter(x['split'] for x in d['catalog'])=={'reference':6,'development':6,'holdout':6}; assert len(a)==18; print('RCAEval v2 artifacts: OK')"
```

## 如何解释评测结果

系统报告至少应区分以下指标：

- `service Top-1`：第一候选服务是否为官方标签服务；
- `joint`：第一候选同时命中服务和故障类型；
- `evidence_valid`：结论引用的证据是否来自本次实际工具返回；
- Agent 工具调用次数、模型请求次数、tokens、耗时和失败原因。

开发集用于调整提示词、工具预算和停止条件。冻结这些设置后，才运行 `holdout`，并把留出结果作为一次独立的最终检查。不能把参考集上的命中率称为泛化能力，也不能把同一故障配方的三次运行称为真实生产事故统计。

## 与生产系统的关系

评测使用与生产 Agent 相同的只读工具治理、候选范围校验、证据引用校验和超时/预算边界，但输入是离线 RCAEval 快照。服务器只提供静态数据和单例演示；原始 Parquet 下载、转换及批量评测在开发机执行，避免在小内存 ECS 上进行高负载计算。

## 固定留出评测结果

冻结 `rca-autonomous-agent-v3` 的提示词、工具、预算和实现后，使用固定模型 `qwen3.7-plus-2026-05-26` 对 6 条 `holdout` 案例顺序运行一次，结果如下：

| 指标 | 结果 |
| --- | ---: |
| 尝试 / 完成 / 执行失败 | 6 / 6 / 0 |
| 服务 Top-1 | 6 / 6 |
| 服务与故障类型联合命中 | 6 / 6 |
| 证据引用契约通过 | 6 / 6 |
| 模型请求 / 只读工具调用 | 40 / 30 |
| 输入 / 输出 tokens | 190729 / 31702 |
| 总耗时 / 单例平均耗时 | 477956 ms / 约 79.7 s |

完整记录保存在 [`agent-evaluation.json`](./agent-evaluation.json)，并绑定数据集 SHA-256、提示词 SHA-256 与实现 SHA-256。后端只在这些标识与当前代码全部一致时展示成绩，避免把旧报告冒充成当前结果。评分标准答案只在 Agent 停止后由独立 Go 评分器读取，不进入模型提示词或任何工具输出。

该固定报告发布后，再次对 `holdout` 运行 CLI 只会标记为 `previously_exposed_case_replay`，不能生成第二份“首次固定评测”覆盖当前成绩。

这是固定、公开、范围受限的已知故障评测；每个案例只运行一次。它可以证明受治理的排查循环、证据引用与独立评分链路在这 6 个案例上完整运行，但不能外推为生产准确率，也不能证明未知故障识别、跨系统泛化或自动修复成功率。

## 旧版替换说明

此前 27 例、规则按钮、A/B/C 分组和多轮 `agent-replay*.json` 已从当前评测入口与仓库工件中移除，避免把不同口径混合展示。当前页面、数据加载器和报告只认本 README、`v2-manifest.json` 的 18 例约束以及 `agent-evaluation.json` 的 6 例留出结果。
