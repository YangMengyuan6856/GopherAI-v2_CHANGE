# 2026-09-19 RCAEval v4 服务与故障扩容记录

## 1. 目标与最终范围

本轮在不重写 RCA Agent 主链路的前提下，将公开已知故障评测从 4 个根因服务、3 类故障、36 个案例扩展为：

- 根因服务：`checkoutservice`、`currencyservice`、`emailservice`、`productcatalogservice`、`recommendationservice`。
- 故障类型：`cpu`、`mem`、`delay`、`socket`。
- 每个“服务 × 故障”组合保留 3 次独立运行，分别作为 reference、development、holdout。
- 总计 60 例，每个 split 20 例；20 条 reference 历史案例与 20 条 holdout 固定评测案例不共享 ID。

能力边界仍是固定候选范围内的已知故障辅助排查，不宣称未知故障发现、生产根因确认或自动修复成功率。

## 2. 数据选择与证据审计

数据源固定为 RCAEval RE2-OB revision `afeacb11bcc94dadfd1c8f483ee4377b2b8b614e`。扩容前曾检查 `disk` 故障，但 5 个目标服务中有 3 个 reference 运行缺少可用的根服务 `diskio` 主证据，因此在形成第一条固定成绩前停止该方案，没有为了扩大数量强行纳入。

最终选择 `socket` 连接资源压力：15 个相关案例的根服务均有足量、低缺失的 `socket` 指标，且 reference / development / holdout 三个 split 的选择规则一致。新增测试会逐个验证 20 个 reference 模式都存在可用主证据，防止以后生成脚本悄悄引入无证据组合。

| 工件 | SHA-256 | 字节数 |
| --- | --- | ---: |
| observations | `36f1874f1e48046ae054810076ac348ba7c09accc5aa235738231e4e3f44c024` | 4,523,943 |
| references | `502a7e8681f1e13385fcf5a5048fe1d2dd161386d49eba6fc0c893b5af715967` | 3,706 |
| answers | `4b4f64ce01b02443923e4484d9ddaf91555d1f793b0ab702b2d09fb859ac2f9b` | 8,948 |
| sources | `421d9aaba6a68364bf2bc74f482cd2d2d966f6f915822087699d92aa2ad8fef1` | 42,051 |

原始 Parquet 共 620,304,433 bytes，只保留在开发机仓库外缓存，不上传 ECS。服务端只部署 60 条观测摘要、20 条历史参考和独立评分真值。

## 3. Agent v5 与一次性固定评测

`rca-autonomous-agent-v5` 延续既有的有界自主排查循环：模型根据新证据选择 overview、metrics、logs、traces 或 history 只读工具，Go Harness 负责参数白名单、权限、重复调用、预算、超时和引用归属校验。上限仍为 8 次模型请求、6 次工具调用、180 秒总超时，失败不会回退为规则答案。

固定模型 `qwen3.7-plus-2026-05-26`，20 条 holdout 每条只运行一次：

| 指标 | 结果 |
| --- | ---: |
| 尝试 / 完成 / 执行失败 | 20 / 17 / 3 |
| 服务 Top-1 | 17 / 20 |
| 服务 + 故障类型联合命中 | 16 / 20 |
| 证据引用合同 | 17 / 20 |
| 模型请求 / 只读工具调用 | 119 / 96 |
| 输入 / 输出 tokens | 517,500 / 70,576 |
| 总耗时 / 单例平均耗时 | 1,316,859 ms / 65.84 s |

3 条模型调用超时和 1 条完成后的故障类型误判均保留在分母，没有重跑挑选最好结果。按服务看，`checkoutservice`、`productcatalogservice`、`recommendationservice` 均为 4/4 联合命中；`currencyservice` 为 2/4；`emailservice` 为 2/4。按故障看，CPU 5/5、delay 4/5、mem 4/5、socket 3/5 联合命中。

固定报告 SHA-256：`cdb2f9bb0685aafffd1aac80d0d06d63540de6d4c0a3ecaf2a41d502b52dcfa5`。报告绑定 dataset、prompt、implementation 三重 Hash；后端展示前会重算案例集合、评分和证据合同。

## 4. 验证与部署

- RCA 数据、评分器、Agent 与 CLI 定向测试：通过。
- 根目录全量 Go 测试：通过；扩容暴露的一处旧测试硬编码下标已改为动态选择首条 holdout。
- MCP 子模块测试：通过。
- `go vet -p 1 ./...`：通过。
- RCA 数据与 Agent race 测试、控制器 race 测试：通过。
- 前端 RCA Node 测试：2/2 通过；Vue lint 通过。
- Vue production build：通过；仅有既有 bundle size 与 Browserslist 提示。

仍采用本地测试和 Linux 交叉构建、上传校验、容器内原子切换；没有在小内存 ECS 编译，也没有删除或重建 `gopherai2`。

```text
branch       add_eico
git sha      5d1f3b953b82951cfd44f3e4ae2d80612adf73be
release      20260919212420-5d1f3b953b82
bundle sha   e2e7c1b16f7a47cd04bb0011d56a3858767580bfc1d871db30fe29b7a4e9a39a
build        local-linux-amd64-nocgo
source_dirty false
```

部署后 MySQL、RabbitMQ、Redis cache、Redis vector、模型配置、索引 Worker、Prometheus、Grafana、MCP 和前端网关健康检查通过；公网 `/health/ready` 与 `/dashboard/rca-experiment` 返回 HTTP 200。

## 5. 面试陈述边界

可以陈述：系统在 RCAEval Online Boutique 的 5 个受支持根因服务、4 类已知故障、20 条一次性固定留出运行中取得 17/20 服务 Top-1、16/20 联合命中，并保存完整工具轨迹、证据引用、失败分母与独立评分结果。

不能陈述：16/20 等于生产准确率、这是严格盲测、系统能识别未知故障、历史案例一定提高每个案例的正确率，或系统已经执行并验证修复动作。
