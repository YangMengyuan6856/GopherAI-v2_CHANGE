package toolruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

const (
	logTailByteLimit = 256 * 1024
	logMatchLimit    = 20
	logExcerptRunes  = 320
)

var (
	logSignatureTokens = map[string][]string{
		"panic":      {"panic:", "runtime error", "fatal"},
		"auth":       {"noauth", "unauthorized", "forbidden", "authentication", "access denied", "invalid jwt", "token expired"},
		"timeout":    {"timeout", "timed out", "deadline exceeded", "context canceled", "context cancelled"},
		"connection": {"connection refused", "connection reset", "broken pipe", "dial tcp", "no route to host", "unexpected eof"},
		"error":      {"error", "failed", "failure", "not_ready", "status=5", "status 5"},
		"warning":    {"warning", "warn", "slow sql", "retry", "degraded"},
	}
	ansiEscapePattern  = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	urlCredential      = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/@\s]+@`)
	bearerCredential   = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/-]+=*`)
	keyValueCredential = regexp.MustCompile(`(?i)\b(password|passwd|pwd|token|secret|authorization|api[_-]?key|smtp[_-]?auth)\s*[:=]\s*("[^"]*"|'[^']*'|[^\s,;]+)`)
	dsnCredential      = regexp.MustCompile(`(?i)\b[a-z0-9._-]+:[^@\s]+@(tcp|unix|\()`)
	jwtCredential      = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}(?:\.[A-Za-z0-9_-]{4,})?\b`)
	emailAddress       = regexp.MustCompile(`(?i)\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b`)
	sourceFilePath     = regexp.MustCompile(`(?i)(?:[A-Z]:[\\/]|/)(?:[^\s:\\]+[\\/])+([^\s:\\/]+\.go):(\d+)`)
)

type BoundedLogSignatureTool struct {
	basePath string
	paths    map[string]string
}

type PublicLogMatch struct {
	ScannedLine int    `json:"scanned_line"`
	LineHash    string `json:"line_hash"`
	Excerpt     string `json:"excerpt"`
}

type PublicLogSignatureSnapshot struct {
	Service       string           `json:"service"`
	Signature     string           `json:"signature"`
	Source        string           `json:"source"`
	ScannedBytes  int              `json:"scanned_bytes"`
	ScannedLines  int              `json:"scanned_lines"`
	MatchedLines  int              `json:"matched_lines"`
	ReturnedLines int              `json:"returned_lines"`
	Truncated     bool             `json:"truncated"`
	Matches       []PublicLogMatch `json:"matches"`
}

func NewBoundedLogSignatureTool(basePath string) *BoundedLogSignatureTool {
	return &BoundedLogSignatureTool{basePath: basePath, paths: map[string]string{
		"backend": "backend.log", "index_worker": "index-worker.log", "mcp": filepath.Join("common", "mcp", "mcp.log"),
	}}
}

func (tool *BoundedLogSignatureTool) Definition() Definition {
	return Definition{
		Name: "bounded_log_signature", Version: "1.0.0",
		Description: "扫描固定 allowlist 服务日志的有界尾部窗口，按预定义故障签名返回脱敏摘录和哈希；不接受路径、正则或 Shell。",
		InputSchema: InputSchema{Type: "object", Properties: map[string]PropertySchema{
			"service":   {Type: "string", Description: "固定日志来源", Enum: []string{"backend", "index_worker", "mcp"}, MinLength: 3, MaxLength: 12},
			"signature": {Type: "string", Description: "预定义故障签名", Enum: []string{"panic", "auth", "timeout", "connection", "error", "warning"}, MinLength: 4, MaxLength: 10},
		}, Required: []string{"service", "signature"}, AdditionalProperties: false},
		AllowedIntents: []string{"tool_task", "troubleshooting"}, RequiredPermission: "devsupport:tools:read",
		SideEffect: SideEffectReadOnly, TimeoutMS: 1000, MaxResultBytes: 32768,
		Idempotent: true, RetryMaxAttempts: 2, CacheTTLMS: 1000, StaleIfErrorMS: 5000, CircuitFailures: 3, CircuitOpenMS: 3000,
	}
}

func (tool *BoundedLogSignatureTool) Execute(ctx context.Context, arguments map[string]any) (Output, error) {
	if err := ctx.Err(); err != nil {
		return Output{}, err
	}
	service, _ := arguments["service"].(string)
	signature, _ := arguments["signature"].(string)
	tokens, knownSignature := logSignatureTokens[signature]
	relativePath, knownService := tool.paths[service]
	if !knownService || !knownSignature {
		return Output{}, errors.New("log source or signature is outside the fixed allowlist")
	}
	file, err := tool.openAllowlisted(relativePath)
	if err != nil {
		return Output{Retryable: true}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Output{Retryable: true}, fmt.Errorf("stat allowlisted log: %w", err)
	}
	start := info.Size() - logTailByteLimit
	if start < 0 {
		start = 0
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return Output{Retryable: true}, fmt.Errorf("seek allowlisted log: %w", err)
	}
	contents, err := io.ReadAll(io.LimitReader(file, logTailByteLimit+1))
	if err != nil {
		return Output{Retryable: true}, fmt.Errorf("read allowlisted log: %w", err)
	}
	if len(contents) > logTailByteLimit {
		contents = contents[:logTailByteLimit]
	}
	if start > 0 {
		if firstNewline := strings.IndexByte(string(contents), '\n'); firstNewline >= 0 {
			contents = contents[firstNewline+1:]
		} else {
			contents = nil
		}
	}
	lines := strings.Split(string(contents), "\n")
	matches := make([]PublicLogMatch, 0, logMatchLimit)
	matchedLines := 0
	for index, line := range lines {
		if err := ctx.Err(); err != nil {
			return Output{}, err
		}
		if !matchesSignature(line, tokens) {
			continue
		}
		matchedLines++
		sanitized := sanitizeLogExcerpt(line)
		digest := sha256.Sum256([]byte(sanitized))
		match := PublicLogMatch{ScannedLine: index + 1, LineHash: hex.EncodeToString(digest[:]), Excerpt: sanitized}
		if len(matches) == logMatchLimit {
			copy(matches, matches[1:])
			matches[len(matches)-1] = match
		} else {
			matches = append(matches, match)
		}
	}
	contentDigest := sha256.Sum256(contents)
	snapshot := PublicLogSignatureSnapshot{
		Service: service, Signature: signature, Source: service + "_application_log", ScannedBytes: len(contents), ScannedLines: len(lines),
		MatchedLines: matchedLines, ReturnedLines: len(matches), Truncated: start > 0 || matchedLines > len(matches), Matches: matches,
	}
	return Output{Data: snapshot, EvidenceRefs: []string{"log-signature:" + service + ":" + signature + ":" + hex.EncodeToString(contentDigest[:8])}}, nil
}

func (tool *BoundedLogSignatureTool) openAllowlisted(relativePath string) (*os.File, error) {
	base, err := filepath.Abs(tool.basePath)
	if err != nil {
		return nil, fmt.Errorf("resolve log root: %w", err)
	}
	candidate := filepath.Join(base, relativePath)
	entry, err := os.Lstat(candidate)
	if err != nil {
		return nil, fmt.Errorf("inspect allowlisted log: %w", err)
	}
	if !entry.Mode().IsRegular() {
		return nil, errors.New("allowlisted log must be a regular non-symlink file")
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return nil, fmt.Errorf("resolve allowlisted log: %w", err)
	}
	relative, err := filepath.Rel(base, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, errors.New("resolved log path escaped the allowlisted root")
	}
	file, err := os.Open(resolved)
	if err != nil {
		return nil, fmt.Errorf("open allowlisted log: %w", err)
	}
	return file, nil
}

func matchesSignature(line string, tokens []string) bool {
	lower := strings.ToLower(line)
	for _, token := range tokens {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func sanitizeLogExcerpt(line string) string {
	line = ansiEscapePattern.ReplaceAllString(line, "")
	line = urlCredential.ReplaceAllString(line, `${1}***:***@`)
	line = bearerCredential.ReplaceAllString(line, "Bearer [REDACTED]")
	line = keyValueCredential.ReplaceAllString(line, `${1}=[REDACTED]`)
	line = dsnCredential.ReplaceAllString(line, `***:***@$1`)
	line = jwtCredential.ReplaceAllString(line, "[REDACTED_JWT]")
	line = emailAddress.ReplaceAllString(line, "[REDACTED_EMAIL]")
	line = sourceFilePath.ReplaceAllString(line, `<source>/$1:$2`)
	line = strings.Map(func(value rune) rune {
		if value == '\t' || unicode.IsPrint(value) {
			return value
		}
		return -1
	}, strings.TrimSpace(line))
	runes := []rune(line)
	if len(runes) > logExcerptRunes {
		line = string(runes[:logExcerptRunes]) + "…"
	}
	return line
}
