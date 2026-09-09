package diagnostic

import (
	"errors"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	ExtractorVersion       = "diagnostic-extractor-v1"
	maxDiagnosticInputRune = 32000
	maxDiagnosticExcerpt   = 8000
	maxDiagnosticSymptom   = 1000
	maxDiagnosticLine      = 500
)

var ErrEmptyDiagnosticInput = errors.New("diagnostic input is empty")

type ExtractedInput struct {
	Version                 string            `json:"version"`
	Symptom                 string            `json:"symptom"`
	Components              []string          `json:"components"`
	ErrorSignatures         []string          `json:"error_signatures"`
	KnownEnvironment        []EnvironmentFact `json:"known_environment"`
	SanitizedExcerpt        string            `json:"sanitized_excerpt"`
	InputTruncated          bool              `json:"input_truncated"`
	OutputTruncated         bool              `json:"output_truncated"`
	RedactionCount          int               `json:"redaction_count"`
	IgnoredInstructionCount int               `json:"ignored_instruction_count"`
}

type Extractor struct{}

// SanitizeFreeText applies the same secret redaction, instruction filtering and
// rune bounds used by diagnostic input before text is admitted to reusable
// memory. The caller chooses a smaller field-specific bound.
func SanitizeFreeText(raw string, maximum int) (string, int, error) {
	if maximum <= 0 || maximum > maxDiagnosticExcerpt {
		return "", 0, errors.New("sanitization bound is invalid")
	}
	normalized := normalizeDiagnosticText(raw)
	normalized, _ = truncateRunes(normalized, maxDiagnosticInputRune)
	normalized, redactions := redactDiagnosticText(normalized)
	lines := make([]string, 0, 16)
	for _, line := range strings.Split(normalized, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || isInstructionLike(line) {
			continue
		}
		line, _ = truncateRunes(line, maxDiagnosticLine)
		lines = append(lines, line)
	}
	value, _ := truncateRunes(strings.TrimSpace(strings.Join(lines, "\n")), maximum)
	if value == "" {
		return "", redactions, ErrEmptyDiagnosticInput
	}
	return value, redactions, nil
}

type namedPattern struct {
	name    string
	pattern *regexp.Regexp
}

