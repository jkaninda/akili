package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jkaninda/akili/internal/agent"
	"github.com/jkaninda/akili/internal/llm"
)

// Compile-time interface check.
var _ agent.ConversationStore = (*ConversationRepository)(nil)

// ConversationRepository implements agent.ConversationStore with PostgreSQL.
type ConversationRepository struct {
	db *gorm.DB
}

// NewConversationRepository creates a ConversationRepository.
func NewConversationRepository(db *gorm.DB) *ConversationRepository {
	return &ConversationRepository{db: db}
}

// GetOrCreateConversation returns an existing conversation or creates a new one.
// If the conversation exists, the userID is verified to prevent cross-user access.
func (r *ConversationRepository) GetOrCreateConversation(ctx context.Context, orgID uuid.UUID, userID string, convID uuid.UUID) (uuid.UUID, error) {
	var existing ConversationModel
	err := r.db.WithContext(ctx).
		Scopes(TenantScope(orgID)).
		Where("id = ?", convID).
		First(&existing).Error

	if err == nil {
		// Conversation exists — verify ownership.
		if existing.UserID != userID {
			return uuid.Nil, fmt.Errorf("conversation belongs to a different user")
		}
		// Touch updated_at.
		r.db.WithContext(ctx).Model(&existing).Update("updated_at", time.Now().UTC())
		return existing.ID, nil
	}

	if err != gorm.ErrRecordNotFound {
		return uuid.Nil, fmt.Errorf("looking up conversation: %w", err)
	}

	// Create new conversation.
	now := time.Now().UTC()
	model := ConversationModel{
		ID:        convID,
		OrgID:     orgID,
		UserID:    userID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return uuid.Nil, fmt.Errorf("creating conversation: %w", err)
	}

	return model.ID, nil
}

// AppendMessages atomically appends one or more messages to a conversation.
// Sequence numbers are monotonically assigned starting after the current max.
func (r *ConversationRepository) AppendMessages(ctx context.Context, convID, orgID uuid.UUID, msgs []llm.Message) error {
	if len(msgs) == 0 {
		return nil
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Get current max sequence number.
		var maxSeq int
		err := tx.Model(&ConversationMessageModel{}).
			Where("conversation_id = ?", convID).
			Select("COALESCE(MAX(seq_num), 0)").
			Scan(&maxSeq).Error
		if err != nil {
			return fmt.Errorf("getting max seq_num: %w", err)
		}

		models := make([]ConversationMessageModel, 0, len(msgs))
		for i, msg := range msgs {
			models = append(models, toConversationMessageModel(convID, orgID, maxSeq+i+1, msg))
		}

		if err := tx.Create(&models).Error; err != nil {
			return fmt.Errorf("inserting messages: %w", err)
		}

		return nil
	})
}

// LoadHistory returns the most recent messages for a conversation,
// ordered oldest-first (ascending seq_num).
func (r *ConversationRepository) LoadHistory(ctx context.Context, convID uuid.UUID, maxMessages int) ([]llm.Message, error) {
	if maxMessages <= 0 {
		maxMessages = agent.DefaultMaxHistoryMessages
	}

	// Subquery: get the N most recent by seq_num DESC, then re-order ASC.
	var models []ConversationMessageModel
	err := r.db.WithContext(ctx).
		Where("conversation_id = ?", convID).
		Order("seq_num DESC").
		Limit(maxMessages).
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("loading conversation history: %w", err)
	}

	// Reverse to oldest-first order.
	for i, j := 0, len(models)-1; i < j; i, j = i+1, j-1 {
		models[i], models[j] = models[j], models[i]
	}

	messages := make([]llm.Message, len(models))
	for i := range models {
		messages[i] = toMessage(&models[i])
	}

	return messages, nil
}

// ReplaceToolResult finds the tool_result block matching toolUseID and replaces
// its content and error flag. Used to update "approval pending" placeholders.
func (r *ConversationRepository) ReplaceToolResult(ctx context.Context, convID uuid.UUID, toolUseID, newContent string, isError bool) error {
	// Scan recent messages (the pending result is near the end of the conversation).
	var models []ConversationMessageModel
	err := r.db.WithContext(ctx).
		Where("conversation_id = ? AND content_blocks IS NOT NULL", convID).
		Order("seq_num DESC").
		Limit(20).
		Find(&models).Error
	if err != nil {
		return fmt.Errorf("loading messages for tool_result replacement: %w", err)
	}

	for _, m := range models {
		var blocks []llm.ContentBlock
		if err := json.Unmarshal(m.ContentBlocks, &blocks); err != nil {
			continue
		}

		found := false
		for i := range blocks {
			if blocks[i].Type == "tool_result" && blocks[i].ToolUseID == toolUseID {
				blocks[i].Text = newContent
				blocks[i].IsError = isError
				found = true
				break
			}
		}
		if !found {
			continue
		}

		data, err := json.Marshal(blocks)
		if err != nil {
			return fmt.Errorf("marshaling updated content_blocks: %w", err)
		}

		return r.db.WithContext(ctx).
			Model(&ConversationMessageModel{}).
			Where("id = ?", m.ID).
			Updates(map[string]any{
				"content_blocks": JSONB(data),
				"content":        newContent,
			}).Error
	}

	return fmt.Errorf("tool_result for tool_use_id %s not found", toolUseID)
}

// DeleteConversation removes all messages and the conversation record.
func (r *ConversationRepository) DeleteConversation(ctx context.Context, convID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Delete messages first (no FK cascade in GORM AutoMigrate by default).
		if err := tx.Where("conversation_id = ?", convID).Delete(&ConversationMessageModel{}).Error; err != nil {
			return fmt.Errorf("deleting conversation messages: %w", err)
		}
		if err := tx.Where("id = ?", convID).Delete(&ConversationModel{}).Error; err != nil {
			return fmt.Errorf("deleting conversation: %w", err)
		}
		return nil
	})
}
