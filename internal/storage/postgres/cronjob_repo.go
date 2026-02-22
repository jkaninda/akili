package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/jkaninda/akili/internal/domain"
)

// CronJobRepository implements cron job persistence with PostgreSQL.
type CronJobRepository struct {
	db *gorm.DB
}

// NewCronJobRepository creates a CronJobRepository.
func NewCronJobRepository(db *gorm.DB) *CronJobRepository {
	return &CronJobRepository{db: db}
}

// Create persists a new cron job.
func (r *CronJobRepository) Create(ctx context.Context, cj *domain.CronJob) error {
	model := toCronJobModel(cj)
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return fmt.Errorf("creating cron job: %w", err)
	}
	return nil
}

// Get retrieves a cron job by ID within an org.
func (r *CronJobRepository) Get(ctx context.Context, orgID, id uuid.UUID) (*domain.CronJob, error) {
	var model CronJobModel
	if err := r.db.WithContext(ctx).
		Scopes(TenantScope(orgID)).
		First(&model, "id = ?", id).Error; err != nil {
		return nil, fmt.Errorf("getting cron job %s: %w", id, err)
	}
	return toCronJobDomain(&model), nil
}

// List returns all cron jobs for an org.
func (r *CronJobRepository) List(ctx context.Context, orgID uuid.UUID) ([]domain.CronJob, error) {
	var models []CronJobModel
	if err := r.db.WithContext(ctx).
		Scopes(TenantScope(orgID)).
		Order("created_at DESC").
		Find(&models).Error; err != nil {
		return nil, fmt.Errorf("listing cron jobs: %w", err)
	}
	jobs := make([]domain.CronJob, len(models))
	for i := range models {
		jobs[i] = *toCronJobDomain(&models[i])
	}
	return jobs, nil
}

// Update persists changes to an existing cron job.
func (r *CronJobRepository) Update(ctx context.Context, cj *domain.CronJob) error {
	model := toCronJobModel(cj)
	if err := r.db.WithContext(ctx).Save(&model).Error; err != nil {
		return fmt.Errorf("updating cron job: %w", err)
	}
	return nil
}

// Delete soft-deletes a cron job by ID within an org.
func (r *CronJobRepository) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	result := r.db.WithContext(ctx).
		Scopes(TenantScope(orgID)).
		Delete(&CronJobModel{}, "id = ?", id)
	if result.Error != nil {
		return fmt.Errorf("deleting cron job %s: %w", id, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("cron job %s not found", id)
	}
	return nil
}

// GetDueJobs returns enabled cron jobs whose NextRunAt <= now,
// atomically locking them with SELECT ... FOR UPDATE SKIP LOCKED
// to prevent double-firing across multiple gateway instances.
func (r *CronJobRepository) GetDueJobs(ctx context.Context, orgID uuid.UUID, now time.Time) ([]domain.CronJob, error) {
	var models []CronJobModel
	if err := r.db.WithContext(ctx).
		Scopes(TenantScope(orgID)).
		Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
		Where("enabled = ? AND next_run_at IS NOT NULL AND next_run_at <= ?", true, now).
		Find(&models).Error; err != nil {
		return nil, fmt.Errorf("getting due cron jobs: %w", err)
	}
	jobs := make([]domain.CronJob, len(models))
	for i := range models {
		jobs[i] = *toCronJobDomain(&models[i])
	}
	return jobs, nil
}

// RecordExecution updates the job after a workflow has been triggered.
func (r *CronJobRepository) RecordExecution(ctx context.Context, id uuid.UUID, workflowID uuid.UUID, nextRunAt time.Time, errMsg string) error {
	now := time.Now().UTC()
	updates := map[string]any{
		"last_run_at":      now,
		"last_workflow_id": workflowID,
		"last_error":       errMsg,
		"next_run_at":      nextRunAt,
		"updated_at":       now,
	}
	if err := r.db.WithContext(ctx).
		Model(&CronJobModel{}).
		Where("id = ?", id).
		Updates(updates).Error; err != nil {
		return fmt.Errorf("recording execution for cron job %s: %w", id, err)
	}
	return nil
}
