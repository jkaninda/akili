package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/jkaninda/akili/internal/domain"
	"github.com/jkaninda/akili/internal/heartbeattask"
)

// HeartbeatTaskRepository implements heartbeattask.HeartbeatTaskStore using GORM.
type HeartbeatTaskRepository struct {
	db *gorm.DB
}

// NewHeartbeatTaskRepository creates a new HeartbeatTaskRepository.
func NewHeartbeatTaskRepository(db *gorm.DB) *HeartbeatTaskRepository {
	return &HeartbeatTaskRepository{db: db}
}

func (r *HeartbeatTaskRepository) Upsert(ctx context.Context, task *domain.HeartbeatTask) error {
	m := toHeartbeatTaskModel(task)
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "org_id"},
				{Name: "file_group"},
				{Name: "name"},
			},
			DoUpdates: clause.AssignmentColumns([]string{
				"description", "source_file", "cron_expression", "mode",
				"user_id", "budget_limit_usd", "notification_channel_ids",
				"enabled", "next_run_at", "updated_at",
			}),
		}).
		Create(&m).Error
}

func (r *HeartbeatTaskRepository) Get(ctx context.Context, orgID, id uuid.UUID) (*domain.HeartbeatTask, error) {
	var m HeartbeatTaskModel
	if err := r.db.WithContext(ctx).
		Scopes(TenantScope(orgID)).
		Where("id = ?", id).
		First(&m).Error; err != nil {
		return nil, err
	}
	return toHeartbeatTaskDomain(&m), nil
}

func (r *HeartbeatTaskRepository) List(ctx context.Context, orgID uuid.UUID) ([]domain.HeartbeatTask, error) {
	var models []HeartbeatTaskModel
	if err := r.db.WithContext(ctx).
		Scopes(TenantScope(orgID)).
		Order("file_group, name").
		Find(&models).Error; err != nil {
		return nil, err
	}
	result := make([]domain.HeartbeatTask, len(models))
	for i := range models {
		result[i] = *toHeartbeatTaskDomain(&models[i])
	}
	return result, nil
}

func (r *HeartbeatTaskRepository) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	return r.db.WithContext(ctx).
		Scopes(TenantScope(orgID)).
		Where("id = ?", id).
		Delete(&HeartbeatTaskModel{}).Error
}

func (r *HeartbeatTaskRepository) DeleteByFileGroup(ctx context.Context, orgID uuid.UUID, fileGroup string) error {
	return r.db.WithContext(ctx).
		Scopes(TenantScope(orgID)).
		Where("file_group = ?", fileGroup).
		Delete(&HeartbeatTaskModel{}).Error
}

func (r *HeartbeatTaskRepository) GetDueTasks(ctx context.Context, orgID uuid.UUID, now time.Time) ([]domain.HeartbeatTask, error) {
	var models []HeartbeatTaskModel
	if err := r.db.WithContext(ctx).
		Scopes(TenantScope(orgID)).
		Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
		Where("enabled = ? AND next_run_at IS NOT NULL AND next_run_at <= ?", true, now).
		Find(&models).Error; err != nil {
		return nil, err
	}
	result := make([]domain.HeartbeatTask, len(models))
	for i := range models {
		result[i] = *toHeartbeatTaskDomain(&models[i])
	}
	return result, nil
}

func (r *HeartbeatTaskRepository) RecordExecution(ctx context.Context, id uuid.UUID, nextRunAt time.Time, status, lastError, resultSummary string) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).
		Model(&HeartbeatTaskModel{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"last_run_at":         now,
			"last_status":         status,
			"last_error":          lastError,
			"last_result_summary": resultSummary,
			"next_run_at":         nextRunAt,
			"updated_at":          now,
		}).Error
}

// compile-time check
var _ heartbeattask.HeartbeatTaskStore = (*HeartbeatTaskRepository)(nil)

// HeartbeatTaskResultRepository implements heartbeattask.HeartbeatTaskResultStore using GORM.
type HeartbeatTaskResultRepository struct {
	db *gorm.DB
}

// NewHeartbeatTaskResultRepository creates a new HeartbeatTaskResultRepository.
func NewHeartbeatTaskResultRepository(db *gorm.DB) *HeartbeatTaskResultRepository {
	return &HeartbeatTaskResultRepository{db: db}
}

func (r *HeartbeatTaskResultRepository) Append(ctx context.Context, result *domain.HeartbeatTaskResult) error {
	m := toHeartbeatTaskResultModel(result)
	return r.db.WithContext(ctx).Create(&m).Error
}

func (r *HeartbeatTaskResultRepository) ListByTask(ctx context.Context, orgID, taskID uuid.UUID, limit int) ([]domain.HeartbeatTaskResult, error) {
	var models []HeartbeatTaskResultModel
	if err := r.db.WithContext(ctx).
		Where("org_id = ? AND task_id = ?", orgID, taskID).
		Order("created_at DESC").
		Limit(limit).
		Find(&models).Error; err != nil {
		return nil, err
	}
	result := make([]domain.HeartbeatTaskResult, len(models))
	for i := range models {
		result[i] = *toHeartbeatTaskResultDomain(&models[i])
	}
	return result, nil
}

func (r *HeartbeatTaskResultRepository) ListRecent(ctx context.Context, orgID uuid.UUID, limit int) ([]domain.HeartbeatTaskResult, error) {
	var models []HeartbeatTaskResultModel
	if err := r.db.WithContext(ctx).
		Where("org_id = ?", orgID).
		Order("created_at DESC").
		Limit(limit).
		Find(&models).Error; err != nil {
		return nil, err
	}
	result := make([]domain.HeartbeatTaskResult, len(models))
	for i := range models {
		result[i] = *toHeartbeatTaskResultDomain(&models[i])
	}
	return result, nil
}

// compile-time check
var _ heartbeattask.HeartbeatTaskResultStore = (*HeartbeatTaskResultRepository)(nil)
