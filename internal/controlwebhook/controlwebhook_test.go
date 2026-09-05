package controlwebhook

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"GopherAI/internal/observability"
	"GopherAI/model"

	"gorm.io/gorm"
)

func TestIncidentTransitionOpensSuppressesAndRequiresTwoRecoveryWindows(t *testing.T) {
	now := time.Date(2026, 9, 6, 3, 0, 0, 0, time.UTC)
	candidate := anomalyCandidate(now, "anomalous", strings.Repeat("a", 64))
	eventType, incident, notify := transitionIncident(model.ControlIncident{}, false, candidate, now)
	if eventType != EventOpened || !notify || incident.Status != IncidentActive || incident.LastBatchID != candidate.BatchID {
		t.Fatalf("unexpected open transition: type=%s notify=%v incident=%+v", eventType, notify, incident)
	}
	if eventType, next, notify := transitionIncident(*incident, true, candidate, now.Add(time.Minute)); eventType != "" || next != nil || notify {
		t.Fatalf("same batch was not suppressed: %s %+v %v", eventType, next, notify)
	}
	updatedCandidate := anomalyCandidate(now.Add(time.Minute), "anomalous", strings.Repeat("b", 64))
	if eventType, next, notify := transitionIncident(*incident, true, updatedCandidate, now.Add(time.Minute)); eventType != "" || next == nil || notify || next.LastBatchID != updatedCandidate.BatchID {
		t.Fatalf("cooldown update should persist without notification: %s %+v %v", eventType, next, notify)
	}
	healthyOne := anomalyCandidate(now.Add(2*time.Minute), "healthy", strings.Repeat("c", 64))
	_, firstRecovery, notify := transitionIncident(*incident, true, healthyOne, now.Add(2*time.Minute))
	if firstRecovery == nil || firstRecovery.RecoveryStreak != 1 || firstRecovery.Status != IncidentActive || notify {
		t.Fatalf("first recovery window must not resolve: %+v notify=%v", firstRecovery, notify)
	}
	healthyTwo := anomalyCandidate(now.Add(3*time.Minute), "healthy", strings.Repeat("d", 64))
	eventType, resolved, notify := transitionIncident(*firstRecovery, true, healthyTwo, now.Add(3*time.Minute))
	if eventType != EventResolved || !notify || resolved.Status != IncidentResolved || resolved.ResolvedAt == nil {
		t.Fatalf("second recovery window did not resolve: %s %+v %v", eventType, resolved, notify)
	}
}

func TestIncidentTransitionEmitsUpdateAfterCooldown(t *testing.T) {
	now := time.Date(2026, 9, 6, 3, 0, 0, 0, time.UTC)
	candidate := anomalyCandidate(now, "anomalous", strings.Repeat("a", 64))
	_, incident, _ := transitionIncident(model.ControlIncident{}, false, candidate, now)
	later := anomalyCandidate(now.Add(updateNotificationCooldown), "anomalous", strings.Repeat("e", 64))
	eventType, next, notify := transitionIncident(*incident, true, later, now.Add(updateNotificationCooldown))
	if eventType != EventUpdated || !notify || next.LastNotifiedAt != now.Add(updateNotificationCooldown) {
		t.Fatalf("expected bounded update notification: %s %+v %v", eventType, next, notify)
	}
}

