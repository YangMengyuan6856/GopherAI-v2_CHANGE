package evaluation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"GopherAI/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	g10ResumeConfirmationSchemaVersion = "g10-resume-confirmation-v1"
	g10ResumeConfirmationMode          = "human_resume_fact_confirmation"
	g10ResumeConfirmationAck           = "I_CONFIRM_RESUME_FACTS_WITH_REQUIRED_QUALIFIERS"
	maximumG10ConfirmationBodyBytes    = 16 * 1024
)

var (
	errG10ConfirmationUnavailable = errors.New("G10 resume confirmation is unavailable")
	errG10ConfirmationInvalid     = errors.New("G10 resume confirmation is invalid")
	errG10ConfirmationStale       = errors.New("G10 resume fact set is stale")
	errG10ConfirmationIdempotency = errors.New("G10 resume confirmation idempotency conflict")
	g10IdempotencyKeyPattern      = regexp.MustCompile(`^[A-Za-z0-9._:-]{16,128}$`)
)

type G10ResumeConfirmationCommand struct {
	FactSetSHA256   string
	SelectedFactIDs []string
	IdempotencyKey  string
	Acknowledgment  string
}

type G10ResumeConfirmationView struct {
	Status                string    `json:"status"`
	ConfirmationSHA256    string    `json:"confirmation_sha256"`
	FactSetSHA256         string    `json:"fact_set_sha256"`
	SourceReleaseID       string    `json:"source_release_id"`
	EvidencePackageSHA256 string    `json:"evidence_package_sha256"`
	SelectedFactIDs       []string  `json:"selected_fact_ids"`
	SelectedCount         int       `json:"selected_count"`
	CurrentBinding        bool      `json:"current_binding"`
	CreatedAt             time.Time `json:"created_at"`
}

type G10ResumeConfirmationReceipt struct {
	SchemaVersion string           `json:"schema_version"`
	Created       bool             `json:"created"`
	Reused        bool             `json:"reused"`
	Review        G10ReleaseReview `json:"review"`
}

type G10ResumeConfirmationStore interface {
	LatestForReviewer(context.Context, string) (model.G10ResumeFactConfirmation, bool, error)
	Append(context.Context, model.G10ResumeFactConfirmation) (bool, model.G10ResumeFactConfirmation, error)
}

type GormG10ResumeConfirmationStore struct{ db *gorm.DB }

func NewGormG10ResumeConfirmationStore(db *gorm.DB) *GormG10ResumeConfirmationStore {
	return &GormG10ResumeConfirmationStore{db: db}
}

func (store *GormG10ResumeConfirmationStore) LatestForReviewer(ctx context.Context, reviewerHash string) (model.G10ResumeFactConfirmation, bool, error) {
	if store == nil || store.db == nil || len(reviewerHash) != 64 {
		return model.G10ResumeFactConfirmation{}, false, gorm.ErrInvalidDB
	}
	var row model.G10ResumeFactConfirmation
	result := store.db.WithContext(ctx).Where("reviewer_hash = ?", reviewerHash).Order("created_at DESC, id DESC").Limit(1).Find(&row)
	if result.Error != nil || result.RowsAffected == 0 {
		return row, false, result.Error
	}
	if err := validateStoredG10Confirmation(row); err != nil {
		return model.G10ResumeFactConfirmation{}, false, errors.New("stored G10 resume confirmation failed validation")
	}
	return row, true, nil
}

