package llm

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

const (
	// summarizeThreshold triggers summarization at 60% of max context.
	summarizeThreshold = 0.60
	// keepRecentMessages is the number of recent messages preserved during summarization.
	keepRecentMessages = 20
	// charsPerToken is the rough estimate for token counting.
	charsPerToken = 4
)

// Summarizer is a callback that compresses text into a shorter summary.
type Summarizer func(ctx context.Context, text string) (string, error)

// ConversationManager holds the full message history in Go memory
// and handles sliding window summarization when approaching context limits.
type ConversationManager struct {
	mu           sync.Mutex
	messages     []Message
	systemPrompt string
	maxTokens    int
	summarizer   Summarizer
}

// NewConversationManager creates a conversation with the given system prompt
// and context window limit (in tokens).
func NewConversationManager(systemPrompt string, maxTokens int) *ConversationManager {
	return &ConversationManager{
		systemPrompt: systemPrompt,
		maxTokens:    maxTokens,
	}
}

// SetSummarizer sets the callback used for context compression.
// Called after the OllamaClient is created to break circular dependency.
func (cm *ConversationManager) SetSummarizer(fn Summarizer) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.summarizer = fn
}

// Append adds a message to the conversation history.
func (cm *ConversationManager) Append(msg Message) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.messages = append(cm.messages, msg)
}

// Messages returns the full message history with the system prompt prepended.
func (cm *ConversationManager) Messages() []Message {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	msgs := make([]Message, 0, len(cm.messages)+1)
	msgs = append(msgs, Message{
		Role:    RoleSystem,
		Content: cm.systemPrompt,
	})
	msgs = append(msgs, cm.messages...)
	return msgs
}

// EstimateTokens returns a rough token count for the conversation.
func (cm *ConversationManager) EstimateTokens() int {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.estimateTokensLocked()
}

func (cm *ConversationManager) estimateTokensLocked() int {
	total := len(cm.systemPrompt) / charsPerToken
	for _, msg := range cm.messages {
		total += len(msg.Content) / charsPerToken
		for _, tc := range msg.ToolCalls {
			// Rough estimate for tool call JSON.
			total += len(tc.Function.Name) / charsPerToken
			for k, v := range tc.Function.Arguments {
				total += len(k) / charsPerToken
				if s, ok := v.(string); ok {
					total += len(s) / charsPerToken
				} else {
					total += 20 // estimate for non-string values
				}
			}
		}
	}
	return total
}

// NeedsSummarization returns true if the conversation is approaching the context limit.
func (cm *ConversationManager) NeedsSummarization() bool {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	threshold := int(float64(cm.maxTokens) * summarizeThreshold)
	return cm.estimateTokensLocked() > threshold
}

// Summarize compresses old messages to free context space.
// Keeps the system prompt and the most recent keepRecentMessages messages.
// Everything in between gets summarized into a single message.
func (cm *ConversationManager) Summarize(ctx context.Context) error {
	cm.mu.Lock()
	if cm.summarizer == nil {
		cm.mu.Unlock()
		return fmt.Errorf("no summarizer configured")
	}
	if len(cm.messages) <= keepRecentMessages {
		cm.mu.Unlock()
		return nil // nothing to summarize
	}

	// Split: old messages to summarize, recent messages to keep.
	cutoff := len(cm.messages) - keepRecentMessages
	oldMessages := make([]Message, cutoff)
	copy(oldMessages, cm.messages[:cutoff])
	recentMessages := make([]Message, keepRecentMessages)
	copy(recentMessages, cm.messages[cutoff:])
	summarizer := cm.summarizer
	cm.mu.Unlock()

	// Build text from old messages for summarization.
	var sb strings.Builder
	for _, msg := range oldMessages {
		sb.WriteString(fmt.Sprintf("[%s]: %s\n", msg.Role, msg.Content))
		for _, tc := range msg.ToolCalls {
			sb.WriteString(fmt.Sprintf("[tool_call]: %s(%v)\n", tc.Function.Name, tc.Function.Arguments))
		}
	}

	summary, err := summarizer(ctx, sb.String())
	if err != nil {
		return fmt.Errorf("summarize: %w", err)
	}

	// Replace old messages with the summary + recent messages.
	cm.mu.Lock()
	defer cm.mu.Unlock()

	summarizedMsg := Message{
		Role:    RoleSystem,
		Content: "Conversation summary so far:\n" + summary,
	}

	cm.messages = make([]Message, 0, 1+len(recentMessages))
	cm.messages = append(cm.messages, summarizedMsg)
	cm.messages = append(cm.messages, recentMessages...)

	return nil
}

// Reset clears the conversation history (keeps system prompt).
func (cm *ConversationManager) Reset() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.messages = nil
}

// Len returns the number of messages (excluding system prompt).
func (cm *ConversationManager) Len() int {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return len(cm.messages)
}
