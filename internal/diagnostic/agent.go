package diagnostic

import (
	"fmt"
	"sort"
	"strings"
)

const AgentVersion = "diagnostic-agent-v1"

type Agent struct {
	extractor Extractor
}

func NewAgent() *Agent { return &Agent{extractor: Extractor{}} }

func (agent *Agent) Analyze(raw string) (ExtractedInput, Result, error) {
	extracted, err := agent.extractor.Extract(raw)
	if err != nil {
		return ExtractedInput{}, Result{}, err
	}
	result := Result{
		Version:            SchemaVersion,
		Symptom:            extracted.Symptom,
		Components:         append([]string(nil), extracted.Components...),
		ErrorSignatures:    append([]string(nil), extracted.ErrorSignatures...),
		KnownEnvironment:   append([]EnvironmentFact(nil), extracted.KnownEnvironment...),
		ConclusionStatus:   ConclusionHypothesis,
		NeedsUserInput:     false,
		MissingInformation: missingInformation(extracted),
	}
	for _, signature := range extracted.ErrorSignatures {
		if playbook, exists := diagnosticPlaybooks[signature]; exists {
			hypothesis := playbook.hypothesis(signature, extracted)
			if index := hypothesisIndex(result.Hypotheses, hypothesis.ID); index >= 0 {
				result.Hypotheses[index] = mergeHypotheses(result.Hypotheses[index], hypothesis)
			} else {
				result.Hypotheses = append(result.Hypotheses, hypothesis)
			}
			if playbook.requiresClarification {
				result.NeedsUserInput = true
				result.MissingInformation = appendMissing(result.MissingInformation, playbook.missingField, playbook.question)
			}
		}
	}
	if len(result.Hypotheses) == 0 {
		result.ConclusionStatus = ConclusionInsufficient
		result.NeedsUserInput = true
		if len(result.MissingInformation) == 0 {
			result.MissingInformation = []MissingInformation{{Field: "error_evidence", Question: "请补充错误码、关键日志或异常发生前后的可观察现象。", Critical: true}}
		}
	} else {
		sort.SliceStable(result.Hypotheses, func(i, j int) bool { return result.Hypotheses[i].Confidence > result.Hypotheses[j].Confidence })
		if len(result.Hypotheses) > 3 {
			result.Hypotheses = result.Hypotheses[:3]
		}
	}
	if err := result.Validate(); err != nil {
		return ExtractedInput{}, Result{}, err
	}
	return extracted, result, nil
}

type playbook struct {
	id                    string
	cause                 string
	rationale             string
	confidence            float64
	steps                 []VerificationStep
	requiresClarification bool
	missingField          string
	question              string
}

func (playbook playbook) hypothesis(signature string, extracted ExtractedInput) Hypothesis {
	component := "相关依赖"
	if len(extracted.Components) > 0 {
		component = strings.Join(extracted.Components, "、")
	}
	hypothesis := Hypothesis{
		ID:         rootCauseID(signature, extracted, playbook.id),
		Cause:      playbook.cause,
		Confidence: playbook.confidence,
		Rationale:  fmt.Sprintf("用户提供的可观察信息中命中了 %s 错误特征，关联组件为 %s；当前仅形成待验证假设。%s", signature, component, playbook.rationale),
		Evidence: []EvidenceReference{{
			ID: "user-observation:" + signature, SourceType: EvidenceUserObservation,
			Summary: "脱敏后的用户日志命中错误特征：" + signature,
		}},
		VerificationSteps: cloneSteps(playbook.steps),
	}
	return specializeHypothesis(signature, extracted, hypothesis)
}