var (
	privateKeyPattern = regexp.MustCompile(`(?is)-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----.*?(?:-----END (?:RSA |EC |OPENSSH )?PRIVATE KEY-----|$)`)
	bearerPattern     = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{8,}`)
	jwtPattern        = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)
	credentialPattern = regexp.MustCompile(`(?i)\b(password|passwd|secret|token|api[_-]?key|access[_-]?key|authorization)\s*([:=])\s*(?:"[^"]*"|'[^']*'|[^\s,;]+)`)
	uriCredential     = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/\s:@]+:[^/\s@]+@`)
	emailPattern      = regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`)

	errorPatterns = []namedPattern{
		{"connection_refused", regexp.MustCompile(`(?i)connection refused|connect(?::\s*connection)? refused|连接[^\n]{0,80}被拒绝`)},
		{"context_deadline_exceeded", regexp.MustCompile(`(?i)context deadline exceeded|i/o timeout`)},
		{"redis_noauth", regexp.MustCompile(`(?i)\bNOAUTH\b|authentication required`)},
		{"redis_wrongtype", regexp.MustCompile(`(?i)\bWRONGTYPE\b`)},
		{"mysql_access_denied", regexp.MustCompile(`(?i)access denied for user`)},
		{"mysql_too_many_connections", regexp.MustCompile(`(?i)too many connections|error\s*1040`)},
		{"mysql_unknown_database", regexp.MustCompile(`(?i)unknown database`)},
		{"mysql_lock_wait_timeout", regexp.MustCompile(`(?i)lock wait timeout exceeded`)},
		{"jwt_expired", regexp.MustCompile(`(?i)token is expired|jwt.*expired`)},
		{"jwt_signature_invalid", regexp.MustCompile(`(?i)signature is invalid|invalid signature`)},
		{"dns_name_not_found", regexp.MustCompile(`(?i)no such host|name or service not known`)},
		{"container_oom_killed", regexp.MustCompile(`(?i)OOMKilled\s*[:=]\s*true|exit(?:ed)?\s+(?:code\s*)?137`)},
		{"filesystem_no_space", regexp.MustCompile(`(?i)no space left on device`)},
		{"rabbitmq_not_found", regexp.MustCompile(`(?i)NOT_FOUND\s*-\s*no queue`)},
		{"rabbitmq_precondition_failed", regexp.MustCompile(`(?i)PRECONDITION_FAILED`)},
		{"http_401", regexp.MustCompile(`(?i)(?:(?:http(?: status)?|status(?: code)?|returned?|返回|报)\s*[:=]?\s*)?\b401\b`)},
		{"http_404", regexp.MustCompile(`(?i)(?:(?:http(?: status)?|status(?: code)?|returned?|返回|报)\s*[:=]?\s*)?\b404\b`)},
		{"http_413", regexp.MustCompile(`(?i)\b413\b.*request entity too large|request entity too large`)},
		{"http_502", regexp.MustCompile(`(?i)\b502\b|bad gateway`)},
		{"http_504", regexp.MustCompile(`(?i)\b504\b|gateway timeout`)},
		{"cors_rejected", regexp.MustCompile(`(?i)Access-Control-Allow-Origin|blocked by CORS`)},
		{"sse_content_type_invalid", regexp.MustCompile(`(?i)text/plain.*text/event-stream|MIME type.*event-stream`)},
		{"authorization_header_missing", regexp.MustCompile(`(?i)missing Authorization header|请求头里没有 Bearer|authorization header (?:is )?missing`)},
		{"bearer_null", regexp.MustCompile(`(?i)Bearer\s+(?:null|undefined)|本地存储中没有访问令牌`)},
		{"host_port_allocated", regexp.MustCompile(`(?i)port is already allocated|address already in use|端口.*(?:已被占用|占用)`)},
		{"redis_latency_spike", regexp.MustCompile(`(?i)Redis.*(?:(?:延迟|latency).*(?:升到|spike|slowlog)|20ms.*2s)|(?:延迟|latency).*Redis.*(?:2s|spike)`)},
		{"rabbitmq_unacked_growth", regexp.MustCompile(`(?i)unacked.*(?:增长|growing)|(?:增长|growing).*unacked`)},
		{"index_worker_missing", regexp.MustCompile(`(?i)(?:看不到|没有|missing).*(?:index[-_ ]worker|索引\s*Worker).*(?:进程|process)|(?:index[-_ ]worker|索引\s*Worker).*(?:not running|进程.*(?:不存在|没有))`)},
		{"rag_zero_chunks", regexp.MustCompile(`(?i)chunk_count\s*(?:为|=|:)\s*0|(?:chunk|片段).*(?:为\s*0|zero|没有)`)},
		{"rag_unauthorized_citation", regexp.MustCompile(`(?i)(?:引用|citation).*(?:(?:不在|unknown|unauthorized).*(?:证据|evidence)|(?:证据|evidence).*(?:只返回|only returned))|unknown citation`)},
		{"rag_failed_version_inactive", regexp.MustCompile(`(?i)(?:active_version\s*=\s*1.*(?:job\s*=\s*failed|v2.*failed))|(?:新版本|v2).*(?:失败|failed).*(?:旧版本|v1).*(?:生效|active|命中)`)},
		{"rag_possible_acl_leak", regexp.MustCompile(`(?i)(?:另一个租户|other tenant|cross.?tenant).*(?:文档|document).*(?:标题|title|搜到|search)`)},
		{"sse_disconnect", regexp.MustCompile(`(?i)(?:流式|SSE|stream).*(?:60\s*秒|断开|disconnect|timeout)`)},
		{"sse_duplicate_replay", regexp.MustCompile(`(?i)(?:两份|重复|duplicate).*(?:回答|response).*(?:重连|reconnect|request_id)|(?:重连|reconnect).*(?:复用|reuse).*(?:request_id|请求)`)},
		{"sse_utf8_boundary", regexp.MustCompile(`(?i)(?:(?:UTF-?8|多字节).*(?:chunk|边界|拆|解码)|(?:替换字符|TextDecoder).*(?:UTF-?8|多字节|chunk))`)},
		{"knowledge_no_evidence", regexp.MustCompile(`(?i)(?:检索结果为空|没有授权证据|no authorized evidence|knowledge base.*no evidence)`)},
		{"generic_service_busy", regexp.MustCompile(`(?i)服务繁忙|service unavailable|temporarily busy`)},
	}

	componentPatterns = []namedPattern{
		{"redis", regexp.MustCompile(`(?i)\bredis(?:-vector)?\b|\bNOAUTH\b|\bWRONGTYPE\b`)},
		{"mysql", regexp.MustCompile(`(?i)\bmysql\b|\binnodb\b|error\s*1040`)},
		{"rabbitmq", regexp.MustCompile(`(?i)\brabbitmq\b|\bamqp\b|PRECONDITION_FAILED`)},
		{"docker", regexp.MustCompile(`(?i)\bdocker\b|\bcontainer\b|OOMKilled|port is already allocated|容器`)},
		{"http_proxy", regexp.MustCompile(`(?i)\bnginx\b|\bproxy\b|\bupstream\b|bad gateway|gateway timeout|\b(?:502|504)\b`)},
		{"jwt", regexp.MustCompile(`(?i)\bjwt\b|\bbearer\b|token is expired|signature is invalid|Authorization header|访问令牌`)},
		{"index_worker", regexp.MustCompile(`(?i)index[-_ ]worker|索引\s*Worker`)},
		{"rag", regexp.MustCompile(`(?i)\bRAG\b|citation|evidence|检索|引用`)},
		{"frontend_sse", regexp.MustCompile(`(?i)\bSSE\b|EventSource|text/event-stream|浏览器|前端`)},
	}

	versionPatterns = []struct {
		key     string
		pattern *regexp.Regexp
	}{
		{"go_version", regexp.MustCompile(`(?i)\bgo\s*(1\.\d+(?:\.\d+)?)\b`)},
		{"mysql_version", regexp.MustCompile(`(?i)\bmysql\s*(\d+\.\d+(?:\.\d+)?)\b`)},
		{"redis_version", regexp.MustCompile(`(?i)\bredis\s*(\d+\.\d+(?:\.\d+)?)\b`)},
		{"docker_version", regexp.MustCompile(`(?i)\bdocker\s*(\d+\.\d+(?:\.\d+)?)\b`)},
		{"os", regexp.MustCompile(`(?i)\b((?:ubuntu|centos|debian|alpine)(?:\s+\d+(?:\.\d+)*)?|linux|windows(?:\s+\d+)?)\b`)},
	}

	cloudProviderPatterns = []struct {
		value   string
		pattern *regexp.Regexp
	}{
		{"aliyun", regexp.MustCompile(`(?i)阿里云|\baliyun\b|\balibaba cloud\b`)},
		{"aws", regexp.MustCompile(`(?i)\baws\b|\bamazon web services\b`)},
		{"azure", regexp.MustCompile(`(?i)\bazure\b`)},
		{"gcp", regexp.MustCompile(`(?i)\bgcp\b|\bgoogle cloud\b`)},
	}
)