func (store *GormG10ResumeConfirmationStore) Append(ctx context.Context, confirmation model.G10ResumeFactConfirmation) (bool, model.G10ResumeFactConfirmation, error) {
	if store == nil || store.db == nil {
		return false, model.G10ResumeFactConfirmation{}, gorm.ErrInvalidDB
	}
	if err := validateStoredG10Confirmation(confirmation); err != nil {
		return false, model.G10ResumeFactConfirmation{}, err
	}
	created := false
	stored := model.G10ResumeFactConfirmation{}
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Where("idempotency_key_hash = ?", confirmation.IdempotencyKeyHash).Limit(1).Find(&stored)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 1 {
			if stored.ConfirmationSHA256 != confirmation.ConfirmationSHA256 {
				return errG10ConfirmationIdempotency
			}
			return nil
		}
		if err := tx.Create(&confirmation).Error; err != nil {
			var concurrent model.G10ResumeFactConfirmation
			if readErr := tx.Where("idempotency_key_hash = ?", confirmation.IdempotencyKeyHash).First(&concurrent).Error; readErr == nil {
				if concurrent.ConfirmationSHA256 != confirmation.ConfirmationSHA256 {
					return errG10ConfirmationIdempotency
				}
				stored = concurrent
				return nil
			}
			return err
		}
		created, stored = true, confirmation
		return nil
	})
	return created, stored, err
}

func (service *evidenceBackedG10ReviewService) ConfirmResumeFacts(ctx context.Context, principal string, command G10ResumeConfirmationCommand) (G10ResumeConfirmationReceipt, error) {
	if service == nil || service.confirmations == nil || service.evidence == nil {
		return G10ResumeConfirmationReceipt{}, errG10ConfirmationUnavailable
	}
	principal = strings.TrimSpace(principal)
	if principal == "" || !g10IdempotencyKeyPattern.MatchString(command.IdempotencyKey) || command.Acknowledgment != g10ResumeConfirmationAck || len(command.FactSetSHA256) != 64 {
		return G10ResumeConfirmationReceipt{}, errG10ConfirmationInvalid
	}
	review, err := service.Build(ctx, principal)
	if err != nil {
		return G10ResumeConfirmationReceipt{}, err
	}
	if command.FactSetSHA256 != review.ResumeFactSetSHA256 {
		return G10ResumeConfirmationReceipt{}, errG10ConfirmationStale
	}
	selected, err := normalizeSelectedG10Facts(command.SelectedFactIDs, review.ResumeFacts)
	if err != nil {
		return G10ResumeConfirmationReceipt{}, err
	}
	encodedIDs, _ := json.Marshal(selected)
	reviewerHash := g10ReviewerHash(principal)
	idempotencyHash := g10Digest(reviewerHash + "\x00" + command.IdempotencyKey)
	requestHash := g10Digest(strings.Join([]string{review.ResumeFactSetSHA256, strings.Join(selected, "\x00"), reviewerHash}, "\x00"))
	confirmation := model.G10ResumeFactConfirmation{
		SchemaVersion: g10ResumeConfirmationSchemaVersion, FactSetSHA256: review.ResumeFactSetSHA256,
		SourceReleaseID: review.ReleaseID, SourceGitSHA: review.GitSHA, EvidencePackageSHA256: review.EvidencePackageSHA256,
		SelectedFactIDsJSON: string(encodedIDs), SelectedCount: len(selected), ReviewerHash: reviewerHash,
		IdempotencyKeyHash: idempotencyHash, RequestSHA256: requestHash, CreatedAt: service.clock().UTC(),
	}
	confirmation.ConfirmationSHA256 = g10ConfirmationHash(confirmation)
	confirmation.ID = confirmation.ConfirmationSHA256
	if err := validateStoredG10Confirmation(confirmation); err != nil {
		return G10ResumeConfirmationReceipt{}, err
	}
	created, _, err := service.confirmations.Append(ctx, confirmation)
	if err != nil {
		return G10ResumeConfirmationReceipt{}, err
	}
	updated, err := service.Build(ctx, principal)
	if err != nil {
		return G10ResumeConfirmationReceipt{}, err
	}
	return G10ResumeConfirmationReceipt{SchemaVersion: g10ResumeConfirmationSchemaVersion, Created: created, Reused: !created, Review: updated}, nil
}

func normalizeSelectedG10Facts(raw []string, facts []G10ResumeFact) ([]string, error) {
	if len(raw) < 3 || len(raw) > 5 {
		return nil, errG10ConfirmationInvalid
	}
	allowed := make(map[string]struct{}, len(facts))
	for _, fact := range facts {
		allowed[fact.StatementID] = struct{}{}
	}
	selected := make([]string, len(raw))
	seen := map[string]struct{}{}
	for index, id := range raw {
		id = strings.TrimSpace(id)
		if _, ok := allowed[id]; !ok {
			return nil, errG10ConfirmationInvalid
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, errG10ConfirmationInvalid
		}
		seen[id], selected[index] = struct{}{}, id
	}
	sort.Strings(selected)
	return selected, nil
}

