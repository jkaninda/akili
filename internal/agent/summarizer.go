package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jkaninda/akili/internal/llm"
)

const summarizationPrompt = `Summarize the following conversation history concisely.
Preserve: key facts, decisions made, tool outputs, errors encountered, and any important context.
Omit: greetings, redundant explanations, and verbose tool output details.
Format as a brief paragraph.`

// SummarizeThreshold is the fraction of max history at which summarization triggers.
const SummarizeThreshold = 0.8

// summarizeHistory compresses older messages into a single summary message.
func summarizeHistory(ctx context.Context, provider llm.Provider, history []llm.Message, maxMessages int, logger *slog.Logger) []llm.Message {
	threshold := int(float64(maxMessages) * SummarizeThreshold)
	if len(history) < threshold {
		return history
	}

	// Keep the most recent 40% of messages intact.
	keepRecent := int(float64(maxMessages) * 0.4)
	if keepRecent < 2 {
		keepRecent = 2
	}
	toSummarize := len(history) - keepRecent
	if toSummarize < 4 {
		return history
	}

	// Build text from older messages for summarization.
	var sb strings.Builder
	for _, msg := range history[:toSummarize] {
		content := msg.TextContent()
		if content == "" {
			continue
		}
		sb.WriteString(fmt.Sprintf("[%s]: %s\n", msg.Role, content))
	}

	if sb.Len() == 0 {
		return history
	}

	// Call LLM to summarize.
	resp, err := provider.SendMessage(ctx, &llm.Request{
		SystemPrompt: summarizationPrompt,
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: sb.String()},
		},
		MaxTokens: 1024,
	})
	if err != nil {
		logger.WarnContext(ctx, "conversation summarization failed, skipping",
			slog.String("error", err.Error()),
		)
		return history
	}

	summary := resp.Content
	if summary == "" {
		return history
	}

	// Build new history
	newHistory := make([]llm.Message, 0, keepRecent+1)
	newHistory = append(newHistory, llm.Message{
		Role:    llm.RoleUser,
		Content: fmt.Sprintf("[Conversation Summary]\n%s", summary),
	})
	newHistory = append(newHistory, history[toSummarize:]...)

	logger.InfoContext(ctx, "conversation history summarized",
		slog.Int("original_messages", len(history)),
		slog.Int("summarized_messages", toSummarize),
		slog.Int("new_total", len(newHistory)),
	)

	return newHistory
}