func (Extractor) Extract(raw string) (ExtractedInput, error) {
	normalized := normalizeDiagnosticText(raw)
	if strings.TrimSpace(normalized) == "" {
		return ExtractedInput{}, ErrEmptyDiagnosticInput
	}
	result := ExtractedInput{Version: ExtractorVersion}
	normalized, result.InputTruncated = truncateRunes(normalized, maxDiagnosticInputRune)
	normalized, result.RedactionCount = redactDiagnosticText(normalized)

	safeLines := make([]string, 0, 64)
	for _, line := range strings.Split(normalized, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if isInstructionLike(line) {
			result.IgnoredInstructionCount++
			continue
		}
		var lineTruncated bool
		line, lineTruncated = truncateRunes(line, maxDiagnosticLine)
		result.OutputTruncated = result.OutputTruncated || lineTruncated
		safeLines = append(safeLines, line)
	}
	safeText := strings.Join(safeLines, "\n")
	if safeText == "" {
		return ExtractedInput{}, ErrEmptyDiagnosticInput
	}
	var excerptTruncated bool
	result.SanitizedExcerpt, excerptTruncated = truncateRunes(safeText, maxDiagnosticExcerpt)
	result.OutputTruncated = result.OutputTruncated || excerptTruncated
	result.Symptom, _ = truncateRunes(strings.ReplaceAll(result.SanitizedExcerpt, "\n", " "), maxDiagnosticSymptom)
	result.Components = matchNames(result.SanitizedExcerpt, componentPatterns)
	result.ErrorSignatures = matchNames(result.SanitizedExcerpt, errorPatterns)
	result.KnownEnvironment = extractEnvironment(result.SanitizedExcerpt)
	return result, nil
}