func applyG10ResumeConfirmation(report *G10ReleaseReview, stored model.G10ResumeFactConfirmation) error {
	if report == nil || validateStoredG10Confirmation(stored) != nil {
		return errors.New("G10 resume confirmation cannot be applied")
	}
	selected := []string{}
	if err := json.Unmarshal([]byte(stored.SelectedFactIDsJSON), &selected); err != nil {
		return errors.New("G10 resume confirmation ids are invalid")
	}
	view := &G10ResumeConfirmationView{
		Status: g10ResumeConfirmationMode, ConfirmationSHA256: stored.ConfirmationSHA256,
		FactSetSHA256: stored.FactSetSHA256, SourceReleaseID: stored.SourceReleaseID,
		EvidencePackageSHA256: stored.EvidencePackageSHA256, SelectedFactIDs: selected,
		SelectedCount: stored.SelectedCount, CurrentBinding: stored.FactSetSHA256 == report.ResumeFactSetSHA256,
		CreatedAt: stored.CreatedAt.UTC(),
	}
	report.ResumeConfirmation = view
	if view.CurrentBinding {
		if _, err := normalizeSelectedG10Facts(selected, report.ResumeFacts); err != nil {
			return errors.New("G10 resume confirmation references ineligible facts")
		}
		for index := range report.Gates {
			if report.Gates[index].ID != "resume_fact_confirmation" {
				continue
			}
			report.Gates[index].Status = "passed"
			report.Gates[index].Conclusion = "用户已从当前技术证据事实集中确认 3～5 条，并明确承诺保留全部限定语。"
			report.Gates[index].EvidenceRefs = append([]string(nil), selected...)
			report.Gates[index].NextAction = "生成简历时只使用已选事实及其原始限定语；证据事实集变化后必须重新确认。"
			report.Gates[index].UserConfirmationRequired = false
		}
		report.PassedGates++
		report.Status = "g10_blocked_by_product_and_environment_gates"
		for _, gate := range report.Gates {
			if gate.ID == "product_total_acceptance" && gate.Status == "passed" {
				report.Status = "g10_deferred_by_environment_gate"
			}
		}
	} else {
		for index := range report.Gates {
			if report.Gates[index].ID == "resume_fact_confirmation" {
				report.Gates[index].Conclusion = "最近一次用户确认绑定旧的事实集，当前证据变化后不能复用。"
				report.Gates[index].NextAction = "重新选择当前 3～5 条事实并确认全部限定语。"
			}
		}
	}
	return finalizeG10ReleaseReview(report)
}

func validateStoredG10Confirmation(row model.G10ResumeFactConfirmation) error {
	selected := []string{}
	if row.SchemaVersion != g10ResumeConfirmationSchemaVersion || len(row.ID) != 64 || row.ID != row.ConfirmationSHA256 || len(row.FactSetSHA256) != 64 || strings.TrimSpace(row.SourceReleaseID) == "" || len(row.SourceGitSHA) != 40 || len(row.EvidencePackageSHA256) != 64 || row.SelectedCount < 3 || row.SelectedCount > 5 || len(row.ReviewerHash) != 64 || len(row.IdempotencyKeyHash) != 64 || len(row.RequestSHA256) != 64 || row.CreatedAt.IsZero() || json.Unmarshal([]byte(row.SelectedFactIDsJSON), &selected) != nil || len(selected) != row.SelectedCount || g10ConfirmationHash(row) != row.ConfirmationSHA256 {
		return errG10ConfirmationInvalid
	}
	for index, id := range selected {
		if strings.TrimSpace(id) == "" || (index > 0 && selected[index-1] >= id) {
			return errG10ConfirmationInvalid
		}
	}
	expectedRequestHash := g10Digest(strings.Join([]string{row.FactSetSHA256, strings.Join(selected, "\x00"), row.ReviewerHash}, "\x00"))
	if row.RequestSHA256 != expectedRequestHash {
		return errG10ConfirmationInvalid
	}
	return nil
}

