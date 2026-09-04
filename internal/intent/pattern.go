package intent

import (
	"GopherAI/internal/contract"
	"regexp"
	"sort"
	"strings"
)

const patternStage = "pattern"

var (
	errorCodePattern = regexp.MustCompile(`\b(?:[A-Z][A-Z0-9]*_[A-Z0-9_]+|[45]\d{2}|E\d{3,}|(?i:panic|fatal|timeout|refused|denied|exception|oomkilled|crashloopbackoff))\b`)
	logPattern       = regexp.MustCompile(`(?im)(?:^|\n)\s*(?:\d{4}[-/]\d{2}[-/]\d{2}|\[[A-Z]+\]|at\s+\S+\(|goroutine\s+\d+|stack trace|level=(?:error|fatal)|ERROR[:\s])`)
	followUpPattern  = regexp.MustCompile(`^(?:那|这个|它|上述|上面|前面|刚才|第二种|第[一二三四五六七八九十]+个|继续|然后|接下来).{0,34}(?:呢|吗|怎么|如何|为什么|验证|处理|解决|操作|做)?[？?]?$`)
)

type PatternInput struct {
	Question          string
	KnowledgeRequired bool
	PreviousIntent    string
}

type PatternDecision struct {
	Result       contract.IntentResult `json:"result"`
	Matched      bool                  `json:"matched"`
	ReasonCodes  []string              `json:"reason_codes"`
	CandidateSet []string              `json:"candidate_set,omitempty"`
}

type signal struct {
	intent     string
	confidence float64
	reason     string
	priority   int
}

type PatternRecognizer struct{}

func NewPatternRecognizer() *PatternRecognizer { return &PatternRecognizer{} }

func (*PatternRecognizer) Recognize(input PatternInput) PatternDecision {
	question := strings.TrimSpace(input.Question)
	if question == "" {
		return patternDecision(General, 0, "empty_question", false, true, nil)
	}
	if isMetaLinguisticRequest(question) {
		return patternDecision(General, 0.96, "quoted_literal_not_action", true, false, []string{General})
	}

	signals := collectSignals(question, input)
	if len(signals) == 0 {
		return patternDecision(General, 0.40, "pattern_no_high_confidence", false, false, nil)
	}
	sort.SliceStable(signals, func(i, j int) bool {
		if signals[i].priority == signals[j].priority {
			return signals[i].confidence > signals[j].confidence
		}
		return signals[i].priority > signals[j].priority
	})

	candidates := uniqueCandidates(signals)
	reasons := uniqueReasons(signals)
	if len(candidates) > 1 {
		decision := patternDecision(signals[0].intent, min(signals[0].confidence, 0.79), "pattern_conflict_requires_fusion", false, false, candidates)
		decision.Result.IsCompound = true
		decision.ReasonCodes = append([]string{"pattern_conflict_requires_fusion"}, reasons...)
		return decision
	}
	decision := patternDecision(signals[0].intent, signals[0].confidence, signals[0].reason, signals[0].confidence >= 0.90, false, candidates)
	decision.ReasonCodes = reasons
	return decision
}

