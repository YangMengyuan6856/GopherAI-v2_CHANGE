package toolruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
)

const (
	officialDocumentResponseLimit = 256 * 1024
	officialDocumentMaxExcerpts   = 5
	officialDocumentExcerptRunes  = 360
)

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type officialDocument struct {
	Title        string
	CanonicalURL string
	FetchURL     string
}

var officialDocuments = map[string]officialDocument{
	"go_context_cancel":   {Title: "Go canceling in-progress operations", CanonicalURL: "https://go.dev/doc/database/cancel-operations"},
	"prometheus_alerting": {Title: "Prometheus alerting rules", CanonicalURL: "https://prometheus.io/docs/prometheus/latest/configuration/alerting_rules/"},
	"rabbitmq_dlx": {
		Title: "RabbitMQ dead letter exchanges", CanonicalURL: "https://www.rabbitmq.com/docs/dlx",
		FetchURL: "https://raw.githubusercontent.com/rabbitmq/rabbitmq-website/main/versioned_docs/version-4.3/dlx.md",
	},
	"redis_acl": {Title: "Redis access control lists", CanonicalURL: "https://redis.io/docs/latest/operate/oss_and_stack/management/security/acl/"},
}

type OfficialDocumentSearchTool struct {
	client    HTTPDoer
	documents map[string]officialDocument
}

type PublicOfficialDocumentEvidence struct {
	DocumentID   string   `json:"document_id"`
	Title        string   `json:"title"`
	SourceHost   string   `json:"source_host"`
	CanonicalURL string   `json:"canonical_url"`
	ContentHash  string   `json:"content_sha256"`
	SourceBytes  int      `json:"source_bytes"`
	MatchCount   int      `json:"match_count"`
	Excerpts     []string `json:"excerpts"`
}

func NewOfficialDocumentSearchTool() *OfficialDocumentSearchTool {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = secureOfficialDocumentDialer
	transport.ResponseHeaderTimeout = 2 * time.Second
	transport.TLSHandshakeTimeout = 2 * time.Second
	client := &http.Client{Transport: transport, CheckRedirect: officialDocumentRedirectPolicy}
	return &OfficialDocumentSearchTool{client: client, documents: officialDocuments}
}

func NewOfficialDocumentSearchToolWithClient(client HTTPDoer) *OfficialDocumentSearchTool {
	return &OfficialDocumentSearchTool{client: client, documents: officialDocuments}
}

func (tool *OfficialDocumentSearchTool) Definition() Definition {
	return Definition{
		Name: "official_document_search", Version: "1.0.0",
		Description: "从固定官方 Go/Redis/RabbitMQ/Prometheus 文档中检索有界证据摘录；不接受 URL、域名、路径或任意抓取目标。",
		InputSchema: InputSchema{Type: "object", Properties: map[string]PropertySchema{
			"document_id": {Type: "string", Description: "服务端固定官方文档标识", Enum: []string{"go_context_cancel", "prometheus_alerting", "rabbitmq_dlx", "redis_acl"}, MinLength: 9, MaxLength: 32},
			"query":       {Type: "string", Description: "文档内检索词，不作为 URL", MinLength: 2, MaxLength: 120},
		}, Required: []string{"document_id", "query"}, AdditionalProperties: false},
		AllowedIntents: []string{"tool_task", "troubleshooting"}, RequiredPermission: "devsupport:tools:read",
		SideEffect: SideEffectReadOnly, TimeoutMS: 3500, MaxResultBytes: 16384,
		Idempotent: true, RetryMaxAttempts: 2, CacheTTLMS: 60000, StaleIfErrorMS: 300000, CircuitFailures: 2, CircuitOpenMS: 5000,
	}
}

func (tool *OfficialDocumentSearchTool) Execute(ctx context.Context, arguments map[string]any) (Output, error) {
	documentID, _ := arguments["document_id"].(string)
	query, _ := arguments["query"].(string)
	if tool == nil || tool.client == nil {
		return Output{}, errors.New("official document source is unavailable")
	}
	document, allowed := tool.documents[documentID]
	if !allowed {
		return Output{}, errors.New("official document source is unavailable or outside allowlist")
	}
	fetchURL := document.FetchURL
	if fetchURL == "" {
		fetchURL = document.CanonicalURL
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fetchURL, nil)
	if err != nil {
		return Output{}, err
	}
	request.Header.Set("Accept", "text/html, text/plain;q=0.9")
	request.Header.Set("User-Agent", "GopherAI-DevSupport-OfficialDocs/1.0")
	response, err := tool.client.Do(request)
	if err != nil {
		return Output{Retryable: true}, fmt.Errorf("official document transport failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Output{Retryable: response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500}, fmt.Errorf("official document returned HTTP %d", response.StatusCode)
	}
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]))
	if mediaType != "text/html" && mediaType != "text/plain" {
		return Output{}, errors.New("official document content type is not allowed")
	}
	contents, err := io.ReadAll(io.LimitReader(response.Body, officialDocumentResponseLimit+1))
	if err != nil {
		return Output{Retryable: true}, fmt.Errorf("read official document: %w", err)
	}
	if len(contents) > officialDocumentResponseLimit {
		return Output{}, errors.New("official document exceeds response size limit")
	}
	text := string(contents)
	if mediaType == "text/html" {
		text, err = visibleHTMLText(contents)
		if err != nil {
			return Output{}, fmt.Errorf("parse official document: %w", err)
		}
	}
	excerpts := findDocumentExcerpts(text, query)
	digest := sha256.Sum256(contents)
	contentHash := hex.EncodeToString(digest[:])
	parsedURL, _ := url.Parse(fetchURL)
	evidence := PublicOfficialDocumentEvidence{
		DocumentID: documentID, Title: document.Title, SourceHost: parsedURL.Hostname(), CanonicalURL: document.CanonicalURL,
		ContentHash: contentHash, SourceBytes: len(contents), MatchCount: len(excerpts), Excerpts: excerpts,
	}
	return Output{Data: evidence, EvidenceRefs: []string{"official-doc:" + documentID + ":" + contentHash}}, nil
}