func normalizeDiagnosticText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			return r
		}
		return -1
	}, value)
}

func redactDiagnosticText(value string) (string, int) {
	count := 0
	replacements := []struct {
		pattern     *regexp.Regexp
		replacement string
	}{
		{privateKeyPattern, "[REDACTED_PRIVATE_KEY]"},
		{uriCredential, "$1[REDACTED]@"},
		{bearerPattern, "Bearer [REDACTED]"},
		{jwtPattern, "[REDACTED_JWT]"},
		{credentialPattern, "$1$2[REDACTED]"},
		{emailPattern, "[REDACTED_EMAIL]"},
	}
	for _, replacement := range replacements {
		count += len(replacement.pattern.FindAllStringIndex(value, -1))
		value = replacement.pattern.ReplaceAllString(value, replacement.replacement)
	}
	return value, count
}

func isInstructionLike(line string) bool {
	lower := strings.ToLower(line)
	markers := []string{
		"ignore previous instructions",
		"ignore all instructions",
		"system prompt",
		"developer message",
		"you are chatgpt",
		"act as ",
		"忽略之前的指令",
		"忽略以上指令",
		"忽略所有指令",
		"系统提示词",
		"你现在是",
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func matchNames(value string, patterns []namedPattern) []string {
	names := make([]string, 0, len(patterns))
	for _, item := range patterns {
		if item.pattern.MatchString(value) {
			names = append(names, item.name)
		}
	}
	sort.Strings(names)
	return names
}

func extractEnvironment(value string) []EnvironmentFact {
	facts := make([]EnvironmentFact, 0, len(versionPatterns)+1)
	for _, item := range versionPatterns {
		match := item.pattern.FindStringSubmatch(value)
		if len(match) == 2 {
			facts = append(facts, EnvironmentFact{Key: item.key, Value: match[1], Source: EvidenceUserObservation, Confidence: 0.7})
		}
	}
	lower := strings.ToLower(value)
	if strings.Contains(lower, "docker") || strings.Contains(lower, "container") || strings.Contains(value, "容器") {
		facts = append(facts, EnvironmentFact{Key: "deployment_mode", Value: "container", Source: EvidenceUserObservation, Confidence: 0.7})
	}
	for _, item := range cloudProviderPatterns {
		if item.pattern.MatchString(value) {
			facts = append(facts, EnvironmentFact{Key: "cloud_provider", Value: item.value, Source: EvidenceUserObservation, Confidence: 0.7})
			break
		}
	}
	sort.Slice(facts, func(i, j int) bool { return facts[i].Key < facts[j].Key })
	return facts
}

func truncateRunes(value string, maximum int) (string, bool) {
	if utf8.RuneCountInString(value) <= maximum {
		return value, false
	}
	runes := []rune(value)
	return string(runes[:maximum]), true
}
