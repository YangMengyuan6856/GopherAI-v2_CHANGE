# 原双 Agent 动态编排发布经验

## 范围

- 新模型 Supervisor 取代旧协作 HTTP 入口的规则协调器；仍为显式 Shadow，不切流。
- 保留旧 Planner/Runner/Coordinator 和历史 A/B，供确定性回归，不覆盖既有分数。
- RCAEval 包、实验数据、报告和前端页没有修改。
- 新路由 `/dashboard/collaboration`；M-06 直达，策略页保留旧规则折叠对照。

## 实现要点

1. 动态委派不仅修改 UI：生成的 objective 真正进入 Knowledge 检索；选择的 evidence_ids 真正进入下一次 Diagnostic 分析。
2. Diagnostic 的原始规则只接收原始脱敏用户报告，不能把 Supervisor 想象的任务文字当观测。
3. 同轮最多两个固定角色，跨轮四次委派；不能把累计四次任务称为四个 Agent。
4. 模型输出严格解码、未知角色和引用先拒绝；重复动作/无新增证据/上下文/超时均可停止；错误不得静默用旧规则答案掩盖。
5. 使用既有 MySQL 工具审计表保存控制事件元数据和 Hash，完整本次记录由响应和下载提供，不宣称已实现轨迹持久化恢复。
6. 旧合并器为动态版本允许最多四项任务，但角色仍限两种；保留各轮有效结果与失败信息。引用验证不代表因果验证。

## 本地验证

- 全量 `go test -p 1 ./...`、`go vet -p 1 ./...` 通过。
- orchestration / controller 定向与 Race 通过。
- 前端 lint 和 production build 通过；既有 Browserslist 与 bundle 体积告警保留，不为此扩展依赖升级。
- 动态测试使用 stub 验证调度、并行、证据交接、预算、取消、隔离与审计契约，不作为诊断准确率。

## 部署约束

部署前 SSH 正常，服务器 1612 MiB 总内存、约 374 MiB 可用；三个原容器均在运行。使用干净 release worktree、本地交叉构建和现有 SSH 原子发布脚本；不得在 ECS 编译、安装依赖、删除容器或改用户复核记录。

线上发布与真实模型验证结果在完成后追加。