func specializeHypothesis(signature string, extracted ExtractedInput, hypothesis Hypothesis) Hypothesis {
	if signature == "context_deadline_exceeded" && containsValue(extracted.Components, "redis") {
		hypothesis.Cause = "Redis 端点配置错误、容器网络不共享或服务名解析失败导致请求超时"
		hypothesis.VerificationSteps = []VerificationStep{
			verify("inspect_container_network_membership", ActionInspect, "只读核对后端与 redis-vector 是否加入同一个 Docker 网络。", "两个容器共享预期网络。", "网络不共享会使服务发现或连接失败。"),
			verify("resolve_redis_service_name", ActionQuery, "从后端容器内解析 redis-vector 服务名。", "服务名解析到目标容器地址。", "解析失败说明服务名或网络归属不一致。"),
			verify("query_redis_readiness", ActionQuery, "查询 Redis 就绪状态和有界 PING 延迟，不输出凭据。", "Redis ready 且延迟低于调用预算。", "不就绪或超时可缩小到服务或网络路径。"),
		}
	}
	if signature != "connection_refused" {
		return hypothesis
	}
	text := strings.ToLower(extracted.SanitizedExcerpt)
	switch {
	case containsValue(extracted.Components, "rabbitmq"):
		hypothesis.VerificationSteps = []VerificationStep{
			verify("inspect_rabbitmq_container_state", ActionInspect, "只读检查 RabbitMQ 容器状态和退出信息。", "容器为 Up/running。", "exited 状态支持服务不可用假设。"),
			verify("inspect_rabbitmq_log_tail", ActionInspect, "读取有界且脱敏的 RabbitMQ 日志尾部。", "日志显示服务完成启动且没有致命错误。", "启动错误可解释端口拒绝连接。"),
			verify("query_rabbitmq_readiness", ActionQuery, "查询 RabbitMQ 就绪状态和 5672 监听。", "就绪检查通过且端口监听。", "不就绪或无监听支持服务未启动假设。"),
		}
	case containsValue(extracted.Components, "mysql"):
		hypothesis.VerificationSteps = []VerificationStep{
			verify("inspect_mysql_service_state", ActionInspect, "只读检查 MySQL 服务状态。", "服务状态为 active/running。", "inactive 支持服务不可用假设。"),
			verify("inspect_mysql_error_log_tail", ActionInspect, "读取有界且脱敏的 MySQL 错误日志尾部。", "日志显示数据库已完成启动。", "启动错误可解释本地端口拒绝。"),
			verify("query_local_port_listener", ActionQuery, "只读检查本地 3306 端口监听。", "3306 由预期 mysqld 监听。", "无监听说明数据库尚未提供连接。"),
		}
	case containsValue(extracted.Components, "redis") && strings.Contains(text, "127.0.0.1") && (strings.Contains(text, "另一个容器") || strings.Contains(text, "another container")):
		hypothesis.VerificationSteps = []VerificationStep{
			verify("inspect_backend_redis_host_config", ActionInspect, "只读核对后端 Redis 主机是否错误使用 127.0.0.1。", "跨容器依赖使用共享网络中的服务名。", "回环地址只指向后端容器自身。"),
			verify("resolve_redis_service_name", ActionQuery, "从后端容器解析 Redis 服务名。", "解析到 Redis 容器地址。", "解析失败说明服务名或网络配置不一致。"),
			verify("inspect_shared_network_membership", ActionInspect, "只读比较后端与 Redis 容器的网络归属。", "两个容器共享预期网络。", "无共享网络会阻断服务名访问。"),
		}
	case containsValue(extracted.Components, "redis"):
		hypothesis.VerificationSteps = []VerificationStep{
			verify("inspect_redis_container_state", ActionInspect, "只读检查 Redis 容器和进程状态。", "Redis 容器为 running。", "容器缺失或退出支持服务不可用假设。"),
			verify("query_redis_readiness", ActionQuery, "从调用方查询 Redis 就绪状态与端口监听。", "Redis ready 且 6379 可连接。", "不就绪或无监听可解释连接被拒绝。"),
		}
	}
	return hypothesis
}

func cloneSteps(steps []VerificationStep) []VerificationStep {
	result := make([]VerificationStep, len(steps))
	copy(result, steps)
	return result
}

func verify(id string, action string, instruction string, expected string, failure string) VerificationStep {
	return VerificationStep{ID: id, ActionType: action, Instruction: instruction, ExpectedObservation: expected, FailureMeaning: failure, ReadOnly: true}
}

