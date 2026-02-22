package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"

	"github.com/jkaninda/akili/internal/llm"
)

// InMemoryConversationStore implements ConversationStore without persistence.
// History is lost on restart.
// Used when no database is configured.
type InMemoryConversationStore struct {
	mu      sync.RWMutex
	history map[uuid.UUID][]llm.Message
	owners  map[uuid.UUID]string
}

// NewInMemoryConversationStore creates an ephemeral conversation store.
func NewInMemoryConversationStore() *InMemoryConversationStore {
	return &InMemoryConversationStore{
		history: make(map[uuid.UUID][]llm.Message),
		owners:  make(map[uuid.UUID]string),
	}
}

func (s *InMemoryConversationStore) GetOrCreateConversation(
	_ context.Context, _ uuid.UUID, userID string, convID uuid.UUID,
) (uuid.UUID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if owner, ok := s.owners[convID]; ok {
		if owner != userID {
			return uuid.Nil, fmt.Errorf("conversation belongs to a different user")
		}
		return convID, nil
	}

	s.owners[convID] = userID
	s.history[convID] = nil
	return convID, nil
}

func (s *InMemoryConversationStore) AppendMessages(
	_ context.Context, convID, _ uuid.UUID, msgs []llm.Message,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history[convID] = append(s.history[convID], msgs...)
	return nil
}

func (s *InMemoryConversationStore) LoadHistory(
	_ context.Context, convID uuid.UUID, maxMessages int,
) ([]llm.Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	hist := s.history[convID]
	if maxMessages > 0 && len(hist) > maxMessages {
		hist = hist[len(hist)-maxMessages:]
	}

	cp := make([]llm.Message, len(hist))
	copy(cp, hist)
	return cp, nil
}

func (s *InMemoryConversationStore) ReplaceToolResult(
	_ context.Context, convID uuid.UUID, toolUseID, newContent string, isError bool,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	msgs := s.history[convID]
	// Scan from newest to oldest
	for i := len(msgs) - 1; i >= 0; i-- {
		for j := range msgs[i].ContentBlocks {
			b := &msgs[i].ContentBlocks[j]
			if b.Type == "tool_result" && b.ToolUseID == toolUseID {
				b.Text = newContent
				b.IsError = isError
				return nil
			}
		}
	}
	return fmt.Errorf("tool_result for tool_use_id %s not found", toolUseID)
}

func (s *InMemoryConversationStore) DeleteConversation(
	_ context.Context, convID uuid.UUID,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.history, convID)
	delete(s.owners, convID)
	return nil
}

// Compile-time interface check.
var _ ConversationStore = (*InMemoryConversationStore)(nil)
