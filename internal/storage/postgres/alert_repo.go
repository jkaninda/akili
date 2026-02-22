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

// AlertRuleRepository implements alert rule persistence with PostgreSQL.
type AlertRuleRepository struct {
	db *gorm.DB
}

// NewAlertRuleRepository creates an AlertRuleRepository.
func NewAlertRuleRepository(db *gorm.DB) *AlertRuleRepository {
	return &AlertRuleRepository{db: db}
}

// Create persists a new alert rule.
func (r *AlertRuleRepository) Create(ctx context.Context, rule *domain.AlertRule) error {
	model := toAlertRuleModel(rule)
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return fmt.Errorf("creating alert rule: %w", err)
	}
	return nil
}

// Get retrieves an alert rule by ID within an org.
func (r *AlertRuleRepository) Get(ctx context.Context, orgID, id uuid.UUID) (*domain.AlertRule, error) {
	var model AlertRuleModel
	if err := r.db.WithContext(ctx).
		Scopes(TenantScope(orgID)).
		First(&model, "id = ?", id).Error; err != nil {
		return nil, fmt.Errorf("getting alert rule %s: %w", id, err)
	}
	return toAlertRuleDomain(&model), nil
}

// List returns all alert rules for an org.
func (r *AlertRuleRepository) List(ctx context.Context, orgID uuid.UUID) ([]domain.AlertRule, error) {
	var models []AlertRuleModel
	if err := r.db.WithContext(ctx).
		Scopes(TenantScope(orgID)).
		Order("created_at DESC").
		Find(&models).Error; err != nil {
		return nil, fmt.Errorf("listing alert rules: %w", err)
	}
	rules := make([]domain.AlertRule, len(models))
	for i := range models {
		rules[i] = *toAlertRuleDomain(&models[i])
	}
	return rules, nil
}

// Update persists changes to an existing alert rule.
func (r *AlertRuleRepository) Update(ctx context.Context, rule *domain.AlertRule) error {
	model := toAlertRuleModel(rule)
	if err := r.db.WithContext(ctx).Save(&model).Error; err != nil {
		return fmt.Errorf("updating alert rule: %w", err)
	}
	return nil
}

// Delete soft-deletes an alert rule by ID within an org.
func (r *AlertRuleRepository) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	result := r.db.WithContext(ctx).
		Scopes(TenantScope(orgID)).
		Delete(&AlertRuleModel{}, "id = ?", id)
	if result.Error != nil {
		return fmt.Errorf("deleting alert rule %s: %w", id, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("alert rule %s not found", id)
	}
	return nil
}

// GetDueAlerts returns enabled alert rules whose NextRunAt <= now,
// atomically locking them with SELECT ... FOR UPDATE SKIP LOCKED
// to prevent double-evaluation across multiple gateway instances.
func (r *AlertRuleRepository) GetDueAlerts(ctx context.Context, orgID uuid.UUID, now time.Time) ([]domain.AlertRule, error) {
	var models []AlertRuleModel
	if err := r.db.WithContext(ctx).
		Scopes(TenantScope(orgID)).
		Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
		Where("enabled = ? AND next_run_at IS NOT NULL AND next_run_at <= ?", true, now).
		Find(&models).Error; err != nil {
		return nil, fmt.Errorf("getting due alert rules: %w", err)
	}
	rules := make([]domain.AlertRule, len(models))
	for i := range models {
		rules[i] = *toAlertRuleDomain(&models[i])
	}
	return rules, nil
}

// RecordCheck updates the alert rule after a check has been evaluated.
func (r *AlertRuleRepository) RecordCheck(ctx context.Context, id uuid.UUID, nextRunAt time.Time, status, errMsg string) error {
	now := time.Now().UTC()
	updates := map[string]any{
		"last_checked_at": now,
		"last_status":     status,
		"next_run_at":     nextRunAt,
		"updated_at":      now,
	}
	if errMsg != "" {
		updates["last_status"] = "error"
	}
	if err := r.db.WithContext(ctx).
		Model(&AlertRuleModel{}).
		Where("id = ?", id).
		Updates(updates).Error; err != nil {
		return fmt.Errorf("recording check for alert rule %s: %w", id, err)
	}
	return nil
}

// RecordAlerted updates the last_alerted_at timestamp for an alert rule.
func (r *AlertRuleRepository) RecordAlerted(ctx context.Context, id uuid.UUID) error {
	now := time.Now().UTC()
	if err := r.db.WithContext(ctx).
		Model(&AlertRuleModel{}).
		Where("id = ?", id).
		Update("last_alerted_at", now).Error; err != nil {
		return fmt.Errorf("recording alerted for alert rule %s: %w", id, err)
	}
	return nil
}

// AlertHistoryRepository implements alert history persistence with PostgreSQL.
// Append-only — no update or delete methods.
type AlertHistoryRepository struct {
	db *gorm.DB
}

// NewAlertHistoryRepository creates an AlertHistoryRepository.
func NewAlertHistoryRepository(db *gorm.DB) *AlertHistoryRepository {
	return &AlertHistoryRepository{db: db}
}

// Append persists a new alert history record. Immutable once created.
func (r *AlertHistoryRepository) Append(ctx context.Context, h *domain.AlertHistory) error {
	model := toAlertHistoryModel(h)
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return fmt.Errorf("appending alert history: %w", err)
	}
	return nil
}

// ListByRule returns alert history for a specific rule, ordered by most recent first.
func (r *AlertHistoryRepository) ListByRule(ctx context.Context, orgID, ruleID uuid.UUID, limit int) ([]domain.AlertHistory, error) {
	if limit <= 0 {
		limit = 50
	}
	var models []AlertHistoryModel
	if err := r.db.WithContext(ctx).
		Scopes(TenantScope(orgID)).
		Where("alert_rule_id = ?", ruleID).
		Order("created_at DESC").
		Limit(limit).
		Find(&models).Error; err != nil {
		return nil, fmt.Errorf("listing alert history for rule %s: %w", ruleID, err)
	}
	history := make([]domain.AlertHistory, len(models))
	for i := range models {
		history[i] = *toAlertHistoryDomain(&models[i])
	}
	return history, nil
}

// ListRecent returns the most recent alert history across all rules for an org.
func (r *AlertHistoryRepository) ListRecent(ctx context.Context, orgID uuid.UUID, limit int) ([]domain.AlertHistory, error) {
	if limit <= 0 {
		limit = 50
	}
	var models []AlertHistoryModel
	if err := r.db.WithContext(ctx).
		Scopes(TenantScope(orgID)).
		Order("created_at DESC").
		Limit(limit).
		Find(&models).Error; err != nil {
		return nil, fmt.Errorf("listing recent alert history: %w", err)
	}
	history := make([]domain.AlertHistory, len(models))
	for i := range models {
		history[i] = *toAlertHistoryDomain(&models[i])
	}
	return history, nil
}
