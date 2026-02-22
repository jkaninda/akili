package postgres

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/jkaninda/akili/internal/llm"
)

// sanitizeRole enforces that only "user" and "assistant" roles are stored.
// Unknown roles default to "user" to prevent injection of system messages.
func sanitizeRole(role llm.Role) string {
	switch role {
	case llm.RoleUser:
		return "user"
	case llm.RoleAssistant:
		return "assistant"
	default:
		return "user"
	}
}

// estimateTokens provides a rough token count using the ~4 chars/token heuristic.
func estimateTokens(text string) int {
	n := len(text) / 4
	if n == 0 && len(text) > 0 {
		n = 1
	}
	return n
}

// toConversationMessageModel converts an llm.Message to a GORM model for persistence.
func toConversationMessageModel(convID, orgID uuid.UUID, seqNum int, msg llm.Message) ConversationMessageModel {
	role := sanitizeRole(msg.Role)

	var contentBlocks JSONB
	if len(msg.ContentBlocks) > 0 {
		data, _ := json.Marshal(msg.ContentBlocks)
		if data != nil {
			contentBlocks = JSONB(data)
		}
	}

	text := msg.TextContent()

	return ConversationMessageModel{
		ID:             uuid.New(),
		ConversationID: convID,
		OrgID:          orgID,
		SeqNum:         seqNum,
		Role:           role,
		Content:        text,
		ContentBlocks:  contentBlocks,
		TokenEstimate:  estimateTokens(text),
		CreatedAt:      time.Now().UTC(),
	}
}

// toMessage converts a GORM model back to an llm.Message.
func toMessage(m *ConversationMessageModel) llm.Message {
	msg := llm.Message{
		Role:    llm.Role(m.Role),
		Content: m.Content,
	}

	if len(m.ContentBlocks) > 0 {
		var blocks []llm.ContentBlock
		if err := json.Unmarshal(m.ContentBlocks, &blocks); err == nil && len(blocks) > 0 {
			msg.ContentBlocks = blocks
			msg.Content = "" // Structured content takes precedence.
		}
	}

	return msg
}
