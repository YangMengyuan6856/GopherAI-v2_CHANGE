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

线上发布与真实模型验证结果见下方记录。

## 第一轮真实运行及针对性修正

- 首次部署 `20260910133037-f3718b541441`，bundle SHA `cea9486feea61897da113674153307da67eb2639850d16869e29388a78c53d92`。Backend/Worker/Prometheus/Grafana/前端健康门通过，Prometheus 2/2。
- 用当前账号已有 `m3b-config.json`，输入先核对 `release.timeout_seconds`、再交给 DiagnosticAgent 分析 HTTP 502 与 context deadline exceeded。真实 Trace `25bea8b8-69c1-4225-a7cd-a94f43d29304`：4 轮调度、4 次委派、10541 个已报告 Token；K 检出 47 → D 接收该证据并分析 → K 补查代理配置无证据 → K 再查文件名无证据，最终 no_new_evidence / partial。
- 这次运行证明动态委派与交接确实发生，但暴露上下文元数据缺口：SharedEvidence 没传文档 Title，Supervisor 错把数据库 source_id UUID 当作文件名不匹配；诊断还把 release 字段用途猜成 probe 专用。
- v1.1 补传 Title，明确 source_id 是内部 ID，参数作用范围没有定义就必须待确认。新增标题保留与未 resolved 兜底不能升级结论的回归测试。不删除第一次运行事实，不声称由此获得准确率提升。
- 浏览器实看新页面为深色独立、可滚动工作区，无原白色指标卡；代码与 RCAEval 页面/数据相互独立。

## 最终版本发布

- 代码提交 `623fbb63c12e6c539adc125c35b3d1fa5ab783d6` 已推送 `origin/add_eico`；发布号 `20260910141614-623fbb63c12e`。
- Bundle SHA-256：`22c3f1ef41e7a46f4e5dcd8ef9839dffbc159252bf3ad0e6cf4482d9ac32f3b5`。
- Backend SHA-256：`b7e8606a9fca486b2e745f897cce6b64ce870d5d43356f5889dde80408858244`。
- 使用既有本地构建、上传、原子切换流程；Backend/Worker live 与 ready、Prometheus 2/2、Grafana、前端均通过。未删除原容器，保留上一版回滚；部署后可用内存约 423 MiB。
- v1.1 补丁定向 Go 测试、Vet、Race 和前端 lint/build 通过；完整 Go 测试与 Vet 在主体实现后通过。RCA 前端范围测试通过，RCA 源码及数据没有改动。
- 发布后公网 `/health/ready` 为 ready，MySQL、RabbitMQ、Redis cache/vector 与模型配置均为 up。模型配置健康不等同于模型答案准确性。
- 新页面 `/dashboard/collaboration` 可独立打开，保留全局鉴权；通过当前已登录账号真实调用，不创建或修改人工评测结论。

## v1.1 真实模型复验

- 使用页面默认“先查文档 → 再诊断”模拟报告运行一次，Trace `539c5a0d-eecc-4040-b9a3-942a0b22a519`。返回 `complete / model_finished`，4 次 Supervisor 调用、3 次委派、13534 个模型报告 Token。
- 第 1 轮 KnowledgeAgent 查到 `release.timeout_seconds = 47`；第 2 轮 DiagnosticAgent 明确接收前轮证据 `d1:aa07e796-3f22-5839-8aee-fc13b1da5a8c`；第 3 轮 Supervisor 根据诊断缺口，重新委派 KnowledgeAgent 查 `probe_code` 的定义和参数关联；第 4 轮主动停止，列出代码实现、参数消费路径和请求耗时等待确认项。
- 浏览器展开第一轮证据，实际显示 `document_chunk · m3b-config.json · L4–5`，文档标题已传递，不再只向模型交付不透明的内部 source_id。
- 只读查询 MySQL 得到 7 条对应控制审计：4 条 accepted 调度事件、3 条 succeeded 委派事件；未篡改人工标签或答案。审计表本版 latency_ms 未填，不能将其零值当作真实执行耗时；页面各轮耗时来自执行记录。
- 局限仍须保留：模型个别措辞将 probe_code 推测为预设探针类型，或将 47 秒评价为较长；原文只有配置字段，不能据此证明用途、有效运行值或因果关系。引用归属校验不能完全防止语义过度推断，本次仅验收动态编排、真实交接、按反馈追加查询与正常停止，**不作为诊断准确率达标或真实故障被解决的证明**。
- 第一轮 partial 与第二轮 complete 均记录在案；后者“完成”指本次编排结束，不代表根因确认。未为了得到成功状态反复挑选运行结果。
