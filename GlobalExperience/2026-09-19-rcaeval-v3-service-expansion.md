# 2026-09-19 RCAEval v3 根因服务扩容与部署记录

## 1. 目标与边界

在不重写 RCA Agent、不重新引入难解释故障类型的前提下，将固定已知故障评测从 2 个根因服务扩到 4 个，增强面试演示和技术说明的可信度。

- 新增服务：`checkoutservice`、`currencyservice`。
- 保留服务：`emailservice`、`productcatalogservice`。
- 保留故障：`cpu`、`mem`、`delay`。
- 明确排除：`disk`、`loss`、`socket`，避免把当前证据合同不擅长区分的类型重新混入主成绩。
- 能力边界仍是已知故障辅助排查，不宣称未知故障发现、生产因果确认或自动修复成功率。

## 2. 数据与隔离

固定数据源仍是 RCAEval RE2-OB revision `afeacb11bcc94dadfd1c8f483ee4377b2b8b614e`。

| 项目 | v3 口径 |
| --- | --- |
| 根因服务 | 4 |
| 故障类型 | 3 |
| 服务 × 故障组合 | 12 |
| reference / development / holdout | 12 / 12 / 12 |
| 总案例 | 36 |
| 原始 Parquet | 366,842,864 bytes，仅本地 |
| ECS 观测摘要 | 2,713,869 bytes |

Agent 只读取 opaque ID、匿名指标/日志/调用链摘要和 12 条 reference 历史案例。原始目录名、注入信息、答案和来源映射没有注册成工具。评分真值仍由 Agent 停止后的独立 Go scorer 读取。

工件 Hash：

- observations SHA-256：`723943b7c44d1c05ec97b913d56b1a56a179ddf2bf0ec3a54eceeef1896c01e3`
- references SHA-256：`877aa4ca74ce84bc54abd51caf2d04ebf9dbd8a6be385083034508691a56efb8`
- answers SHA-256：`f50be1279f7988dbc435080bcaacabef3d36ca0a7613e948718e4e05164d51d1`
- sources SHA-256：`2563a27b39ce06b6404841f32c7b645b004cc9b0c4e6aa886fe6cc0ef52bef6c`

## 3. Agent v4 与固定评测

`rca-autonomous-agent-v4` 只扩展最终候选服务白名单，仍沿用原有五种只读工具、8 次模型调用上限、6 次工具调用上限、180 秒总超时、参数校验、重复调用防护和引用归属检查。

固定模型：`qwen3.7-plus-2026-05-26`。

| 指标 | 结果 |
| --- | ---: |
| 尝试 / 完成 / 执行失败 | 12 / 12 / 0 |
| 故障服务 Top-1 | 12 / 12 |
| 服务 + 故障类型联合命中 | 12 / 12 |
| 证据引用合同 | 12 / 12 |
| 模型调用 / 工具调用 | 75 / 61 |
| 输入 / 输出 tokens | 355,031 / 47,857 |
| 总耗时 / 平均耗时 | 758,999 ms / 63.25 s |

按服务拆分均为 3 / 3 联合命中。工具分布为 overview 12、metrics 15、logs 12、traces 13、history 9；说明流程不是强制每例固定调用全部工具，但该有限样本结果不能外推为生产准确率。

固定报告 SHA-256：`0befb77f1316869841dcb77ed650c310b5ac7ecac71d69a59bc3c9d7fc600ee8`。报告绑定 dataset、prompt、implementation 三重 SHA。服务端展示前重算 12 个 ID、逐例 scorer、证据合同和聚合指标；任一不一致返回 503。

## 4. 验证

- 根目录 `go test -p 1 ./...`：通过。
- MCP 子模块 `go test -p 1 ./...`：通过。
- `go vet -p 1 ./...`：通过。
- `go test -race -p 1 ./internal/rcaagent ./controller/rcaexperiment`：通过。
- Vue lint：通过。
- RCA 前端 Node 测试：2 / 2 通过。
- Vue production build：通过；只有既有 bundle size / Browserslist 提示。
- 浏览器只读验收确认页面展示 36 例、四个服务、12 条固定记录和 12 / 12 四项结果。

## 5. 小内存 ECS 部署

仍使用本地构建、上传、容器内校验和原子切换：

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass -File scripts/deploy/deploy-aliyun.ps1 `
  -HostAlias gopherai-aliyun `
  -SshConfigPath C:\Users\Lenovo\.ssh\config `
  -RunLocalTests
```

没有传 `-DeployConfig`，没有在 ECS 编译，没有删除或重建 `gopherai2`，远端配置、上传目录和运行数据均保留。

```text
branch       add_eico
git sha      64e307f78ed5d50cd0b7bb005bf937159a7da4a8
release      20260919192707-64e307f78ed5
bundle sha   141134724bb95f75f34adef3672880b48afcb47769489d1a0cfefa4ab8299b31
build        local-linux-amd64-nocgo
source_dirty false
```

内部健康门通过后，公网 `http://101.200.145.78:8080/health/ready` 和评测页面均返回 HTTP 200。页面确认加载 `rca-autonomous-agent-v4`、12 条固定轨迹和当前三重 Hash。临时评测二进制、检查点和中转报告在固定报告落库后已从宿主机与容器 `/root` 删除。

## 6. 面试陈述边界

可以陈述：系统在 RCAEval Online Boutique 的四个受支持根因服务、三类已知故障、12 条固定评测运行中完成 12 / 12 联合命中，且全过程有只读工具治理、证据引用、失败分母和独立评分。

不能陈述：12 / 12 等于生产准确率、这是严格盲测、系统能发现未知故障、历史检索必然带来收益，或系统已经执行并验证了修复动作。
