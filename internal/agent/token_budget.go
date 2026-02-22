package agent

import (
	"encoding/json"

	"github.com/jkaninda/akili/internal/llm"
)

const maxInputTokens = 12000

func estimateTokens(s string) int {
	return (len(s) + 3) / 4
}

func estimateToolDefTokens(td llm.ToolDefinition) int {
	tokens := estimateTokens(td.Name) + estimateTokens(td.Description)
	if td.InputSchema != nil {
		b, _ := json.Marshal(td.InputSchema)
		tokens += estimateTokens(string(b))
	}
	return tokens
}

func estimateMessageTokens(msg llm.Message) int {
	tokens := estimateTokens(string(msg.Role)) + 4
	if msg.Content != "" {
		tokens += estimateTokens(msg.Content)
	}
	for _, b := range msg.ContentBlocks {
		tokens += estimateTokens(b.Text)
		if b.Input != nil {
			raw, _ := json.Marshal(b.Input)
			tokens += estimateTokens(string(raw))
		}
		tokens += estimateTokens(b.Name) + estimateTokens(b.ID) + estimateTokens(b.ToolUseID)
	}
	return tokens
}

func trimHistoryToTokenBudget(history []llm.Message, fixedTokens, maxTokens int) []llm.Message {
	budget := maxTokens - fixedTokens
	if budget <= 0 {
		budget = 2000 // safety minimum
	}

	total := 0
	for _, m := range history {
		total += estimateMessageTokens(m)
	}

	if total <= budget {
		return history
	}

	for len(history) > 2 && total > budget {
		dropped := estimateMessageTokens(history[0])
		history = history[1:]
		total -= dropped
	}

	// Ensure first message is a user message
	if len(history) > 0 && history[0].Role == llm.RoleAssistant {
		history = history[1:]
	}

	return history
}
