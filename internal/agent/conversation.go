package agent

import (
	"context"

	"github.com/google/uuid"

	"github.com/jkaninda/akili/internal/llm"
)

// ConversationStore persists conversation history.
type ConversationStore interface {
	// GetOrCreateConversation returns an existing conversation or creates a new one.
	// The userID is verified on existing conversations to prevent cross-user access.
	GetOrCreateConversation(ctx context.Context, orgID uuid.UUID, userID string, convID uuid.UUID) (uuid.UUID, error)

	// AppendMessages atomically appends one or more messages to a conversation.
	AppendMessages(ctx context.Context, convID, orgID uuid.UUID, msgs []llm.Message) error

	// LoadHistory returns the most recent messages for a conversation,
	// up to maxMessages, ordered oldest-first.
	LoadHistory(ctx context.Context, convID uuid.UUID, maxMessages int) ([]llm.Message, error)

	// ReplaceToolResult finds the tool_result block matching toolUseID in the
	ReplaceToolResult(ctx context.Context, convID uuid.UUID, toolUseID, newContent string, isError bool) error

	// DeleteConversation removes all messages and the conversation record.
	DeleteConversation(ctx context.Context, convID uuid.UUID) error
}

// DefaultMaxHistoryMessages is the default cap on messages loaded per conversation.
const DefaultMaxHistoryMessages = 100

// DefaultMaxMessageBytes is the default per-message content size limit (32 KB).
const DefaultMaxMessageBytes = 32768