func TestBuildDeliveryIsDeterministicAndNeverAppliesRecommendation(t *testing.T) {
	now := time.Now().UTC()
	candidate := anomalyCandidate(now, "anomalous", strings.Repeat("a", 64))
	_, incident, _ := transitionIncident(model.ControlIncident{}, false, candidate, now)
	first, err := buildDelivery(EventOpened, *incident, candidate, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := buildDelivery(EventOpened, *incident, candidate, now)
	if err != nil || first.EventID != second.EventID || first.PayloadSHA256 != stablePayloadSHA(first.PayloadJSON) || strings.Contains(first.PayloadJSON, "tenant") || strings.Contains(first.PayloadJSON, "user") {
		t.Fatalf("delivery is not deterministic and bounded: first=%+v second=%+v err=%v", first, second, err)
	}
	if !strings.Contains(first.PayloadJSON, `"applied":false`) || !strings.Contains(first.PayloadJSON, `"active_policy_unchanged"`) {
		t.Fatalf("recommend-only guardrail missing: %s", first.PayloadJSON)
	}
}

func TestAcceptanceDeliveryIsExplicitSimulationAndKeepsProductionProvenance(t *testing.T) {
	now := time.Now().UTC()
	runtime := observability.PrometheusRuntimeSnapshot{Status: "ready", RulesVersion: observability.RecordingRulesVersion, RulesSHA256: strings.Repeat("a", 64)}
	delivery, err := BuildAcceptanceDelivery(runtime, now)
	if err != nil {
		t.Fatal(err)
	}
	if !delivery.Simulation || delivery.IncidentKey != "incident-acceptance-fixture" || !strings.Contains(delivery.PayloadJSON, `"simulation":true`) || !strings.Contains(delivery.PayloadJSON, `"fixture_mode":"signed_loopback_delivery"`) || stablePayloadSHA(delivery.PayloadJSON) != delivery.PayloadSHA256 {
		t.Fatalf("acceptance event crossed production boundary: %+v", delivery)
	}
}

type fakeDeliveryRepository struct {
	delivery *model.ControlWebhookDelivery
	marked   string
	status   int
	code     string
}

func (repository *fakeDeliveryRepository) ClaimAvailable(context.Context, time.Time, time.Duration) (*model.ControlWebhookDelivery, error) {
	if repository.delivery == nil {
		return nil, gorm.ErrRecordNotFound
	}
	result := *repository.delivery
	repository.delivery = nil
	return &result, nil
}

func (repository *fakeDeliveryRepository) MarkDelivered(_ context.Context, _ string, _ time.Time, status int) error {
	repository.marked, repository.status = StatusDelivered, status
	return nil
}
func (repository *fakeDeliveryRepository) MarkRetry(_ context.Context, _ string, _ time.Time, status int, code string) error {
	repository.marked, repository.status, repository.code = StatusRetry, status, code
	return nil
}
func (repository *fakeDeliveryRepository) MarkDead(_ context.Context, _ string, _ time.Time, status int, code string) error {
	repository.marked, repository.status, repository.code = StatusDead, status, code
	return nil
}

type fakeDoer struct {
	response *http.Response
	err      error
	request  *http.Request
	body     string
}

func (doer *fakeDoer) Do(request *http.Request) (*http.Response, error) {
	doer.request = request
	body, _ := io.ReadAll(request.Body)
	doer.body = string(body)
	return doer.response, doer.err
}

func TestDispatcherSignsAndMarksSuccessfulDelivery(t *testing.T) {
	config := testConfig(t)
	delivery := testDelivery(t, 1)
	repository := &fakeDeliveryRepository{delivery: &delivery}
	doer := &fakeDoer{response: &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("ok"))}}
	dispatcher, err := NewDispatcher(config, repository, doer, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 6, 4, 0, 0, 0, time.UTC)
	dispatcher.clock = func() time.Time { return now }
	if processed, err := dispatcher.DispatchAvailable(context.Background(), 1); err != nil || processed != 1 || repository.marked != StatusDelivered || repository.status != http.StatusNoContent {
		t.Fatalf("delivery did not succeed: processed=%d repo=%+v err=%v", processed, repository, err)
	}
	timestamp := doer.request.Header.Get("X-GopherAI-Timestamp")
	if doer.request.Header.Get("X-GopherAI-Signature") != SignatureVersion+"="+Sign(config.Secret, timestamp, []byte(doer.body)) || doer.request.Header.Get("X-GopherAI-Event-ID") != delivery.EventID {
		t.Fatalf("signed headers are invalid: %v", doer.request.Header)
	}
}

func TestDispatcherRetriesTransientAndDeadLettersExhaustedDelivery(t *testing.T) {
	config := testConfig(t)
	delivery := testDelivery(t, 1)
	repository := &fakeDeliveryRepository{delivery: &delivery}
	doer := &fakeDoer{response: &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader("private upstream detail"))}}
	dispatcher, _ := NewDispatcher(config, repository, doer, nil, nil)
	if _, err := dispatcher.DispatchAvailable(context.Background(), 1); err != nil || repository.marked != StatusRetry || repository.code != ErrorRemoteServer {
		t.Fatalf("transient failure did not retry: %+v err=%v", repository, err)
	}
	delivery = testDelivery(t, config.MaxAttempts)
	repository = &fakeDeliveryRepository{delivery: &delivery}
	dispatcher, _ = NewDispatcher(config, repository, doer, nil, nil)
	if _, err := dispatcher.DispatchAvailable(context.Background(), 1); err != nil || repository.marked != StatusDead || repository.code != ErrorRemoteServer {
		t.Fatalf("exhausted delivery did not enter DLQ: %+v err=%v", repository, err)
	}
}

type fakeReceiptRepository struct {
	receipt model.ControlWebhookReceipt
}