var diagnosticPlaybooks = map[string]playbook{
	"connection_refused": {
		cause: "目标服务未监听、端口映射错误或调用方无法到达目标容器地址", confidence: 0.86,
		rationale: "connection refused 通常说明网络路径已到达目标主机，但目标端口没有接受连接。",
		steps: []VerificationStep{
			verify("verify_process", ActionInspect, "在目标运行环境查看对应服务进程和容器状态。", "目标服务状态为 running/Up。", "若服务未运行，优先定位启动失败原因。"),
			verify("verify_listener", ActionQuery, "在目标容器内只读检查预期端口是否处于 LISTEN。", "预期端口存在监听进程。", "无监听说明服务未成功绑定端口或配置端口不一致。"),
			verify("verify_caller_path", ActionQuery, "从实际调用方容器对目标主机名和端口执行只读连通性检查。", "调用方能够解析主机名并建立 TCP 连接。", "失败说明容器网络、服务名或端口配置不一致。"),
		},
	},
	"context_deadline_exceeded": {
		id: "backend_request_exceeds_timeout_budget", cause: "下游依赖响应时间超过当前调用链超时预算", confidence: 0.80,
		rationale:             "deadline exceeded 只能证明预算耗尽，仍需区分下游变慢、网络阻塞和超时设置过小。",
		requiresClarification: true, missingField: "stage_latency", question: "请补充同一 Trace 的阶段耗时、依赖就绪状态或调用方到依赖的连通性结果。",
		steps: []VerificationStep{
			verify("compare_latency", ActionCompare, "比较同一 Trace 中上游超时阈值与下游 P95/P99 延迟。", "超时阈值高于正常尾延迟且请求在预算内完成。", "若下游尾延迟超过阈值，应继续定位慢调用而非盲目扩大超时。"),
			verify("inspect_dependency", ActionInspect, "检查超时窗口内依赖服务的错误率、队列积压和资源水位。", "依赖无错误峰值且无明显积压。", "异常峰值可缩小到具体依赖或资源瓶颈。"),
		},
	},
	"redis_noauth": {
		id: "redis_authentication_missing_or_mismatched", cause: "Redis 已启用认证，但客户端未携带有效凭据或连接到了错误实例", confidence: 0.95,
		rationale: "NOAUTH 是 Redis 服务端返回的明确认证错误。",
		steps: []VerificationStep{
			verify("inspect_redis_target", ActionInspect, "核对脱敏后的 Redis 主机、端口、数据库号和认证配置来源。", "运行时配置指向预期 Redis 实例且认证字段已注入。", "配置缺失或目标不一致会直接产生认证失败。"),
			verify("query_redis_ping", ActionQuery, "使用应用相同的只读连接配置执行 PING，不输出凭据。", "返回 PONG。", "仍返回 NOAUTH 说明凭据缺失、错误或未被应用加载。"),
		},
	},
	"redis_wrongtype": {
		id: "redis_key_type_collision", cause: "应用以错误的数据结构操作了已存在的 Redis Key", confidence: 0.94,
		rationale: "WRONGTYPE 明确表示命令与目标 Key 的实际类型不兼容。",
		steps: []VerificationStep{
			verify("query_key_type", ActionQuery, "对脱敏后的目标 Key 执行只读 TYPE 和 TTL 检查。", "Key 类型与当前命令预期一致。", "类型不一致说明命名空间复用、旧数据或迁移兼容问题。"),
			verify("compare_key_owner", ActionCompare, "比较所有写入该 Key 前缀的代码路径和版本。", "同一前缀只有一个稳定的数据结构契约。", "多个写入方使用不同结构会持续复现 WRONGTYPE。"),
		},
	},
	"mysql_access_denied": {
		id: "mysql_credentials_or_grants_mismatch", cause: "MySQL 用户凭据、来源主机授权或目标实例配置不匹配", confidence: 0.94,
		rationale: "Access denied for user 是服务端在认证/授权阶段给出的明确拒绝。",
		steps: []VerificationStep{
			verify("inspect_mysql_target", ActionInspect, "核对脱敏后的 MySQL 用户、目标主机、端口和配置加载来源。", "运行时目标与授权实例一致。", "连接到错误实例或配置未刷新会造成持续拒绝。"),
			verify("query_mysql_identity", ActionQuery, "使用应用同源配置执行只读 SELECT USER(), CURRENT_USER()。", "连接成功且授权身份符合预期。", "失败或身份不一致说明用户/host 授权或凭据配置有误。"),
		},
	},
	"mysql_too_many_connections": {
		id: "mysql_connection_pool_exhaustion", cause: "MySQL 连接数达到上限，可能由连接池配置或连接泄漏放大", confidence: 0.92,
		rationale:             "1040/too many connections 表明服务端已拒绝新连接。",
		requiresClarification: true, missingField: "pool_metrics", question: "请补充同一时间窗口的数据库连接数、连接池指标和请求量。",
		steps: []VerificationStep{
			verify("query_connection_watermark", ActionQuery, "只读查询当前连接数、max_connections 和按用户分布。", "连接水位显著低于上限且分布符合容量设计。", "接近上限时需定位最大连接来源。"),
			verify("compare_pool_limits", ActionCompare, "汇总各副本连接池 MaxOpenConns 与数据库上限。", "所有副本理论连接总数留有安全余量。", "理论总数超过上限说明容量配置不一致。"),
		},
	},
	"mysql_unknown_database": {
		id: "mysql_database_name_mismatch", cause: "应用配置的数据库名不存在或连接到了错误 MySQL 实例", confidence: 0.96,
		rationale: "Unknown database 直接指出目标实例中没有请求的 schema。",
		steps: []VerificationStep{
			verify("inspect_database_name", ActionInspect, "核对运行时数据库名和目标实例，避免输出凭据。", "数据库名与部署清单一致。", "名称或实例不一致即为主要配置缺口。"),
			verify("query_schema_presence", ActionQuery, "在目标实例只读查询 information_schema.schemata。", "目标 schema 存在。", "不存在说明初始化未执行或连接目标错误。"),
		},
	},
	"mysql_lock_wait_timeout": {
		id: "mysql_concurrent_update_lock_contention", cause: "事务等待行锁超过阈值，存在长事务或热点更新竞争", confidence: 0.90,
		rationale: "lock wait timeout 表明事务未能在预算内获得锁。",
		steps: []VerificationStep{
			verify("query_lock_waits", ActionQuery, "只读查看当前事务、锁等待和阻塞链。", "没有长期运行事务或持续阻塞链。", "阻塞链可定位持锁事务与竞争表。"),
			verify("compare_transaction_scope", ActionCompare, "比较问题路径的事务范围与慢查询时间。", "事务只覆盖必要数据库操作。", "事务内包含外部调用或批量工作会放大锁持有时间。"),
		},
	},
	"jwt_expired": {
		id: "jwt_access_token_expired", cause: "访问令牌已过期或签发端与验证端时钟偏差过大", confidence: 0.93,
		rationale: "expired 信号明确指向令牌有效期校验，但仍需排除系统时钟漂移。",
		steps: []VerificationStep{
			verify("inspect_token_time", ActionInspect, "只读取令牌 exp/iat 元数据并与服务端当前时间比较，不展示完整令牌。", "当前时间位于合法有效期内。", "超出有效期需重新登录；明显偏差需校准时钟。"),
			verify("inspect_refresh_flow_result", ActionInspect, "只读检查访问令牌过期后的刷新流程状态和稳定错误码。", "刷新成功后新访问令牌可通过验证。", "刷新失败说明还需定位刷新令牌或前端续期流程。"),
		},
	},
	"jwt_signature_invalid": {
		id: "jwt_signing_key_changed_or_loaded_differently", cause: "令牌签名密钥、算法或签发环境与验证端不一致", confidence: 0.92,
		rationale:             "invalid signature 说明完整性校验失败。",
		requiresClarification: true, missingField: "signing_config_metadata", question: "请补充重启前后签名配置的来源、版本或哈希元数据，以及新令牌是否可用。",
		steps: []VerificationStep{
			verify("compare_jwt_config", ActionCompare, "比较签发端与验证端的算法、kid 和密钥版本标识，不输出密钥。", "两端使用相同算法和有效密钥版本。", "版本或算法不一致会使所有对应令牌验签失败。"),
		},
	},
	"dns_name_not_found": {
		id: "container_service_name_or_network_membership_mismatch", cause: "调用方环境无法解析目标服务名，可能是容器网络或 DNS 配置不一致", confidence: 0.91,
		rationale:             "no such host 表明连接在名称解析阶段失败。",
		requiresClarification: true, missingField: "container_network", question: "请补充调用方与目标容器的网络归属和实际服务名。",
		steps: []VerificationStep{
			verify("query_dns_from_caller", ActionQuery, "从实际调用方容器只读解析目标服务名。", "解析到预期网络中的地址。", "解析失败说明服务名、网络加入关系或 DNS 配置错误。"),
			verify("inspect_network_membership", ActionInspect, "查看调用方与目标容器加入的 Docker 网络。", "双方至少共享一个预期网络。", "没有共享网络时容器服务名通常不可解析或不可达。"),
		},
	},
	"container_oom_killed": {
		id: "container_oom_during_parallel_build", cause: "容器内存超过限制并被内核终止", confidence: 0.98,
		rationale: "OOMKilled=true 或退出码 137 是强故障信号。",
		steps: []VerificationStep{
			verify("inspect_container_state", ActionInspect, "只读查看容器 State.OOMKilled、ExitCode 和内存限制。", "OOMKilled=false 且退出码不是 137。", "OOMKilled=true 基本确认内存超限终止。"),
			verify("compare_memory_peak", ActionCompare, "比较故障前内存峰值、限制和 Go 堆/非堆指标。", "峰值低于限制并留有余量。", "接近限制可进一步区分并发峰值、缓存增长或泄漏。"),
		},
	},
	"filesystem_no_space": {
		id: "filesystem_capacity_or_inode_exhaustion", cause: "容器可写层或挂载卷空间/ inode 耗尽", confidence: 0.97,
		rationale:             "no space left on device 是文件系统返回的明确容量错误。",
		requiresClarification: true, missingField: "filesystem_usage", question: "请补充目标文件系统容量、inode 和 Docker 占用的只读统计。",
		steps: []VerificationStep{
			verify("query_disk_usage", ActionQuery, "只读检查目标路径所在文件系统的容量和 inode 使用率。", "空间和 inode 均低于告警阈值。", "任一达到上限都可解释写入失败。"),
			verify("inspect_growth_sources", ActionInspect, "查看日志、构建缓存和容器可写层的目录占用排行。", "不存在异常增长目录。", "占用排行可定位主要增长来源。"),
		},
	},
	"rabbitmq_not_found": {
		id: "rabbitmq_queue_name_mismatch", cause: "RabbitMQ 队列未声明、vhost 不一致或名称配置不匹配", confidence: 0.92,
		rationale: "NOT_FOUND - no queue 表明当前 vhost 中找不到目标队列。",
		steps: []VerificationStep{
			verify("compare_publisher_and_consumer_queue_names", ActionCompare, "只读比较发布者、消费者配置中的队列名和 vhost。", "发布者与消费者使用相同队列名和 vhost。", "任一名称不一致都会让发布端找不到消费者队列。"),
			verify("query_queue_catalog", ActionQuery, "只读查询目标 vhost 的队列目录。", "目录中存在配置的目标队列。", "目录缺失可验证队列未声明或名称错误。"),
		},
	},
	"rabbitmq_precondition_failed": {
		id: "rabbitmq_queue_declaration_argument_mismatch", cause: "同名 RabbitMQ 队列/交换机的声明参数与既有实体不一致", confidence: 0.94,
		rationale: "PRECONDITION_FAILED 常见于 durable、auto-delete、arguments 等声明契约冲突。",
		steps: []VerificationStep{
			verify("compare_existing_and_requested_queue_arguments", ActionCompare, "只读比较服务端现有队列 arguments 与新版本请求的声明参数。", "同名实体的 durability、类型和 arguments 完全一致。", "任一属性不一致都会导致声明被拒绝。"),
			verify("inspect_release_configuration_change", ActionInspect, "只读定位本次发布中死信交换机等队列配置的版本变化。", "发布前后声明契约兼容。", "发布配置变化可解释旧队列与新声明冲突。"),
		},
	},
	"http_401": {
		id: "jwt_expiry_or_clock_skew", cause: "请求缺少有效认证信息，或令牌被网关/后端拒绝", confidence: 0.82,
		rationale:             "401 表示请求未通过认证，但需结合响应方定位责任边界。",
		requiresClarification: true, missingField: "auth_trace", question: "请补充失败请求的 Trace、时间和脱敏后的认证错误类型。",
		steps: []VerificationStep{
			verify("inspect_auth_boundary", ActionInspect, "根据 Trace 只读确认 401 由网关还是应用返回，并检查认证头是否到达。", "认证头到达预期验证组件且令牌元数据有效。", "头丢失或验证端拒绝可定位具体边界。"),
		},
	},
	"http_404": {
		id: "frontend_backend_api_path_mismatch", cause: "请求路径、方法、代理前缀或后端路由版本不一致", confidence: 0.78,
		rationale: "404 只说明目标路由未匹配，需要确认由哪一层返回。",
		steps: []VerificationStep{
			verify("compare_route", ActionCompare, "比较浏览器请求 URL/方法、代理改写规则和后端注册路由。", "三层路径和方法一致。", "任一前缀或方法差异都可能产生 404。"),
		},
	},
	"http_413": {
		id: "proxy_or_backend_body_limit_exceeded", cause: "上传请求体超过代理或应用配置的大小限制", confidence: 0.94,
		rationale:             "413/request entity too large 明确表示请求体大小被拒绝。",
		requiresClarification: true, missingField: "upload_limits", question: "请补充文件大小、代理请求体上限和后端上传上限。",
		steps: []VerificationStep{
			verify("compare_upload_limits", ActionCompare, "比较实际文件大小、代理请求体限制和 Go 后端上传限制。", "文件大小同时低于代理与后端限制。", "最小的限制值即当前拒绝边界。"),
		},
	},
	"http_502": {
		id: "backend_upstream_unavailable", cause: "反向代理无法从上游获得有效响应，上游可能未启动、地址错误或异常退出", confidence: 0.88,
		rationale: "Bad Gateway 表明代理存在，但其上游链路失败。",
		steps: []VerificationStep{
			verify("inspect_upstream", ActionInspect, "核对代理 upstream 地址并查看同一时刻后端进程/容器状态。", "upstream 与后端监听地址一致且服务健康。", "地址不一致或后端退出会造成 502。"),
			verify("query_upstream_health", ActionQuery, "从代理所在网络直接请求后端健康接口。", "返回健康状态。", "失败可将故障收敛到代理到后端的路径。"),
		},
	},
	"http_504": {
		id: "backend_request_exceeds_proxy_timeout", cause: "反向代理等待上游响应超过超时阈值", confidence: 0.87,
		rationale:             "Gateway Timeout 表明代理已等待上游但未在预算内收到响应。",
		requiresClarification: true, missingField: "trace_latency", question: "请补充同一 Trace 的代理、后端、检索和模型阶段耗时。",
		steps: []VerificationStep{
			verify("compare_proxy_latency", ActionCompare, "比较代理超时、后端 Trace 耗时和下游依赖耗时。", "完整调用链在代理超时内完成。", "超时集中在哪一段即可定位慢依赖或预算不一致。"),
		},
	},
	"cors_rejected": {
		id: "cors_policy_missing_or_mismatched", cause: "前端 Origin、预检请求或服务端 CORS 响应策略不匹配", confidence: 0.91,
		rationale: "浏览器 CORS 拒绝发生在跨源访问策略校验阶段。",
		steps: []VerificationStep{
			verify("inspect_cors_exchange", ActionInspect, "查看浏览器 Network 中 Origin、OPTIONS 状态及 CORS 响应头。", "预检成功且 Allow-Origin/Methods/Headers 覆盖实际请求。", "缺失或不匹配的响应头即为拒绝边界。"),
		},
	},
	"sse_content_type_invalid": {
		id: "proxy_overrides_sse_content_type", cause: "SSE 响应被代理或后端返回为非 text/event-stream 类型", confidence: 0.93,
		rationale: "EventSource 对响应 Content-Type 有明确要求。",
		steps: []VerificationStep{
			verify("inspect_sse_headers", ActionInspect, "查看流式接口响应状态、Content-Type、缓存和代理缓冲头。", "Content-Type 为 text/event-stream 且代理缓冲关闭。", "错误页、JSON 或 text/plain 会使浏览器拒绝建立事件流。"),
		},
	},
	"authorization_header_missing": {
		id: "frontend_auth_header_not_attached", cause: "前端请求没有附加认证头", confidence: 0.96,
		rationale: "浏览器请求和服务端错误都指向 Authorization 头缺失。",
		steps: []VerificationStep{
			verify("inspect_request_interceptor", ActionInspect, "只读检查该请求是否经过认证拦截器，并与成功请求的头部字段名比较。", "失败请求包含 Bearer 认证头且不展示令牌值。", "若只有失败路径未附加认证头，可定位前端路径或拦截器匹配问题。"),
		},
	},
	"bearer_null": {
		id: "frontend_token_persistence_or_key_mismatch", cause: "登录响应、本地令牌存储键和请求拦截器的字段映射不一致", confidence: 0.95,
		rationale: "Bearer null 与本地令牌缺失共同指向令牌没有被正确持久化或读取。",
		steps: []VerificationStep{
			verify("compare_token_mapping", ActionCompare, "只比较登录响应字段名、本地存储 key 和拦截器读取 key，不读取令牌值。", "三处字段映射一致且值存在。", "字段名不一致或写入缺失会产生 Bearer null。"),
		},
	},
	"host_port_allocated": {
		id: "host_port_collision_with_existing_container", cause: "宿主机发布端口已被现有进程或容器占用", confidence: 0.97,
		rationale: "port is already allocated 是 Docker 绑定宿主机端口时的明确冲突。",
		steps: []VerificationStep{
			verify("inspect_port_owner", ActionInspect, "只读确认冲突端口的监听进程、容器归属和预期运行拓扑。", "该端口只由预期服务占用。", "被旧容器或其他进程占用即可解释新容器绑定失败。"),
		},
	},
	"redis_latency_spike": {
		id: "redis_resource_pressure", cause: "Redis 可能存在资源压力、慢命令或连接饱和", confidence: 0.62,
		rationale:             "单个延迟样本不足以区分资源压力与偶发网络抖动。",
		requiresClarification: true, missingField: "redis_window_metrics", question: "请补充固定窗口的延迟、CPU、连接数和脱敏 slowlog 元数据。",
		steps: []VerificationStep{
			verify("collect_redis_window", ActionQuery, "收集有界时间窗口的 Redis 延迟、连接数和资源水位。", "指标稳定且无同窗峰值。", "同窗峰值可用于区分资源、连接或慢命令问题。"),
		},
	},
	"rabbitmq_unacked_growth": {
		id: "index_worker_ack_or_handler_stall", cause: "索引 Worker 处理或确认消息的路径可能停滞", confidence: 0.68,
		rationale:             "unacked 持续增长而 CPU 空闲说明消息已投递给消费者但尚未完成确认。",
		requiresClarification: true, missingField: "consumer_trace", question: "请补充同一窗口的 Worker 消费日志、处理时长和下游索引就绪状态。",
		steps: []VerificationStep{
			verify("inspect_consumer_window", ActionInspect, "关联 unacked 增长窗口、Worker 处理时长、错误日志和下游就绪状态。", "处理时长有界且消息最终确认。", "停留步骤可区分处理器阻塞与下游依赖阻塞。"),
		},
	},
	"index_worker_missing": {
		id: "index_worker_not_running", cause: "独立索引 Worker 没有运行，任务持续在队列中等待", confidence: 0.94,
		rationale: "文档 pending、队列 ready 增长且进程列表无 Worker 是一致的可观察证据。",
		steps: []VerificationStep{
			verify("inspect_worker_readiness", ActionInspect, "只读检查 Worker 进程、9091 就绪接口和日志尾部。", "Worker 进程唯一且就绪接口返回 ready。", "进程缺失或不就绪可解释队列不被消费。"),
		},
	},
	"rag_zero_chunks": {
		id: "index_state_inconsistent_with_empty_chunks", cause: "文档 indexed 状态与零 Chunk 持久化结果不一致", confidence: 0.94,
		rationale: "非空文档的 chunk_count=0 与可检索状态契约冲突。",
		steps: []VerificationStep{
			verify("compare_index_counts", ActionCompare, "只读对照索引任务各阶段、解析输出计数和 MySQL Chunk 计数。", "解析与持久化计数均大于零且一致。", "首个变为零的阶段即为当前故障边界。"),
		},
	},
	"rag_unauthorized_citation": {
		id: "model_emitted_unauthorized_citation_id", cause: "模型输出了不属于授权 Evidence 集的引用 ID", confidence: 0.97,
		rationale: "回答引用 ID 与检索授权集合不相交，且 Citation Builder 已按契约拒绝。",
		steps: []VerificationStep{
			verify("compare_citation_set", ActionCompare, "只读比较模型引用 ID、授权 Evidence ID 和安全回退原因。", "每个引用都属于授权 Evidence 集。", "未知引用应被拒绝，不能补造证据。"),
		},
	},
	"rag_failed_version_inactive": {
		id: "new_document_version_failed_and_old_version_remains_active", cause: "新文档版本索引失败，旧版本按安全激活协议继续生效", confidence: 0.96,
		rationale: "active_version、失败任务和旧版本命中三项状态符合版本隔离设计。",
		steps: []VerificationStep{
			verify("inspect_version_state", ActionInspect, "只读检查新版本稳定失败码、active/latest 版本和旧版本可检索性。", "失败候选未替换活动版本，旧版本仍可查询。", "若活动别名已改变，则需升级为版本一致性事件。"),
		},
	},
	"rag_possible_acl_leak": {
		id: "possible_acl_filter_failure", cause: "可能存在 ACL 过滤缺口，也可能是共享测试账号或 owner 标注错误", confidence: 0.55,
		rationale:             "单张截图不足以确认跨租户泄露，但应按高风险事件收集最小证据。",
		requiresClarification: true, missingField: "acl_trace", question: "请提供脱敏 Trace 和文档 ID，以核对认证 owner、文档 owner 与 ACL 决策。",
		steps: []VerificationStep{
			verify("inspect_acl_decision", ActionInspect, "按脱敏 Trace 和文档 ID 只读核对认证 owner、文档 owner 与 ACL 决策，不打开正文。", "返回文档均属于授权 owner。", "owner 不一致且 ACL 允许时才支持过滤缺口假设。"),
		},
	},
	"sse_disconnect": {
		id: "proxy_read_timeout_closes_sse", cause: "代理读超时或调用链取消可能关闭长时间 SSE", confidence: 0.66,
		rationale:             "固定时长断开提示超时边界，但 200 状态不足以区分代理、后端和客户端取消。",
		requiresClarification: true, missingField: "disconnect_trace", question: "请补充代理 SSE 超时、后端同 Trace 取消原因和三端断开时间。",
		steps: []VerificationStep{
			verify("compare_disconnect_timeline", ActionCompare, "只读比较代理超时、后端取消和浏览器断开时间。", "三端时间线和取消原因一致。", "最先发生的边界是下一步定位方向。"),
		},
	},
	"sse_duplicate_replay": {
		id: "sse_reconnect_replays_non_idempotent_request", cause: "SSE 重连复用了请求标识，但服务端缺少生成请求去重", confidence: 0.94,
		rationale: "相同 request_id 对应两个生成请求并产生重复回答，符合非幂等重放特征。",
		steps: []VerificationStep{
			verify("compare_reconnect_runs", ActionCompare, "只读对照重连前后 request_id、后端 Run 数量和客户端去重状态。", "同一请求标识只产生一个权威 Run。", "两个 Run 说明幂等边界未覆盖重连。"),
		},
	},
	"sse_utf8_boundary": {
		id: "frontend_non_streaming_utf8_decoder", cause: "前端逐 Chunk 解码时没有保留 UTF-8 跨块状态", confidence: 0.96,
		rationale: "多字节字符跨 Chunk 且出现替换字符，是非流式 TextDecoder 的典型边界问题。",
		steps: []VerificationStep{
			verify("replay_utf8_boundary", ActionCompare, "用无敏感信息的多字节边界夹具复现，并检查 TextDecoder 的 stream 选项。", "跨 Chunk 解码后的字节序列和原文一致。", "仅非流式解码失败时可定位前端解码状态。"),
		},
	},
	"generic_service_busy": {
		id: "frontend_error_mapping_hides_specific_reason", cause: "统一错误文案隐藏了后端、网络或代理的具体失败类别", confidence: 0.50,
		rationale:             "只有“服务繁忙”无法确认具体下游，需先恢复状态码和 Trace。",
		requiresClarification: true, missingField: "request_trace", question: "请提供发生时间、HTTP 状态码和 Trace ID，再关联代理与后端日志。",
		steps: []VerificationStep{
			verify("request_trace_context", ActionUserCheck, "获取失败请求的状态码、Trace ID 和时间，不索要账号密码。", "能够关联到唯一的代理和后端请求。", "缺少这些信息时不得确认具体依赖故障。"),
		},
	},
	"knowledge_no_evidence": {
		id: "knowledge_base_has_no_authorized_evidence", cause: "知识库没有能够支持该问题的授权证据", confidence: 0.98,
		rationale:             "检索结果为空，且该类敏感事实不能依靠模型常识补全。",
		requiresClarification: true, missingField: "authorized_source", question: "是否可以提供包含该事实的非敏感授权文档或缩小查询范围？",
		steps: []VerificationStep{
			verify("report_evidence_gap", ActionUserCheck, "确认授权检索结果为空，明确报告证据不足并询问可用来源。", "不生成日期、凭据或来源位置等未经授权的事实。", "若没有新证据，运行应保持等待而不是猜测答案。"),
		},
	},
}

