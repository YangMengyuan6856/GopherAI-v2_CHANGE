package catalogreview

import (
	"context"
	"errors"
	"fmt"

	"GopherAI/model"

	"gorm.io/gorm"
)

var ErrLegacyReviewMigration = errors.New("legacy evaluation catalog review migration failed")

// MigrateLegacyReviews upgrades only reviews bound to the current immutable
// catalog and governance lineage. Semantic decisions and timestamps are kept;
// the prior commitment is retained in PreviousReviewSHA256.
func MigrateLegacyReviews(ctx context.Context, db *gorm.DB, snapshot Snapshot) (int, error) {
	if db == nil || len(snapshot.CatalogSHA256) != 64 || len(snapshot.Governance.ManifestSHA256) != 64 {
		return 0, ErrLegacyReviewMigration
	}
	rows := make([]model.EvaluationCatalogReview, 0)
	if err := db.WithContext(ctx).
		Where("schema_version = ? AND catalog_sha256 = ? AND governance_sha256 = ?", LegacyReviewSchemaVersion, snapshot.CatalogSHA256, snapshot.Governance.ManifestSHA256).
		Order("case_id ASC, revision ASC").Find(&rows).Error; err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	migrated := 0
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, row := range rows {
			upgraded, changed, err := upgradeLegacyReview(snapshot, row)
			if err != nil {
				return err
			}
			if !changed {
				continue
			}
			result := tx.Model(&model.EvaluationCatalogReview{}).
				Where("id = ? AND schema_version = ? AND review_sha256 = ?", row.ID, LegacyReviewSchemaVersion, row.ReviewSHA256).
				Updates(map[string]any{
					"id": upgraded.ID, "schema_version": upgraded.SchemaVersion,
					"review_sha256":          upgraded.ReviewSHA256,
					"previous_review_sha256": upgraded.PreviousReviewSHA256,
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("%w: concurrent legacy review update", ErrLegacyReviewMigration)
			}
			migrated++
		}
		return nil
	})
	return migrated, err
}

func upgradeLegacyReview(snapshot Snapshot, review model.EvaluationCatalogReview) (model.EvaluationCatalogReview, bool, error) {
	if review.SchemaVersion != LegacyReviewSchemaVersion {
		return review, false, nil
	}
	if review.ID != review.ReviewSHA256 || len(review.ID) != 64 || review.PreviousReviewSHA256 != "" || review.CreatedAt.Nanosecond() != 0 {
		return model.EvaluationCatalogReview{}, false, ErrLegacyReviewMigration
	}
	if err := validateReviewSemantics(review); err != nil {
		return model.EvaluationCatalogReview{}, false, ErrLegacyReviewMigration
	}
	if review.DatasetVersion != snapshot.DatasetVersion || review.CatalogSHA256 != snapshot.CatalogSHA256 || review.GovernanceSHA256 != snapshot.Governance.ManifestSHA256 {
		return model.EvaluationCatalogReview{}, false, ErrLegacyReviewMigration
	}
	matched := false
	for _, item := range snapshot.Cases {
		if item.ID == review.CaseID {
			matched = item.Slice == review.Slice && item.CaseSHA256 == review.CaseSHA256
			break
		}
	}
	if !matched {
		return model.EvaluationCatalogReview{}, false, ErrLegacyReviewMigration
	}
	upgraded := review
	upgraded.SchemaVersion = ReviewSchemaVersion
	upgraded.PreviousReviewSHA256 = review.ReviewSHA256
	upgraded.ReviewSHA256 = reviewSHA(upgraded)
	upgraded.ID = upgraded.ReviewSHA256
	if err := validateReview(upgraded); err != nil {
		return model.EvaluationCatalogReview{}, false, ErrLegacyReviewMigration
	}
	return upgraded, true, nil
}