func (repository *fakeReceiptRepository) StoreReceipt(_ context.Context, receipt model.ControlWebhookReceipt) (bool, error) {
	repository.receipt = receipt
	return false, nil
}

func TestReceiverVerifiesSignatureTimestampIdentityAndLoopback(t *testing.T) {
	config := testConfig(t)
	config.LoopbackReceiver = true
	delivery := testDelivery(t, 1)
	now := time.Date(2026, 9, 6, 4, 0, 0, 0, time.UTC)
	timestamp := strconvUnix(now)
	header := http.Header{}
	header.Set("X-GopherAI-Event-ID", delivery.EventID)
	header.Set("X-GopherAI-Event-Type", delivery.EventType)
	header.Set("X-GopherAI-Timestamp", timestamp)
	header.Set("X-GopherAI-Signature", SignatureVersion+"="+Sign(config.Secret, timestamp, []byte(delivery.PayloadJSON)))
	repository := new(fakeReceiptRepository)
	receiver := NewReceiver(config, repository)
	receiver.clock = func() time.Time { return now }
	if duplicate, code, err := receiver.Receive(context.Background(), "127.0.0.1:12345", header, []byte(delivery.PayloadJSON)); err != nil || duplicate || code != "" || repository.receipt.EventID != delivery.EventID {
		t.Fatalf("valid signed webhook rejected: duplicate=%v code=%s receipt=%+v err=%v", duplicate, code, repository.receipt, err)
	}
	header.Set("X-GopherAI-Signature", SignatureVersion+"="+strings.Repeat("0", 64))
	if _, code, err := receiver.Receive(context.Background(), "127.0.0.1:12345", header, []byte(delivery.PayloadJSON)); err == nil || code != ErrorInvalidSignature {
		t.Fatalf("tampered signature accepted: code=%s err=%v", code, err)
	}
	if _, code, err := receiver.Receive(context.Background(), "203.0.113.7:12345", header, []byte(delivery.PayloadJSON)); err == nil || code != "WEBHOOK_SOURCE_REJECTED" {
		t.Fatalf("non-loopback source accepted: code=%s err=%v", code, err)
	}
}

func anomalyCandidate(now time.Time, decision string, batchID string) IncidentCandidate {
	policy, observations, _ := observability.AcceptanceAnomalyScenario("quality_drop", now)
	analysis, _ := observability.AnalyzeMetricWindow(policy, observations)
	if decision == "healthy" {
		policy, observations, _ = observability.AcceptanceAnomalyScenario("healthy", now)
		analysis, _ = observability.AnalyzeMetricWindow(policy, observations)
	}
	return IncidentCandidate{
		BatchID: batchID, CollectedAt: now, RulesVersion: observability.RecordingRulesVersion, RulesSHA256: strings.Repeat("f", 64),
		Series: observability.ProductionMetricAnalysis{
			Metric: policy.Metric, Strategy: policy.Strategy, WindowSeconds: 900, DataStatus: observability.MetricWindowObserved,
			Latest:   observability.MetricWindowPoint{Metric: policy.Metric, Strategy: policy.Strategy, DataStatus: observability.MetricWindowObserved, Value: observations[len(observations)-1].Value, Population: observations[len(observations)-1].Population, WindowSeconds: 900, ObservedAt: now},
			Analysis: &analysis,
		},
	}
}

func testConfig(t *testing.T) Config {
	t.Helper()
	endpoint, err := urlParse("http://127.0.0.1:9090/internal/v1/webhooks/control")
	if err != nil {
		t.Fatal(err)
	}
	return Config{Enabled: true, Endpoint: endpoint, Secret: []byte(strings.Repeat("s", 32)), EndpointMode: "staging_loopback", LoopbackReceiver: true, RequestTimeout: time.Second, PollInterval: time.Second, LeaseDuration: time.Second, MaxAttempts: 3}
}

func testDelivery(t *testing.T, attempt int) model.ControlWebhookDelivery {
	t.Helper()
	now := time.Date(2026, 9, 6, 4, 0, 0, 0, time.UTC)
	candidate := anomalyCandidate(now, "anomalous", strings.Repeat("a", 64))
	_, incident, _ := transitionIncident(model.ControlIncident{}, false, candidate, now)
	delivery, err := buildDelivery(EventOpened, *incident, candidate, now)
	if err != nil {
		t.Fatal(err)
	}
	delivery.Status, delivery.Attempt = StatusProcessing, attempt
	return delivery
}

func urlParse(value string) (*url.URL, error) { return url.Parse(value) }

func strconvUnix(value time.Time) string { return strconv.FormatInt(value.Unix(), 10) }