func rootCauseID(signature string, extracted ExtractedInput, configured string) string {
	if signature != "connection_refused" && signature != "context_deadline_exceeded" {
		return firstValue(configured, "cause_"+signature)
	}
	text := strings.ToLower(extracted.SanitizedExcerpt)
	if signature == "context_deadline_exceeded" && containsValue(extracted.Components, "redis") {
		return "redis_network_unreachable"
	}
	if containsValue(extracted.Components, "rabbitmq") {
		return "rabbitmq_service_unavailable"
	}
	if containsValue(extracted.Components, "mysql") {
		return "mysql_service_unavailable"
	}
	if containsValue(extracted.Components, "redis") {
		if strings.Contains(text, "127.0.0.1") && (strings.Contains(text, "另一个容器") || strings.Contains(text, "another container")) {
			return "container_uses_loopback_for_peer_service"
		}
		return "redis_service_unavailable"
	}
	return firstValue(configured, "backend_upstream_unavailable")
}

func containsValue(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func hypothesisIndex(values []Hypothesis, id string) int {
	for index, value := range values {
		if value.ID == id {
			return index
		}
	}
	return -1
}

func mergeHypotheses(current Hypothesis, candidate Hypothesis) Hypothesis {
	if candidate.Confidence > current.Confidence {
		current.Cause = candidate.Cause
		current.Confidence = candidate.Confidence
		current.Rationale = candidate.Rationale
	}
	seenEvidence := map[string]struct{}{}
	for _, evidence := range current.Evidence {
		seenEvidence[evidence.ID] = struct{}{}
	}
	for _, evidence := range candidate.Evidence {
		if _, exists := seenEvidence[evidence.ID]; !exists {
			seenEvidence[evidence.ID] = struct{}{}
			current.Evidence = append(current.Evidence, evidence)
		}
	}
	seenSteps := map[string]struct{}{}
	for _, step := range current.VerificationSteps {
		seenSteps[step.ID] = struct{}{}
	}
	for _, step := range candidate.VerificationSteps {
		if _, exists := seenSteps[step.ID]; !exists && len(current.VerificationSteps) < 5 {
			seenSteps[step.ID] = struct{}{}
			current.VerificationSteps = append(current.VerificationSteps, step)
		}
	}
	return current
}

func missingInformation(extracted ExtractedInput) []MissingInformation {
	missing := make([]MissingInformation, 0, 3)
	if len(extracted.Components) == 0 {
		missing = append(missing, MissingInformation{Field: "component", Question: "异常发生在哪个组件或调用链阶段？", Critical: true})
	}
	if len(extracted.ErrorSignatures) == 0 {
		missing = append(missing, MissingInformation{Field: "error_signature", Question: "请提供错误码或关键日志（请先移除真实密码、Token 和私钥）。", Critical: true})
	}
	if len(extracted.KnownEnvironment) == 0 {
		missing = append(missing, MissingInformation{Field: "environment", Question: "请补充运行环境，例如 Docker/宿主机以及相关组件版本。", Critical: false})
	}
	return missing
}

func appendMissing(items []MissingInformation, field string, question string) []MissingInformation {
	if field == "" || question == "" {
		return items
	}
	for _, item := range items {
		if item.Field == field {
			return items
		}
	}
	return append(items, MissingInformation{Field: field, Question: question, Critical: true})
}

func firstValue(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
