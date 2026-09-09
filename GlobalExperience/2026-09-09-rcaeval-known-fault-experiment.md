# RCAEval 已知故障实验：轻量发布与验收

## 实现范围

新增独立 `/dashboard/rca-experiment`，保留聊天、旧诊断、人工复核和线上策略。27 例公开观测，6 条只读参考；本地 Parquet 转换，服务器只部署约 2 MB 观测摘要、参考记录和压缩后的报告。

数据/匹配器/评分器分离；无 LLM 调用、无生产故障注入、无自动修复。本版案例匹配是确定性 Go 工作流，真实调用已有 Tool Runtime，线上审计落入 MySQL。无需新增中间件或迁移生产历史案例。

## 发布前检查

- 现有基础容器全部保留；ECS 只读检查 available 392 MiB、负载 0.01，满足本轮约定的 256 MiB 余量目标。
- 本地主 Go 全量测试、Vet，以及 RCA 匹配/控制器 Race；前端定向 lint 和生产构建。发布脚本仍会执行生产构建。
- 本机 Go 环境的 GOROOT/GOPATH 默认值不正确，单次进程设置 `GOENV=off`、正确 GOROOT 和缓存即可；不改用户全局环境。
- 原始 Parquet 不进入发布包；初始开发集的大型逐工具完整报告转存 `.codex-tmp`，仓库只保留最终开发与首次留出紧凑报告。
- 参考/观测 JSON 的 Hash 使用现有字节，`.gitattributes` 禁止这两类 JSON 行尾转换，并固定匹配器为 LF。用回归测试检查干净检出后的 Hash，防止本地报告正确、发布后失配。

## 发布方法

遵循 `2026-09-03-aliyun-ssh-container-deploy.md` 和现有 `scripts/deploy/deploy-aliyun.ps1`：本地 Linux 交叉编译、静态前端构建、SSH 上传、原子切换、健康门禁和失败回退；不在低内存 ECS 编译。

本地主目录存在用户编写且未提交的项目学习文档，不能删除或混入本功能提交。为满足发布脚本 clean-tree 要求，提交功能后从该提交创建 `.codex-tmp` 内干净发布 worktree，复用本地已安装 node_modules 的目录联接。发布源来自 git archive，目录联接及本地临时文件不会上传。

## 必要线上验收

1. release manifest 对应代码提交，公网网关、Backend Ready、Worker Ready 正常。
2. 认证后的目录/观测/报告/诊断 API 正常；报告数据 Hash 与匹配器 Hash 一致。
3. C 方案读取四个只读工具，返回 Trace 和候选；不执行后续修复建议。
4. 留出范围内联合正确 6/6，对照 5/6；范围外误接纳 4/6 必须保留并显示，不能被“待验证”文案掩盖。
5. 浏览器正常缩放下页面可滚动，首页卡片和独立路由可访问。

实际发布标识与线上检查结果完成后追加在本文件末尾。复现和中文演示步骤见 `evals/rcaeval/README.md`。

## 本轮发现的发布兼容问题

从全新 Windows worktree 发布时，Git `core.autocrlf=true` 把 PowerShell 脚本转换为 CRLF，其中 Bash here-string 也携带 CR。首个只读容量检查报 `set: pipefail\r: invalid option name`，在上传/切换前停止，因此线上服务未受影响。

修复：在 `Invoke-RemoteScript` 写入 SSH stdin 前统一 CRLF→LF。这样发布不依赖某个开发目录偶然是 LF；不改变远程命令含义、不关闭容量门禁。该修复与匹配算法无关，不重新调参或覆盖首轮留出报告。

首次页面验收发现新页面请求错误地重复携带 `/v1`：Axios `baseURL=/api`，现有静态网关会将 `/api/...` 改写为 `/api/v1/...`。组件请求应为 `/experiments/rca`，不是 `/v1/experiments/rca`。404 来自 `/api/v1/v1/...`，不是鉴权或数据加载失败。已统一组件 endpoint，并添加网关路径回归；保留完整发布流程而不是手动覆盖线上 JS。这说明控制器单测和构建不能代替经过网关的浏览器业务验收。
