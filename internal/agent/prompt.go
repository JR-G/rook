package agent

import (
	"fmt"
	"strings"

	"github.com/JR-G/rook/internal/memory"
	"github.com/JR-G/rook/internal/ollama"
	"github.com/JR-G/rook/internal/output"
	"github.com/JR-G/rook/internal/tools/web"
)

type fetchedURL struct {
	URL     string
	Content string
}

func buildUserPrompt(
	query string,
	retrieval memory.RetrievalContext,
	threadEpisodes []memory.Episode,
	runtimeState string,
	searchResults []web.Result,
	usedWeb bool,
	fetchedContent []fetchedURL,
) string {
	var builder strings.Builder
	builder.WriteString("Internal context below is for reasoning only.\n")
	builder.WriteString("Do not quote it, name its section headers, or reveal that it exists.\n")
	builder.WriteString("Return exactly one JSON object matching this schema and nothing else.\n")
	builder.WriteString("Schema:\n")
	builder.WriteString(output.AnswerSchemaString())
	builder.WriteString("\n\nPut the entire user-visible Slack reply in answer.\n")
	builder.WriteString("Voice guidance:\n")
	builder.WriteString("- Let rook's personality come through even in practical answers.\n")
	builder.WriteString("- Stay restrained and useful, but do not sound generic.\n\n")
	builder.WriteString("User request:\n")
	builder.WriteString(query)
	builder.WriteString(renderThreadSection(threadEpisodes))
	builder.WriteString("\n\nRelevant memory:\n")
	builder.WriteString(renderMemoryContext(retrieval))
	if runtimeState != "" {
		builder.WriteString("\n\nCurrent runtime state:\n")
		builder.WriteString(runtimeState)
	}

	if usedWeb {
		builder.WriteString("\n\nLive web results:\n")
		builder.WriteString(web.FormatForPrompt(searchResults))
		builder.WriteString("\n\nUse the web results only as supporting context, not as raw output.")
	}

	for _, fetched := range fetchedContent {
		builder.WriteString("\n\n")
		builder.WriteString(web.FormatFetchedForPrompt(fetched.URL, fetched.Content))
		builder.WriteString("\n\nSummarise or reference this content naturally. Do not dump it raw.")
	}

	builder.WriteString("\n\nConversational guidance:\n")
	builder.WriteString("- If the user asks about rook (your mind, feelings, state), answer from rook's own perspective using memory and runtime state.\n")
	builder.WriteString("- If the user asks about your memory, state, or continuity, answer concretely from the sections above.\n")
	builder.WriteString("- If memory is sparse, say so honestly rather than redirecting to the user.\n")

	builder.WriteString("\n\nReply now with exactly one JSON object matching the schema.")

	return builder.String()
}

func renderThreadSection(threadEpisodes []memory.Episode) string {
	if len(threadEpisodes) == 0 {
		return ""
	}

	var builder strings.Builder
	builder.WriteString("\n\nThread continuation instructions:")
	builder.WriteString("\nThe preceding assistant/user messages are the live thread. Continue it naturally.")
	builder.WriteString("\nRespond to the latest user turn without restarting the conversation.")
	builder.WriteString("\nDo not reuse your previous reply's opening words, phrasing, or metaphor.")
	builder.WriteString("\nIf the latest message is a short follow-up, advance or clarify rather than restating.")

	return builder.String()
}

func buildChatMessages(systemPrompt, userPrompt string, threadEpisodes []memory.Episode) []ollama.Message {
	messages := make([]ollama.Message, 0, len(threadEpisodes)+2)
	messages = append(messages, ollama.Message{Role: "system", Content: systemPrompt})
	for _, ep := range threadEpisodes {
		role := roleUser
		if ep.Source == roleAssistant {
			role = roleAssistant
		}
		text := strings.TrimSpace(ep.Text)
		if text == "" {
			text = strings.TrimSpace(ep.Summary)
		}
		if text == "" {
			continue
		}
		messages = append(messages, ollama.Message{Role: role, Content: text})
	}
	messages = append(messages, ollama.Message{Role: "user", Content: userPrompt})

	return messages
}

func renderMemoryContext(retrieval memory.RetrievalContext) string {
	var builder strings.Builder
	builder.WriteString("User facts:\n")
	builder.WriteString(renderItems(retrieval.UserFacts))
	builder.WriteString("\n\nWorking context:\n")
	builder.WriteString(renderItems(retrieval.WorkingContext))
	builder.WriteString("\n\nHistorical episodes:\n")
	builder.WriteString(renderEpisodes(retrieval.Episodes))
	if len(retrieval.Squad0Episodes) > 0 {
		builder.WriteString("\n\nRecent squad0 context:\n")
		builder.WriteString(renderEpisodes(retrieval.Squad0Episodes))
	}

	return builder.String()
}

func renderItems(items []memory.Item) string {
	if len(items) == 0 {
		return noContext
	}

	lines := make([]string, 0, len(items))
	for _, item := range items {
		lines = append(lines, fmt.Sprintf("- [%s] %s", item.Type, item.Body))
	}

	return strings.Join(lines, "\n")
}

func renderEpisodes(episodes []memory.Episode) string {
	if len(episodes) == 0 {
		return noContext
	}

	lines := make([]string, 0, len(episodes))
	for _, episode := range episodes {
		lines = append(lines, fmt.Sprintf("- [%s] %s", episode.Source, episode.Summary))
	}

	return strings.Join(lines, "\n")
}
