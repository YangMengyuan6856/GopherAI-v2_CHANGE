package toolruntime

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

type documentDoer func(*http.Request) (*http.Response, error)

func (doer documentDoer) Do(request *http.Request) (*http.Response, error) { return doer(request) }

func documentResponse(status int, contentType string, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestOfficialDocumentToolFetchesOnlyFixedSourceAndReturnsBoundedEvidence(t *testing.T) {
	var requestedURL string
	tool := NewOfficialDocumentSearchToolWithClient(documentDoer(func(request *http.Request) (*http.Response, error) {
		requestedURL = request.URL.String()
		if request.Header.Get("User-Agent") != "GopherAI-DevSupport-OfficialDocs/1.0" {
			t.Fatalf("unexpected user agent: %s", request.Header.Get("User-Agent"))
		}
		return documentResponse(http.StatusOK, "text/html; charset=utf-8", `<html><head><style>password=hidden</style><script>token=hidden</script></head><body><h1>Access Control Lists</h1><p>Use AUTH with a named ACL user and least privilege permissions.</p></body></html>`), nil
	}))
	output, err := tool.Execute(context.Background(), map[string]any{"document_id": "redis_acl", "query": "AUTH ACL"})
	if err != nil {
		t.Fatal(err)
	}
	result := output.Data.(PublicOfficialDocumentEvidence)
	if requestedURL != officialDocuments["redis_acl"].CanonicalURL || result.DocumentID != "redis_acl" || result.SourceHost != "redis.io" || result.MatchCount == 0 || len(result.ContentHash) != 64 {
		t.Fatalf("unexpected official evidence: %+v url=%s", result, requestedURL)
	}
	joined := strings.Join(result.Excerpts, " ")
	if strings.Contains(joined, "password=hidden") || strings.Contains(joined, "token=hidden") || len(joined) > officialDocumentMaxExcerpts*(officialDocumentExcerptRunes*4+8) {
		t.Fatalf("hidden or unbounded content escaped: %s", joined)
	}
	if len(output.EvidenceRefs) != 1 || !strings.HasPrefix(output.EvidenceRefs[0], "official-doc:redis_acl:") {
		t.Fatalf("missing evidence lineage: %+v", output.EvidenceRefs)
	}
}

func TestOfficialDocumentToolRejectsTypeSizeAndNonSuccess(t *testing.T) {
	cases := []struct {
		name      string
		response  *http.Response
		retryable bool
	}{
		{name: "content type", response: documentResponse(http.StatusOK, "application/json", `{}`)},
		{name: "oversized", response: documentResponse(http.StatusOK, "text/plain", strings.Repeat("x", officialDocumentResponseLimit+1))},
		{name: "server failure", response: documentResponse(http.StatusBadGateway, "text/html", "failure"), retryable: true},
		{name: "not found", response: documentResponse(http.StatusNotFound, "text/html", "missing")},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			tool := NewOfficialDocumentSearchToolWithClient(documentDoer(func(*http.Request) (*http.Response, error) { return testCase.response, nil }))
			output, err := tool.Execute(context.Background(), map[string]any{"document_id": "go_context_cancel", "query": "context cancel"})
			if err == nil || output.Retryable != testCase.retryable {
				t.Fatalf("unsafe response accepted: retryable=%v err=%v", output.Retryable, err)
			}
		})
	}
}

func TestOfficialDocumentToolUsesPinnedRabbitMQRepositorySourceButReturnsCanonicalURL(t *testing.T) {
	var requestedURL string
	tool := NewOfficialDocumentSearchToolWithClient(documentDoer(func(request *http.Request) (*http.Response, error) {
		requestedURL = request.URL.String()
		return documentResponse(http.StatusOK, "text/plain; charset=utf-8", "Dead letter exchanges use a dead-letter-exchange policy."), nil
	}))
	output, err := tool.Execute(context.Background(), map[string]any{"document_id": "rabbitmq_dlx", "query": "dead letter exchange"})
	if err != nil {
		t.Fatal(err)
	}
	result := output.Data.(PublicOfficialDocumentEvidence)
	if requestedURL != officialDocuments["rabbitmq_dlx"].FetchURL || result.CanonicalURL != officialDocuments["rabbitmq_dlx"].CanonicalURL || result.SourceHost != "raw.githubusercontent.com" || result.MatchCount == 0 {
		t.Fatalf("RabbitMQ source lineage is not explicit: url=%s result=%+v", requestedURL, result)
	}
}

