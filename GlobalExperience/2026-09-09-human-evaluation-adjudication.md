# 2026-09-09 人工评测结论接入经验

## 结论

- 人工工作簿共 350 条：Full 320 为 240 条通过、80 条退回；Judge 30 已全部评分。
- 首轮 Judge v2 线性加权 κ 为 0.6537，低于 0.70 门槛；v3 同集迭代反而降至 0.5432，因此拒绝候选并恢复 v2。这是真实反馈，不得改写成“校准通过”。
- Full 退回不是失败数据，应作为规则、证据和数据契约的变更输入。

## 接入原则

1. TXT 导入必须校验固定编号、切片顺序、枚举值和文件 SHA，解析失败时不写数据库。
2. 人工评论进入追加式 Review Hash，保留结论来源和修订链。
3. 数据集升级后只继承“原结论为通过且用例原始行 SHA 完全不变”的记录。
4. 已修改用例与原先退回用例保持待复核，不用脚本替人批准。
5. Judge Prompt 根据本轮分歧修订时必须披露为同集迭代校准；不能把复跑结果当成独立留出集结论。
6. 导入幂等键必须同时绑定结论文件、目标 Catalog 和 Governance；同一人工结论可用于修订目录的安全继承，但不能与旧 lineage 请求键冲突。

## 本轮主要修订

- Intent：follow-up 使用真实前序消息；组合问题只表示存在可独立执行的多个子任务；意图与授权/拒绝解耦。
- RAG：补充 ACL、重试队列、健康门、中文稠密召回、高风险审计与回滚阈值证据。
- Diagnosis：补齐索引 Worker 的 9091 ready probe 证据。
- Memory：固定主体、状态、新鲜度、置信度、相关性、去重和 Token 预算选择规则。
- Tool：为全部注册工具和 30 条场景固定 Schema、权限、超时、缓存、熔断与精确执行轨迹。
- Insufficient evidence：将工具意图、Prompt Injection 与冲突证据场景改为可独立判断的输入。

## 运维与发布检查

- 本地先运行 Catalog 校验、定向测试、全量 Go 测试、Vet/Race 和 Vue 构建。
- 从 clean Git SHA 构建并使用既有原子发布脚本部署；不在小内存 ECS 中编译。
- 部署后执行 approved-only 导入，预期新目录进度为 240 approved、80 pending、0 rejected。
- 在云端凭据环境真实运行 Intent Cascade、RAG 和 Judge；本地缺少模型凭据时的 fail-closed 不能伪装为模型质量失败。
- 剩余 80 条经所有者确认后才能封存 Full 320、固定重跑并审批基线。

## 2026-09-09 云端验证结果

- 新 Catalog 的 approved-only 导入首次创建 240 条；第二次运行创建 0、复用 240，证明断点恢复幂等。当前为 240 approved / 80 pending / 0 rejected。
- Intent Cascade：150 条准确率 95.33%、Macro-F1 95.31%、最低类召回 91.67%、严重误路由 0.67%、LLM 调用率 55.33%，技术门通过。
- RAG：Recall@5 100%、nDCG@5 95.99%、引用精确率 100%、引用覆盖率 98%、越权召回 0、无证据安全拒答率 100%、错误率 0，技术门通过。
- Judge v3：30/30 技术执行完成，但人工一致性 κ 0.5432，低于 v2 的 0.6537，因此候选被拒绝，生产使用恢复 v2；自动控制持续关闭。
- 本轮出现过一次导入幂等冲突：旧键只绑定结论文件，未绑定 Catalog/Governance。修复后键同时绑定三者，旧记录保留且没有删除或覆盖。

## 2026-09-09 最终封存与固定重跑

- 项目所有者完成修订后 80 条复核，当前 Catalog `69e5d1d9ea6102e2fdb12f23ebd0303a175e8d8f4e744a2edb3387ad422e7bb7` 为 `320 approved / 0 rejected / 0 pending`，Review Set SHA-256 为 `99abb5eac78e518120fce831ee0cca5ea3d6f91afc921c3143aef519f552bcc7`。
- Release `20260909114728-e190a9fb165d` 从 clean Git SHA `e190a9fb165d6b60a202f6fe80c9efd7e51c20da` 部署，公网与 MySQL、Redis、RabbitMQ、Backend、Index Worker、MCP、Prometheus、Grafana、静态前端网关健康门通过。
- 通过登录所有者显式确认创建只读不可变候选 `catalog-seal-9347cccd1154294783263fbeabcb50fc`，Seal SHA-256 为 `7388527fc11db6072170c104d0fd440a789114db981d1d4a1d3bedd47ce9035b`；该动作不写正式 baseline 或 active policy。
- 固定 Run `rerun-20260909T043921.016655656Z-4bbfb670` 按 Intent→RAG→Diagnosis→Tool→Memory→Unified 顺序完成 `6/6`，技术门通过；独立物理复验得到 Report SHA-256 `6b46cbdd815db0f46b36e42d1b4243df29ae448cc64ce7cc716613402f8ee371`。
- Judge v2 当前真实复跑为 `30/30`，线性加权 κ `0.6177`，低于 `0.70`。因此 Judge 只保留为离线辅助评估，`automation_use_permitted=false`；不得修改人工标签制造过线结果，也不得声称正式自动 Judge 已校准。
- 当前结论是“面试演示与工程交付主线完成”，不是“生产发布认证全部通过”。单 ECS 无隔离回滚窗口与 Judge 校准门作为披露边界保留；固定重跑技术通过不会自动晋级或切流。
