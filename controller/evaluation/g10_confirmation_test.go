package evaluation

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"GopherAI/model"

	"github.com/gin-gonic/gin"
)

type memoryG10ConfirmationStore struct {
	rows []model.G10ResumeFactConfirmation
}

func (store *memoryG10ConfirmationStore) LatestForReviewer(_ context.Context, reviewerHash string) (model.G10ResumeFactConfirmation, bool, error) {
	for index := len(store.rows) - 1; index >= 0; index-- {
		if store.rows[index].ReviewerHash == reviewerHash {
			return store.rows[index], true, nil
		}
	}
	return model.G10ResumeFactConfirmation{}, false, nil
}

func (store *memoryG10ConfirmationStore) Append(_ context.Context, row model.G10ResumeFactConfirmation) (bool, model.G10ResumeFactConfirmation, error) {
	for _, existing := range store.rows {
		if existing.IdempotencyKeyHash != row.IdempotencyKeyHash {
			continue
		}
		if existing.ConfirmationSHA256 != row.ConfirmationSHA256 {
			return false, model.G10ResumeFactConfirmation{}, errG10ConfirmationIdempotency
		}
		return false, existing, nil
	}
	store.rows = append(store.rows, row)
	return true, row, nil
}

func TestG10ResumeConfirmationIsAppendOnlyBoundAndIdempotent(t *testing.T) {
	evidence, err := buildInterviewEvidencePackage(validInterviewEvidenceInputs(t))
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryG10ConfirmationStore{}
	clock := func() time.Time { return time.Date(2026, 9, 7, 6, 0, 0, 0, time.UTC) }
	service := newG10ReviewService(&stubInterviewEvidenceService{report: evidence}, store, clock)
	before, err := service.Build(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	selected := []string{before.ResumeFacts[0].StatementID, before.ResumeFacts[1].StatementID, before.ResumeFacts[2].StatementID}
	command := G10ResumeConfirmationCommand{
		FactSetSHA256: before.ResumeFactSetSHA256, SelectedFactIDs: selected,
		IdempotencyKey: "resume-facts-current-set-0001", Acknowledgment: g10ResumeConfirmationAck,
	}
	receipt, err := service.ConfirmResumeFacts(context.Background(), "alice", command)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Created || receipt.Reused || len(store.rows) != 1 || receipt.Review.PassedGates != 2 || receipt.Review.ResumeConfirmation == nil || !receipt.Review.ResumeConfirmation.CurrentBinding || receipt.Review.ResumeConfirmation.SelectedCount != 3 {
		t.Fatalf("unexpected confirmation receipt: %+v rows=%d", receipt, len(store.rows))
	}
	gate := map[string]G10ReviewGate{}
	for _, item := range receipt.Review.Gates {
		gate[item.ID] = item
	}
	if gate["resume_fact_confirmation"].Status != "passed" || gate["resume_fact_confirmation"].UserConfirmationRequired || receipt.Review.ProductionReleaseReady {
		t.Fatalf("resume gate or production boundary is invalid: %+v", gate["resume_fact_confirmation"])
	}
	repeated, err := service.ConfirmResumeFacts(context.Background(), "alice", command)
	if err != nil || repeated.Created || !repeated.Reused || len(store.rows) != 1 {
		t.Fatalf("same confirmation was not idempotent: receipt=%+v err=%v rows=%d", repeated, err, len(store.rows))
	}
	conflicting := command
	conflicting.SelectedFactIDs = append(conflicting.SelectedFactIDs, before.ResumeFacts[3].StatementID)
	if _, err := service.ConfirmResumeFacts(context.Background(), "alice", conflicting); !errors.Is(err, errG10ConfirmationIdempotency) {
		t.Fatalf("same idempotency key accepted another selection: %v", err)
	}
	stale := command
	stale.IdempotencyKey = "resume-facts-current-set-0002"
	stale.FactSetSHA256 = strings.Repeat("f", 64)
	if _, err := service.ConfirmResumeFacts(context.Background(), "alice", stale); !errors.Is(err, errG10ConfirmationStale) {
		t.Fatalf("stale fact set was accepted: %v", err)
	}
}

func TestG10ResumeConfirmationRejectsUnsafeSelections(t *testing.T) {
	evidence, err := buildInterviewEvidencePackage(validInterviewEvidenceInputs(t))
	if err != nil {
		t.Fatal(err)
	}
	service := newG10ReviewService(&stubInterviewEvidenceService{report: evidence}, &memoryG10ConfirmationStore{}, time.Now)
	report, err := service.Build(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	valid := G10ResumeConfirmationCommand{FactSetSHA256: report.ResumeFactSetSHA256, SelectedFactIDs: []string{report.ResumeFacts[0].StatementID, report.ResumeFacts[1].StatementID, report.ResumeFacts[2].StatementID}, IdempotencyKey: "resume-facts-validation-0001", Acknowledgment: g10ResumeConfirmationAck}
	cases := []G10ResumeConfirmationCommand{
		{FactSetSHA256: valid.FactSetSHA256, SelectedFactIDs: valid.SelectedFactIDs[:2], IdempotencyKey: valid.IdempotencyKey, Acknowledgment: valid.Acknowledgment},
		{FactSetSHA256: valid.FactSetSHA256, SelectedFactIDs: []string{valid.SelectedFactIDs[0], valid.SelectedFactIDs[0], valid.SelectedFactIDs[1]}, IdempotencyKey: valid.IdempotencyKey, Acknowledgment: valid.Acknowledgment},
		{FactSetSHA256: valid.FactSetSHA256, SelectedFactIDs: []string{valid.SelectedFactIDs[0], valid.SelectedFactIDs[1], "full_320_evaluation"}, IdempotencyKey: valid.IdempotencyKey, Acknowledgment: valid.Acknowledgment},
		{FactSetSHA256: valid.FactSetSHA256, SelectedFactIDs: valid.SelectedFactIDs, IdempotencyKey: valid.IdempotencyKey, Acknowledgment: "yes"},
	}
	for _, command := range cases {
		if _, err := service.ConfirmResumeFacts(context.Background(), "alice", command); !errors.Is(err, errG10ConfirmationInvalid) {
			t.Fatalf("unsafe selection was accepted: %+v err=%v", command, err)
		}
	}
}

func TestG10ResumeConfirmationHandlerUsesStrictBodyAndPrincipal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	report := G10ReleaseReview{ResumeFactSetSHA256: strings.Repeat("a", 64)}
	service := &stubG10ReviewService{confirmation: G10ResumeConfirmationReceipt{SchemaVersion: g10ResumeConfirmationSchemaVersion, Created: true, Review: report}}
	handler := NewG10ReviewHandler(service)
	router := gin.New()
	router.Use(func(context *gin.Context) { context.Set("userName", "alice"); context.Next() })
	router.POST("/confirm", handler.ConfirmResumeFacts)
	body := fmt.Sprintf(`{"mode":"%s","fact_set_sha256":"%s","selected_fact_ids":["a","b","c"],"idempotency_key":"resume-confirmation-handler-0001","acknowledgment":"%s"}`, g10ResumeConfirmationMode, strings.Repeat("a", 64), g10ResumeConfirmationAck)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/confirm", strings.NewReader(body)))
	if response.Code != http.StatusOK || service.confirmationUser != "alice" || len(service.command.SelectedFactIDs) != 3 {
		t.Fatalf("principal or command was not forwarded: %d %s %+v", response.Code, response.Body.String(), service.command)
	}
	unknown := httptest.NewRecorder()
	router.ServeHTTP(unknown, httptest.NewRequest(http.MethodPost, "/confirm", strings.NewReader(strings.TrimSuffix(body, "}")+`,"approved":true}`)))
	if unknown.Code != http.StatusBadRequest || !strings.Contains(unknown.Body.String(), "G10_RESUME_CONFIRMATION_INVALID") {
		t.Fatalf("unknown field was accepted: %d %s", unknown.Code, unknown.Body.String())
	}
}