func officialDocumentRedirectPolicy(request *http.Request, via []*http.Request) error {
	if len(via) == 0 || len(via) > 3 {
		return http.ErrUseLastResponse
	}
	original := via[0].URL
	if request.URL.Scheme != "https" || !strings.EqualFold(request.URL.Hostname(), original.Hostname()) {
		return errors.New("official document redirect left the fixed HTTPS host")
	}
	return nil
}

func secureOfficialDocumentDialer(ctx context.Context, network string, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 2 * time.Second, KeepAlive: 30 * time.Second}
	for _, candidate := range addresses {
		if forbiddenOutboundIP(candidate.IP) {
			continue
		}
		connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
		if dialErr == nil {
			return connection, nil
		}
		err = dialErr
	}
	if err == nil {
		err = errors.New("official document host resolved only to forbidden addresses")
	}
	return nil, err
}

func forbiddenOutboundIP(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	for _, cidr := range []string{"100.64.0.0/10", "192.0.0.0/24", "198.18.0.0/15", "2001:db8::/32"} {
		_, network, _ := net.ParseCIDR(cidr)
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func visibleHTMLText(contents []byte) (string, error) {
	tokenizer := html.NewTokenizer(strings.NewReader(string(contents)))
	var builder strings.Builder
	skipDepth := 0
	for {
		tokenType := tokenizer.Next()
		switch tokenType {
		case html.ErrorToken:
			if errors.Is(tokenizer.Err(), io.EOF) {
				return strings.Join(strings.Fields(builder.String()), " "), nil
			}
			return "", tokenizer.Err()
		case html.StartTagToken:
			name, _ := tokenizer.TagName()
			if skipDepth > 0 {
				skipDepth++
			} else if isSkippedHTMLTag(string(name)) {
				skipDepth = 1
			}
		case html.EndTagToken:
			if skipDepth > 0 {
				skipDepth--
			}
		case html.TextToken:
			if skipDepth == 0 {
				builder.WriteByte(' ')
				builder.Write(tokenizer.Text())
			}
		}
	}
}

func isSkippedHTMLTag(name string) bool {
	switch strings.ToLower(name) {
	case "script", "style", "svg", "noscript", "template":
		return true
	default:
		return false
	}
}

func findDocumentExcerpts(documentText string, query string) []string {
	normalizedDocument := strings.Join(strings.Fields(documentText), " ")
	terms := queryTerms(query)
	if normalizedDocument == "" || len(terms) == 0 {
		return []string{}
	}
	documentRunes := []rune(normalizedDocument)
	lowerDocumentRunes := []rune(strings.ToLower(normalizedDocument))
	excerpts := make([]string, 0, officialDocumentMaxExcerpts)
	seen := make(map[string]struct{})
	for _, term := range terms {
		termRunes := []rune(term)
		searchFrom := 0
		for len(excerpts) < officialDocumentMaxExcerpts {
			runeIndex := indexRunes(lowerDocumentRunes, termRunes, searchFrom)
			if runeIndex < 0 {
				break
			}
			start := runeIndex - officialDocumentExcerptRunes/2
			if start < 0 {
				start = 0
			}
			end := start + officialDocumentExcerptRunes
			if end > len(documentRunes) {
				end = len(documentRunes)
			}
			excerpt := strings.TrimSpace(string(documentRunes[start:end]))
			if start > 0 {
				excerpt = "…" + excerpt
			}
			if end < len(documentRunes) {
				excerpt += "…"
			}
			if _, duplicate := seen[excerpt]; !duplicate {
				seen[excerpt] = struct{}{}
				excerpts = append(excerpts, excerpt)
			}
			searchFrom = runeIndex + len(termRunes)
		}
		if len(excerpts) == officialDocumentMaxExcerpts {
			break
		}
	}
	return excerpts
}

func indexRunes(document []rune, term []rune, start int) int {
	if len(term) == 0 || start < 0 || start > len(document) || len(term) > len(document)-start {
		return -1
	}
	for index := start; index+len(term) <= len(document); index++ {
		matched := true
		for offset := range term {
			if document[index+offset] != term[offset] {
				matched = false
				break
			}
		}
		if matched {
			return index
		}
	}
	return -1
}

func queryTerms(query string) []string {
	parts := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(query)), func(value rune) bool {
		return !unicode.IsLetter(value) && !unicode.IsDigit(value) && value != '-' && value != '_'
	})
	terms := make([]string, 0, 8)
	seen := make(map[string]struct{})
	for _, part := range parts {
		if utf8.RuneCountInString(part) < 2 {
			continue
		}
		if _, duplicate := seen[part]; duplicate {
			continue
		}
		seen[part] = struct{}{}
		terms = append(terms, part)
		if len(terms) == 8 {
			break
		}
	}
	return terms
}
