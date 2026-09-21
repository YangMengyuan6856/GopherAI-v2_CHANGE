# 2026-09-21 阿里云百炼业务空间专属域名迁移

## 背景

阿里云通知：共享域名 `dashscope.aliyuncs.com` 将于 2026-09-30 起进入维护状态。维护状态不是立即停服，但共享入口不再获得新特性。GopherAI 因而迁移到华北 2（北京）默认业务空间的专属域名。

本次只迁移 OpenAI 兼容 API 的 Base URL，不更换 API Key，不改变模型分层，也不重建 Redis 向量索引。

## 迁移范围

项目的 `ragModelConfig.baseUrl` 是统一模型入口，下列调用都会读取同一个配置：

- 快速问答模型与深度问答模型；
- 意图识别中的语义召回和模型裁决；
- `text-embedding-v4` 文档与查询向量；
- LLM-as-a-Judge；
- 动态协作 Agent；
- RCA 已知故障排查 Agent。

因此迁移不是只改聊天模型，而是统一迁移 Chat、Embedding、Judge 和 Agent 链路。

## 关键兼容问题

原代码通过 Base URL 是否包含字符串 `dashscope` 来识别百炼提供方。专属域名采用 `{workspaceId}.cn-beijing.maas.aliyuncs.com`，若只替换配置而不修改识别逻辑，会产生两个隐蔽问题：

1. 旧模型名的安全回退策略失效；
2. Qwen 确定性链路不再携带 `enable_thinking=false`，可能重新引入不必要的思考延迟。

修复方式是解析 URL hostname，并同时识别阿里云共享域名与 `.maas.aliyuncs.com` 专属域名。不能继续使用任意字符串包含判断，否则形似阿里云域名的第三方 URL 也可能错误启用 Qwen 专属参数。

## 上线前验证

在不输出 API Key 的前提下，从运行容器使用现有密钥直接请求新的专属入口：

- `qwen3.7-flash-2026-07-15` Chat Completions：HTTP 200；
- `text-embedding-v4`、1024 维 Embeddings：HTTP 200。

这同时证明：

- 当前 API Key 属于该默认业务空间；
- 对话与向量接口均可通过同一个专属 OpenAI 兼容入口访问；
- 无需更换密钥，也无需重建已有 1024 维 Redis 向量。

代码回归覆盖共享域名、专属域名、旧模型回退、Qwen 确定性参数，以及第三方仿冒域名不应被识别为百炼。

## 部署原则

- 遵循既有的本地交叉编译、上传、容器内原子切换流程；
- 不在 1.6 GiB ECS 中执行 Go 编译；
- 不使用 `-DeployConfig` 整体覆盖服务器配置；
- 仅对远端 `config.toml` 的旧 Base URL 做精确替换并保留备份，防止覆盖服务器密码等运行时配置；
- 原子部署继续保留已经更新的远端配置；
- 发布后检查健康接口、进程、日志以及远端实际 Base URL。

## 结果

部署结果、发布版本和线上健康证据在完成原子发布后补充。

## 官方依据

- 阿里云百炼地域与接入域名说明：<https://help.aliyun.com/zh/model-studio/regions/>
- 阿里云百炼 OpenAI 兼容 Base URL：<https://help.aliyun.com/en/model-studio/base-url>