func g10ConfirmationHash(row model.G10ResumeFactConfirmation) string {
	return g10Digest(strings.Join([]string{row.SchemaVersion, row.FactSetSHA256, row.SourceReleaseID, row.SourceGitSHA, row.EvidencePackageSHA256, row.SelectedFactIDsJSON, row.ReviewerHash, row.IdempotencyKeyHash, row.RequestSHA256}, "\x00"))
}

func g10ReviewerHash(principal string) string { return g10Digest(strings.TrimSpace(principal)) }

func g10Digest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

type g10ResumeConfirmationRequest struct {
	Mode            string   `json:"mode"`
	FactSetSHA256   string   `json:"fact_set_sha256"`
	SelectedFactIDs []string `json:"selected_fact_ids"`
	IdempotencyKey  string   `json:"idempotency_key"`
	Acknowledgment  string   `json:"acknowledgment"`
}

func (handler *G10ReviewHandler) ConfirmResumeFacts(ginContext *gin.Context) {
	if handler == nil || handler.service == nil {
		writeG10ReviewError(ginContext, http.StatusServiceUnavailable, "G10_RESUME_CONFIRMATION_UNAVAILABLE", "简历事实确认暂不可用")
		return
	}
	ginContext.Request.Body = http.MaxBytesReader(ginContext.Writer, ginContext.Request.Body, maximumG10ConfirmationBodyBytes)
	decoder := json.NewDecoder(ginContext.Request.Body)
	decoder.DisallowUnknownFields()
	var request g10ResumeConfirmationRequest
	if err := decoder.Decode(&request); err != nil || request.Mode != g10ResumeConfirmationMode {
		writeG10ReviewError(ginContext, http.StatusBadRequest, "G10_RESUME_CONFIRMATION_INVALID", "简历事实确认请求格式无效")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeG10ReviewError(ginContext, http.StatusBadRequest, "G10_RESUME_CONFIRMATION_INVALID", "简历事实确认请求包含多余内容")
		return
	}
	command := G10ResumeConfirmationCommand{
		FactSetSHA256: strings.TrimSpace(request.FactSetSHA256), SelectedFactIDs: request.SelectedFactIDs,
		IdempotencyKey: strings.TrimSpace(request.IdempotencyKey), Acknowledgment: request.Acknowledgment,
	}
	ctx, cancel := context.WithTimeout(ginContext.Request.Context(), 5*time.Second)
	defer cancel()
	receipt, err := handler.service.ConfirmResumeFacts(ctx, ginContext.GetString("userName"), command)
	if err != nil {
		switch {
		case errors.Is(err, errG10ConfirmationUnavailable):
			writeG10ReviewError(ginContext, http.StatusServiceUnavailable, "G10_RESUME_CONFIRMATION_UNAVAILABLE", "简历事实确认暂不可用")
		case errors.Is(err, errG10ConfirmationStale):
			writeG10ReviewError(ginContext, http.StatusConflict, "G10_RESUME_FACT_SET_STALE", "事实证据已变化，请刷新后重新选择")
		case errors.Is(err, errG10ConfirmationIdempotency):
			writeG10ReviewError(ginContext, http.StatusConflict, "G10_RESUME_CONFIRMATION_CONFLICT", "幂等键已绑定另一组事实")
		case errors.Is(err, errG10ConfirmationInvalid):
			writeG10ReviewError(ginContext, http.StatusBadRequest, "G10_RESUME_CONFIRMATION_INVALID", "请选择 3～5 条当前事实并确认保留限定语")
		default:
			writeG10ReviewError(ginContext, http.StatusUnprocessableEntity, "G10_RESUME_CONFIRMATION_FAILED", "简历事实确认无法安全记录")
		}
		return
	}
	ginContext.Header("Cache-Control", "no-store")
	ginContext.JSON(http.StatusOK, receipt)
}
