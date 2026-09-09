package onlineeval

import (
	"GopherAI/model"
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

type Repository interface {
	Create(context.Context, *model.OnlineEvaluationSample, *model.OutboxEvent) error
	Get(context.Context, string) (model.OnlineEvaluationSample, error)
	MarkEvaluating(context.Context, string, int, time.Time) (bool, error)
	Complete(context.Context, string, EvaluationResult, time.Time) error
	Fail(context.Context, string, string, int, time.Time) error
	MarkDead(context.Context, string, string, int, time.Time) error
	Summary(context.Context, time.Time) (Summary, error)
	PruneExpired(context.Context, time.Time) (int64, error)
}

type GormRepository struct{ db *gorm.DB }

type EvaluationResult struct {
	AdapterVersion string
	PromptVersion  string
	ModelVersion   string
	Relevance      float64
	Completeness   float64
	Helpfulness    float64
	Groundedness   float64
	Safety         float64
	Overall        float64
	ResultJSON     string
}

type StatusCount struct {
	Status string `json:"status"`
	Count  int64  `json:"count"`
}

type Summary struct {
	WindowStart time.Time                     `json:"window_start"`
	Total       int64                         `json:"total"`
	ByStatus    map[string]int64              `json:"by_status"`
	Latest      *model.OnlineEvaluationSample `json:"latest,omitempty"`
}

func NewGormRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

func (repository *GormRepository) Create(ctx context.Context, sample *model.OnlineEvaluationSample, event *model.OutboxEvent) error {
	if repository == nil || repository.db == nil || sample == nil || event == nil {
		return gorm.ErrInvalidDB
	}
	return repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(sample).Error; err != nil {
			return err
		}
		return tx.Create(event).Error
	})
}

func (repository *GormRepository) Get(ctx context.Context, id string) (model.OnlineEvaluationSample, error) {
	if repository == nil || repository.db == nil {
		return model.OnlineEvaluationSample{}, gorm.ErrInvalidDB
	}
	var sample model.OnlineEvaluationSample
	err := repository.db.WithContext(ctx).Where("id = ?", id).First(&sample).Error
	return sample, err
}

func (repository *GormRepository) MarkEvaluating(ctx context.Context, id string, attempt int, now time.Time) (bool, error) {
	if repository == nil || repository.db == nil {
		return false, gorm.ErrInvalidDB
	}
	result := repository.db.WithContext(ctx).Model(&model.OnlineEvaluationSample{}).
		Where("id = ? AND status IN ?", id, []string{StatusPending, StatusEvaluating}).
		Updates(map[string]any{"status": StatusEvaluating, "attempt": attempt, "updated_at": now})
	return result.RowsAffected > 0, result.Error
}

func (repository *GormRepository) Complete(ctx context.Context, id string, result EvaluationResult, now time.Time) error {
	if repository == nil || repository.db == nil {
		return gorm.ErrInvalidDB
	}
	updates := map[string]any{
		"status": StatusCompleted, "last_error_code": "", "judge_adapter_version": result.AdapterVersion,
		"judge_prompt_version": result.PromptVersion, "judge_model_version": result.ModelVersion,
		"relevance": result.Relevance, "completeness": result.Completeness, "helpfulness": result.Helpfulness,
		"groundedness": result.Groundedness, "safety": result.Safety, "overall": result.Overall,
		"judge_result_json": result.ResultJSON, "evaluated_at": now, "updated_at": now,
	}
	return repository.db.WithContext(ctx).Model(&model.OnlineEvaluationSample{}).Where("id = ?", id).Updates(updates).Error
}

func (repository *GormRepository) Fail(ctx context.Context, id, code string, attempt int, now time.Time) error {
	return repository.updateFailure(ctx, id, StatusJudgeFailed, code, attempt, now)
}

func (repository *GormRepository) MarkDead(ctx context.Context, id, code string, attempt int, now time.Time) error {
	return repository.updateFailure(ctx, id, StatusDead, code, attempt, now)
}

func (repository *GormRepository) updateFailure(ctx context.Context, id, status, code string, attempt int, now time.Time) error {
	if repository == nil || repository.db == nil {
		return gorm.ErrInvalidDB
	}
	return repository.db.WithContext(ctx).Model(&model.OnlineEvaluationSample{}).Where("id = ?", id).
		Updates(map[string]any{"status": status, "last_error_code": code, "attempt": attempt, "evaluated_at": now, "updated_at": now}).Error
}

func (repository *GormRepository) Summary(ctx context.Context, since time.Time) (Summary, error) {
	if repository == nil || repository.db == nil {
		return Summary{}, gorm.ErrInvalidDB
	}
	rows := make([]StatusCount, 0, 5)
	if err := repository.db.WithContext(ctx).Model(&model.OnlineEvaluationSample{}).
		Select("status, COUNT(*) AS count").Where("created_at >= ? AND simulation = ?", since, false).Group("status").Scan(&rows).Error; err != nil {
		return Summary{}, err
	}
	summary := Summary{WindowStart: since, ByStatus: make(map[string]int64, len(rows))}
	for _, row := range rows {
		summary.ByStatus[row.Status] = row.Count
		summary.Total += row.Count
	}
	var latest model.OnlineEvaluationSample
	err := repository.db.WithContext(ctx).Where("simulation = ?", false).Order("created_at DESC").First(&latest).Error
	if err == nil {
		summary.Latest = &latest
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return Summary{}, err
	}
	return summary, nil
}

func (repository *GormRepository) PruneExpired(ctx context.Context, now time.Time) (int64, error) {
	if repository == nil || repository.db == nil {
		return 0, gorm.ErrInvalidDB
	}
	result := repository.db.WithContext(ctx).Where("expires_at < ?", now).Delete(&model.OnlineEvaluationSample{})
	return result.RowsAffected, result.Error
}
