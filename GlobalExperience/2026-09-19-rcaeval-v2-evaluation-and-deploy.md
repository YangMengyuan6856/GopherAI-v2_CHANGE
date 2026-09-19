# 2026-09-19 RCAEval v2 固定已知故障评测与部署记录

## 1. 本次目标

用一版范围清晰、可复核的公开数据评测替换旧 RCAEval 演示，证明 GopherAI 在**已知故障范围**内可以完成：

1. 读取匿名微服务观测窗口；
2. 由模型自主选择只读指标、日志、调用链与历史案例工具；
3. 随新证据更新假设；
4. 输出故障服务、候选类型、证据和待确认项；
5. Agent 停止后由隔离的 Go 评分器核对结果。

不宣称未知故障识别、跨系统泛化、生产准确率或自动修复成功率。

## 2. 固定数据口径

- 数据源：RCAEval RE2-OB（Online Boutique），固定 revision `afeacb11bcc94dadfd1c8f483ee4377b2b8b614e`。
- 服务：`emailservice`、`productcatalogservice`。
- 故障：`cpu`、`mem`、`delay`。
- 18 例：reference 6、development 6、holdout 6。
- 原始约 184 MB Parquet 只在本地缓存和转换，不提交 Git、不上传 ECS。
- Agent 只读取不含标签的匿名摘要；评分答案保存在独立 scorer 工件中，不能通过 Agent 工具访问。

## 3. 固定留出结果

冻结 Agent v3 的提示词、工具、预算和实现后，使用 `qwen3.7-plus-2026-05-26` 对 6 条 holdout 各运行一次：

| 指标 | 结果 |
| --- | ---: |
| 尝试 / 完成 / 失败 | 6 / 6 / 0 |
| 故障服务 Top-1 | 6 / 6 |
| 服务 + 故障类型联合命中 | 6 / 6 |
| 证据引用契约通过 | 6 / 6 |
| 模型请求 / 只读工具调用 | 40 / 30 |
| 输入 / 输出 tokens | 190729 / 31702 |
| 总耗时 | 477956 ms |

报告绑定 dataset、prompt、Agent implementation 三重 SHA-256。服务端展示前还会重新检查：恰好为当前 6 个 holdout ID、ID 不重复、轨迹和答案归属一致、当前 scorer 重算一致、证据合同重算一致、聚合指标一致。任一不一致返回 503，不展示旧成绩。

固定报告发布后，后续 holdout CLI 运行只能标记为 `previously_exposed_case_replay`，不能冒充第二次首次固定评测。

## 4. 发布前验证

- `go test -p 1 ./...`：通过。
- `go test -p 1 ./...`（`common/mcp` 子模块）：通过。
- `go vet -p 1 ./...`：通过。
- `go test -race -p 1 ./internal/rcaagent ./controller/rcaexperiment`：通过。
- Vue ESLint：通过。
- RCA 前端 Node 测试：2 / 2 通过。
- Vue production build：通过；只有既有 bundle size / Browserslist 提示。
- 18 条 catalog、observation、answer、source ID 一致；manifest 工件 Hash 与字节数一致。

本机若出现 `GOROOT`、`GOPATH` 同指向 `F:\Golang`，需要在单次进程显式使用：

```text
GOENV=off
GOROOT=C:\Program Files\Go
GOPATH=<任务专用临时目录>
GOMODCACHE=F:\Golang\pkg\mod
GOTOOLCHAIN=local
```

不要修改用户全局 Go 配置。

## 5. 小内存 ECS 部署

使用既有本地构建、远端原子切换脚本：

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass -File scripts/deploy/deploy-aliyun.ps1 `
  -HostAlias gopherai-aliyun `
  -SshConfigPath C:\Users\Lenovo\.ssh\config `
  -RunLocalTests
```

没有传 `-DeployConfig`；远端配置、上传目录和运行数据被保留。没有在 ECS 或容器内运行 Go/Vue 编译，也没有删除或重建 `gopherai2`。

### 已部署版本

```text
branch       add_eico
git sha      e7f569369fcc916ad31f9c66a7b6a13a0b2a1f65
release      20260919173819-e7f569369fcc
bundle sha   baf5eddc2f597f244d9c115c4b9040bb354e1c2416955b9d89c63382c0c16430
build        local-linux-amd64-nocgo
source_dirty false
```

部署健康门通过：后端 live/ready、MySQL、RabbitMQ、Redis cache/vector、索引 Worker、Prometheus 2/2 targets、Grafana、MCP、静态前端网关均正常。公网 `/health/ready` 返回 ready，`/dashboard/rca-experiment` 返回 HTTP 200；容器中的 `agent-evaluation.json` 与 `v2-manifest.json` 已确认存在。

## 6. 可复用经验

1. 正式留出报告必须保留失败分母，并与数据、提示词、实现和当前 scorer 同时绑定。
2. 标准答案不能进入模型上下文或工具返回；历史参考案例需要明确标注“参考”，不能当成当前根因。
3. 发布后的留出集已经暴露，后续运行只能称为回放，不能继续称为首次盲测。
4. 小内存 ECS 只做校验、解压、原子切换和健康检查；所有高负载测试、前端构建、Go 交叉编译都放在本地。
5. SSH 在切换阶段短暂无输出时先等待健康门；若真的断开，应重连检查 release manifest、进程和健康接口，不要盲目重复部署。