func TestOfficialDocumentDefinitionRejectsURLAndUnknownDocumentBeforeFetch(t *testing.T) {
	calls := 0
	tool := NewOfficialDocumentSearchToolWithClient(documentDoer(func(*http.Request) (*http.Response, error) {
		calls++
		return documentResponse(http.StatusOK, "text/plain", "context cancellation"), nil
	}))
	runtime := newTestRuntime(t, &testTool{definition: tool.Definition(), execute: tool.Execute}, &captureAuditor{}, &captureObserver{})
	base := validInvocation()
	base.ToolName = "official_document_search"
	base.Arguments = []byte(`{"document_id":"go_context_cancel","query":"context cancel","url":"http://127.0.0.1/secret"}`)
	if result := runtime.Invoke(context.Background(), base); result.ErrorCode != ErrorArgumentsInvalid {
		t.Fatalf("URL field was not rejected: %+v", result)
	}
	base.CallID = "call-2"
	base.Arguments = []byte(`{"document_id":"internal_metadata","query":"credentials"}`)
	if result := runtime.Invoke(context.Background(), base); result.ErrorCode != ErrorArgumentsInvalid {
		t.Fatalf("unknown document was not rejected: %+v", result)
	}
	if calls != 0 {
		t.Fatalf("rejected sources reached network: %d", calls)
	}
}

func TestOfficialDocumentNetworkPolicyBlocksPrivateAndCrossHostRedirects(t *testing.T) {
	blocked := []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.100.100.200", "::1", "fc00::1", "2001:db8::1"}
	for _, value := range blocked {
		if !forbiddenOutboundIP(net.ParseIP(value)) {
			t.Fatalf("private or reserved address was allowed: %s", value)
		}
	}
	if forbiddenOutboundIP(net.ParseIP("8.8.8.8")) {
		t.Fatal("public address was rejected")
	}
	original, _ := http.NewRequest(http.MethodGet, "https://redis.io/docs/start", nil)
	sameHost, _ := http.NewRequest(http.MethodGet, "https://redis.io/docs/final", nil)
	if err := officialDocumentRedirectPolicy(sameHost, []*http.Request{original}); err != nil {
		t.Fatalf("same-host HTTPS redirect was rejected: %v", err)
	}
	crossHost, _ := http.NewRequest(http.MethodGet, "https://example.com/internal", nil)
	if err := officialDocumentRedirectPolicy(crossHost, []*http.Request{original}); err == nil {
		t.Fatal("cross-host redirect was accepted")
	}
	downgrade, _ := http.NewRequest(http.MethodGet, "http://redis.io/docs/final", nil)
	if err := officialDocumentRedirectPolicy(downgrade, []*http.Request{original}); err == nil {
		t.Fatal("HTTPS downgrade was accepted")
	}
}

func TestDocumentExcerptSearchHandlesUnicodeRuneBoundaries(t *testing.T) {
	excerpts := findDocumentExcerpts("前缀 İSTANBUL 中间 Redis 身份认证与最小权限 后缀", "身份认证")
	if len(excerpts) != 1 || !strings.Contains(excerpts[0], "身份认证") {
		t.Fatalf("unicode excerpt search failed: %+v", excerpts)
	}
}

func TestOfficialDocumentLiveSources(t *testing.T) {
	if os.Getenv("GOPHERAI_LIVE_DOC_TEST") != "1" {
		t.Skip("set GOPHERAI_LIVE_DOC_TEST=1 for bounded official-source integration test")
	}
	queries := map[string]string{
		"go_context_cancel": "context cancel", "redis_acl": "AUTH ACL",
		"rabbitmq_dlx": "dead letter exchange", "prometheus_alerting": "alerting rules",
	}
	tool := NewOfficialDocumentSearchTool()
	for documentID, query := range queries {
		t.Run(documentID, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			output, err := tool.Execute(ctx, map[string]any{"document_id": documentID, "query": query})
			if err != nil {
				t.Fatalf("live official source failed: %v", err)
			}
			result := output.Data.(PublicOfficialDocumentEvidence)
			if result.MatchCount == 0 || len(result.ContentHash) != 64 {
				t.Fatalf("live source returned no bounded evidence: %+v", result)
			}
		})
	}
}
