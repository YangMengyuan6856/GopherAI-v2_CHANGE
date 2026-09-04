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
			result.Hypotheses = append(result.Hypotheses, playbook.hypothesis(signature, extracted))
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
	cause      string
	rationale  string
	confidence float64
	steps      []VerificationStep
}

func (playbook playbook) hypothesis(signature string, extracted ExtractedInput) Hypothesis {
	component := "相关依赖"
	if len(extracted.Components) > 0 {
		component = strings.Join(extracted.Components, "、")
	}
	return Hypothesis{
		ID:         "cause_" + signature,
		Cause:      playbook.cause,
		Confidence: playbook.confidence,
		Rationale:  fmt.Sprintf("用户提供的可观察信息中命中了 %s 错误特征，关联组件为 %s；当前仅形成待验证假设。%s", signature, component, playbook.rationale),
		Evidence: []EvidenceReference{{
			ID: "user-observation:" + signature, SourceType: EvidenceUserObservation,
			Summary: "脱敏后的用户日志命中错误特征：" + signature,
		}},
		VerificationSteps: cloneSteps(playbook.steps),
	}
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
		cause: "下游依赖响应时间超过当前调用链超时预算", confidence: 0.80,
		rationale: "deadline exceeded 只能证明预算耗尽，仍需区分下游变慢、网络阻塞和超时设置过小。",
		steps: []VerificationStep{
			verify("compare_latency", ActionCompare, "比较同一 Trace 中上游超时阈值与下游 P95/P99 延迟。", "超时阈值高于正常尾延迟且请求在预算内完成。", "若下游尾延迟超过阈值，应继续定位慢调用而非盲目扩大超时。"),
			verify("inspect_dependency", ActionInspect, "检查超时窗口内依赖服务的错误率、队列积压和资源水位。", "依赖无错误峰值且无明显积压。", "异常峰值可缩小到具体依赖或资源瓶颈。"),
		},
	},
	"redis_noauth": {
		cause: "Redis 已启用认证，但客户端未携带有效凭据或连接到了错误实例", confidence: 0.95,
		rationale: "NOAUTH 是 Redis 服务端返回的明确认证错误。",
		steps: []VerificationStep{
			verify("inspect_redis_target", ActionInspect, "核对脱敏后的 Redis 主机、端口、数据库号和认证配置来源。", "运行时配置指向预期 Redis 实例且认证字段已注入。", "配置缺失或目标不一致会直接产生认证失败。"),
			verify("query_redis_ping", ActionQuery, "使用应用相同的只读连接配置执行 PING，不输出凭据。", "返回 PONG。", "仍返回 NOAUTH 说明凭据缺失、错误或未被应用加载。"),
		},
	},
	"redis_wrongtype": {
		cause: "应用以错误的数据结构操作了已存在的 Redis Key", confidence: 0.94,
		rationale: "WRONGTYPE 明确表示命令与目标 Key 的实际类型不兼容。",
		steps: []VerificationStep{
			verify("query_key_type", ActionQuery, "对脱敏后的目标 Key 执行只读 TYPE 和 TTL 检查。", "Key 类型与当前命令预期一致。", "类型不一致说明命名空间复用、旧数据或迁移兼容问题。"),
			verify("compare_key_owner", ActionCompare, "比较所有写入该 Key 前缀的代码路径和版本。", "同一前缀只有一个稳定的数据结构契约。", "多个写入方使用不同结构会持续复现 WRONGTYPE。"),
		},
	},
	"mysql_access_denied": {
		cause: "MySQL 用户凭据、来源主机授权或目标实例配置不匹配", confidence: 0.94,
		rationale: "Access denied for user 是服务端在认证/授权阶段给出的明确拒绝。",
		steps: []VerificationStep{
			verify("inspect_mysql_target", ActionInspect, "核对脱敏后的 MySQL 用户、目标主机、端口和配置加载来源。", "运行时目标与授权实例一致。", "连接到错误实例或配置未刷新会造成持续拒绝。"),
			verify("query_mysql_identity", ActionQuery, "使用应用同源配置执行只读 SELECT USER(), CURRENT_USER()。", "连接成功且授权身份符合预期。", "失败或身份不一致说明用户/host 授权或凭据配置有误。"),
		},
	},
	"mysql_too_many_connections": {
		cause: "MySQL 连接数达到上限，可能由连接池配置或连接泄漏放大", confidence: 0.92,
		rationale: "1040/too many connections 表明服务端已拒绝新连接。",
		steps: []VerificationStep{
			verify("query_connection_watermark", ActionQuery, "只读查询当前连接数、max_connections 和按用户分布。", "连接水位显著低于上限且分布符合容量设计。", "接近上限时需定位最大连接来源。"),
			verify("compare_pool_limits", ActionCompare, "汇总各副本连接池 MaxOpenConns 与数据库上限。", "所有副本理论连接总数留有安全余量。", "理论总数超过上限说明容量配置不一致。"),
		},
	},
	"mysql_unknown_database": {
		cause: "应用配置的数据库名不存在或连接到了错误 MySQL 实例", confidence: 0.96,
		rationale: "Unknown database 直接指出目标实例中没有请求的 schema。",
		steps: []VerificationStep{
			verify("inspect_database_name", ActionInspect, "核对运行时数据库名和目标实例，避免输出凭据。", "数据库名与部署清单一致。", "名称或实例不一致即为主要配置缺口。"),
			verify("query_schema_presence", ActionQuery, "在目标实例只读查询 information_schema.schemata。", "目标 schema 存在。", "不存在说明初始化未执行或连接目标错误。"),
		},
	},
	"mysql_lock_wait_timeout": {
		cause: "事务等待行锁超过阈值，存在长事务或热点更新竞争", confidence: 0.90,
		rationale: "lock wait timeout 表明事务未能在预算内获得锁。",
		steps: []VerificationStep{
			verify("query_lock_waits", ActionQuery, "只读查看当前事务、锁等待和阻塞链。", "没有长期运行事务或持续阻塞链。", "阻塞链可定位持锁事务与竞争表。"),
			verify("compare_transaction_scope", ActionCompare, "比较问题路径的事务范围与慢查询时间。", "事务只覆盖必要数据库操作。", "事务内包含外部调用或批量工作会放大锁持有时间。"),
		},
	},
	"jwt_expired": {
		cause: "访问令牌已过期或签发端与验证端时钟偏差过大", confidence: 0.93,
		rationale: "expired 信号明确指向令牌有效期校验，但仍需排除系统时钟漂移。",
		steps: []VerificationStep{
			verify("inspect_token_time", ActionInspect, "只读取令牌 exp/iat 元数据并与服务端当前时间比较，不展示完整令牌。", "当前时间位于合法有效期内。", "超出有效期需重新登录；明显偏差需校准时钟。"),
		},
	},
	"jwt_signature_invalid": {
		cause: "令牌签名密钥、算法或签发环境与验证端不一致", confidence: 0.92,
		rationale: "invalid signature 说明完整性校验失败。",
		steps: []VerificationStep{
			verify("compare_jwt_config", ActionCompare, "比较签发端与验证端的算法、kid 和密钥版本标识，不输出密钥。", "两端使用相同算法和有效密钥版本。", "版本或算法不一致会使所有对应令牌验签失败。"),
		},
	},
	"dns_name_not_found": {
		cause: "调用方环境无法解析目标服务名，可能是容器网络或 DNS 配置不一致", confidence: 0.91,
		rationale: "no such host 表明连接在名称解析阶段失败。",
		steps: []VerificationStep{
			verify("query_dns_from_caller", ActionQuery, "从实际调用方容器只读解析目标服务名。", "解析到预期网络中的地址。", "解析失败说明服务名、网络加入关系或 DNS 配置错误。"),
			verify("inspect_network_membership", ActionInspect, "查看调用方与目标容器加入的 Docker 网络。", "双方至少共享一个预期网络。", "没有共享网络时容器服务名通常不可解析或不可达。"),
		},
	},
	"container_oom_killed": {
		cause: "容器内存超过限制并被内核终止", confidence: 0.98,
		rationale: "OOMKilled=true 或退出码 137 是强故障信号。",
		steps: []VerificationStep{
			verify("inspect_container_state", ActionInspect, "只读查看容器 State.OOMKilled、ExitCode 和内存限制。", "OOMKilled=false 且退出码不是 137。", "OOMKilled=true 基本确认内存超限终止。"),
			verify("compare_memory_peak", ActionCompare, "比较故障前内存峰值、限制和 Go 堆/非堆指标。", "峰值低于限制并留有余量。", "接近限制可进一步区分并发峰值、缓存增长或泄漏。"),
		},
	},
	"filesystem_no_space": {
		cause: "容器可写层或挂载卷空间/ inode 耗尽", confidence: 0.97,
		rationale: "no space left on device 是文件系统返回的明确容量错误。",
		steps: []VerificationStep{
			verify("query_disk_usage", ActionQuery, "只读检查目标路径所在文件系统的容量和 inode 使用率。", "空间和 inode 均低于告警阈值。", "任一达到上限都可解释写入失败。"),
			verify("inspect_growth_sources", ActionInspect, "查看日志、构建缓存和容器可写层的目录占用排行。", "不存在异常增长目录。", "占用排行可定位主要增长来源。"),
		},
	},
	"rabbitmq_not_found": {
		cause: "RabbitMQ 队列未声明、vhost 不一致或名称配置不匹配", confidence: 0.92,
		rationale: "NOT_FOUND - no queue 表明当前 vhost 中找不到目标队列。",
		steps: []VerificationStep{
			verify("inspect_queue_vhost", ActionInspect, "只读核对连接 vhost、队列名和声明方状态。", "目标队列存在于应用连接的 vhost。", "队列缺失或 vhost 不一致会直接触发 NOT_FOUND。"),
		},
	},
	"rabbitmq_precondition_failed": {
		cause: "同名 RabbitMQ 队列/交换机的声明参数与既有实体不一致", confidence: 0.94,
		rationale: "PRECONDITION_FAILED 常见于 durable、auto-delete、arguments 等声明契约冲突。",
		steps: []VerificationStep{
			verify("compare_rabbit_declaration", ActionCompare, "比较应用声明参数与服务端现有实体属性。", "同名实体的 durability、类型和 arguments 完全一致。", "任一属性不一致都会导致声明被拒绝。"),
		},
	},
	"http_401": {
		cause: "请求缺少有效认证信息，或令牌被网关/后端拒绝", confidence: 0.82,
		rationale: "401 表示请求未通过认证，但需结合响应方定位责任边界。",
		steps: []VerificationStep{
			verify("inspect_auth_boundary", ActionInspect, "根据 Trace 只读确认 401 由网关还是应用返回，并检查认证头是否到达。", "认证头到达预期验证组件且令牌元数据有效。", "头丢失或验证端拒绝可定位具体边界。"),
		},
	},
	"http_404": {
		cause: "请求路径、方法、代理前缀或后端路由版本不一致", confidence: 0.78,
		rationale: "404 只说明目标路由未匹配，需要确认由哪一层返回。",
		steps: []VerificationStep{
			verify("compare_route", ActionCompare, "比较浏览器请求 URL/方法、代理改写规则和后端注册路由。", "三层路径和方法一致。", "任一前缀或方法差异都可能产生 404。"),
		},
	},
	"http_413": {
		cause: "上传请求体超过代理或应用配置的大小限制", confidence: 0.94,
		rationale: "413/request entity too large 明确表示请求体大小被拒绝。",
		steps: []VerificationStep{
			verify("compare_upload_limits", ActionCompare, "比较实际文件大小、代理 body 限制和应用上传限制。", "文件大小同时低于各层限制。", "最小的限制值即当前拒绝边界。"),
		},
	},
	"http_502": {
		cause: "反向代理无法从上游获得有效响应，上游可能未启动、地址错误或异常退出", confidence: 0.88,
		rationale: "Bad Gateway 表明代理存在，但其上游链路失败。",
		steps: []VerificationStep{
			verify("inspect_upstream", ActionInspect, "核对代理 upstream 地址并查看同一时刻后端进程/容器状态。", "upstream 与后端监听地址一致且服务健康。", "地址不一致或后端退出会造成 502。"),
			verify("query_upstream_health", ActionQuery, "从代理所在网络直接请求后端健康接口。", "返回健康状态。", "失败可将故障收敛到代理到后端的路径。"),
		},
	},
	"http_504": {
		cause: "反向代理等待上游响应超过超时阈值", confidence: 0.87,
		rationale: "Gateway Timeout 表明代理已等待上游但未在预算内收到响应。",
		steps: []VerificationStep{
			verify("compare_proxy_latency", ActionCompare, "比较代理超时、后端 Trace 耗时和下游依赖耗时。", "完整调用链在代理超时内完成。", "超时集中在哪一段即可定位慢依赖或预算不一致。"),
		},
	},
	"cors_rejected": {
		cause: "前端 Origin、预检请求或服务端 CORS 响应策略不匹配", confidence: 0.91,
		rationale: "浏览器 CORS 拒绝发生在跨源访问策略校验阶段。",
		steps: []VerificationStep{
			verify("inspect_cors_exchange", ActionInspect, "查看浏览器 Network 中 Origin、OPTIONS 状态及 CORS 响应头。", "预检成功且 Allow-Origin/Methods/Headers 覆盖实际请求。", "缺失或不匹配的响应头即为拒绝边界。"),
		},
	},
	"sse_content_type_invalid": {
		cause: "SSE 响应被代理或后端返回为非 text/event-stream 类型", confidence: 0.93,
		rationale: "EventSource 对响应 Content-Type 有明确要求。",
		steps: []VerificationStep{
			verify("inspect_sse_headers", ActionInspect, "查看流式接口响应状态、Content-Type、缓存和代理缓冲头。", "Content-Type 为 text/event-stream 且代理缓冲关闭。", "错误页、JSON 或 text/plain 会使浏览器拒绝建立事件流。"),
		},
	},
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