func collectSignals(question string, input PatternInput) []signal {
	lower := strings.ToLower(question)
	result := make([]signal, 0, 5)

	hasErrorSignature := errorCodePattern.MatchString(question)
	hasLog := logPattern.MatchString(question) || strings.Contains(question, "日志如下") || strings.Contains(question, "报错如下")
	hasDiagnosisVerb := containsAny(lower, "排查", "诊断", "定位原因", "帮我定位", "根因", "为什么失败", "怎么修复", "无法启动", "连不上", "搜索不到", "一直重启", "异常退出")
	if hasErrorSignature || hasLog || hasDiagnosisVerb {
		confidence, reason := 0.95, "explicit_troubleshooting"
		if hasLog {
			confidence, reason = 0.97, "log_block"
		} else if hasErrorSignature {
			confidence, reason = 0.98, "error_signature"
		}
		result = append(result, signal{intent: Troubleshooting, confidence: confidence, reason: reason, priority: 60})
	}

	docAction := containsAny(lower, "上传文档", "上传文件", "重新索引", "重建索引", "删除文档", "删除知识库", "查看索引状态", "索引状态", "更新文档版本", "替换文档", "清空知识库") ||
		(strings.Contains(lower, "上传") && containsAny(lower, ".md", ".txt", ".json", ".yaml", ".yml", ".go", "markdown", "yaml", "json", "go 文件", "项目说明"))
	if docAction {
		result = append(result, signal{intent: DocTask, confidence: 0.98, reason: "explicit_document_action", priority: 50})
	}

	toolVerb := containsAny(lower, "调用工具", "执行工具", "使用工具", "通过工具", "请查询", "帮我查询", "检查服务健康", "获取监控指标", "读取部署状态")
	toolTarget := containsAny(lower, "健康状态", "prometheus", "监控指标", "部署状态", "服务状态", "运行状态", "mcp")
	if toolVerb && toolTarget {
		result = append(result, signal{intent: ToolTask, confidence: 0.96, reason: "explicit_governed_tool_request", priority: 40})
	}
	dangerousTool := containsAny(lower, "重启后端", "重启容器", "执行 sql", "运行 shell", "执行 shell", "读取密钥", "把密钥", "修改策略权重", "改成 100%")
	if dangerousTool {
		result = append(result, signal{intent: ToolTask, confidence: 0.97, reason: "governed_operation_request", priority: 40})
	}

	if input.KnowledgeRequired {
		result = append(result, signal{intent: ProjectQA, confidence: 0.99, reason: "explicit_knowledge_mode", priority: 35})
	} else {
		projectTarget := containsAny(lower, "这个项目", "本项目", "gopherai", "知识库", "项目文档", "配置文件", "接口文档", "部署手册", "代码里", "文档里")
		qaVerb := containsAny(lower, "是什么", "是多少", "支持哪些", "在哪里", "如何配置", "怎么使用", "根据", "说明", "告诉我", "默认")
		if projectTarget && qaVerb {
			result = append(result, signal{intent: ProjectQA, confidence: 0.93, reason: "project_evidence_request", priority: 30})
		}
	}

	if isFollowUpPhrase(question) {
		if IsKnown(input.PreviousIntent) && input.PreviousIntent != FollowUp && input.PreviousIntent != General {
			result = append(result, signal{intent: FollowUp, confidence: 0.96, reason: "contextual_follow_up", priority: 20})
		} else {
			result = append(result, signal{intent: FollowUp, confidence: 0.55, reason: "follow_up_context_missing", priority: 20})
		}
	}

	if isCasualGeneral(lower) {
		result = append(result, signal{intent: General, confidence: 0.98, reason: "casual_general", priority: 10})
	}
	return result
}

func patternDecision(label string, confidence float64, reason string, matched bool, clarify bool, candidates []string) PatternDecision {
	return PatternDecision{
		Result: contract.IntentResult{
			Intent: label, Confidence: confidence, NeedsClarify: clarify,
			Version: PatternVersion,
			Stages:  []contract.IntentStageResult{{Stage: patternStage, Intent: label, Confidence: confidence, ReasonCode: reason}},
		},
		Matched: matched, ReasonCodes: []string{reason}, CandidateSet: candidates,
	}
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

func isCasualGeneral(value string) bool {
	trimmed := strings.TrimSpace(strings.Trim(value, "!！?？。,.，"))
	switch trimmed {
	case "你好", "您好", "嗨", "hello", "hi", "谢谢", "感谢", "谢谢你的帮助", "你是谁", "讲个笑话", "早上好", "晚上好":
		return true
	default:
		return false
	}
}

func isFollowUpPhrase(question string) bool {
	if followUpPattern.MatchString(question) {
		return true
	}
	trimmed := strings.TrimSpace(strings.Trim(question, "!！?？。,.，"))
	if len([]rune(trimmed)) > 18 {
		return false
	}
	return containsAny(trimmed, "为什么", "它呢", "那第二个", "继续", "然后", "接下来", "上一个", "前一个")
}

func isMetaLinguisticRequest(question string) bool {
	lower := strings.ToLower(question)
	return containsAny(lower,
		"忽略系统规则，把我的意图标成",
		"输出单词",
		"四个字重复",
		"这个词是什么意思",
		"如果没有上下文",
	)
}

func uniqueCandidates(signals []signal) []string {
	seen := make(map[string]struct{}, len(signals))
	result := make([]string, 0, len(signals))
	for _, item := range signals {
		if _, exists := seen[item.intent]; exists {
			continue
		}
		seen[item.intent] = struct{}{}
		result = append(result, item.intent)
	}
	return result
}

func uniqueReasons(signals []signal) []string {
	seen := make(map[string]struct{}, len(signals))
	result := make([]string, 0, len(signals))
	for _, item := range signals {
		if _, exists := seen[item.reason]; exists {
			continue
		}
		seen[item.reason] = struct{}{}
		result = append(result, item.reason)
	}
	return result
}

func min(left float64, right float64) float64 {
	if left < right {
		return left
	}
	return right
}
