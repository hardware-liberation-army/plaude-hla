package llm

import (
	"context"
	"fmt"
	"regexp"
)

const summarySystemPrompt = `You are a concise, insightful executive assistant. The user will provide you with a daily log of voice memo transcriptions from their day. Your job is to produce an executive summary that:

1. Identifies the key topics and themes discussed across the recordings
2. Highlights any action items, decisions, or follow-ups mentioned
3. Calls out any important discoveries, insights, or realizations
4. Notes any deadlines, commitments, or appointments mentioned

Format the summary as clean markdown with:
- A brief opening paragraph summarizing the overall day
- A "## Key Topics" section with bullet points
- A "## Action Items" section (if any actions were mentioned)
- A "## Key Discoveries & Insights" section (if any insights were noted)
- A "## Decisions & Commitments" section (if any were made)

IMPORTANT markdown formatting rules:
- Headings must be plain markdown: "## Section Name" — do NOT wrap headings in bold (**)
- Use **bold** only for emphasis within body text, not on heading lines
- Do not add bold markers before or after the ## markers
Be specific - reference actual topics, names, and details from the recordings rather than being generic.
If a section would have no content, omit it entirely.
Keep the summary focused and actionable - the user wants to quickly recall what
mattered from their day.

CRITICAL: Only include information, names, events, and details that are explicitly
present in the daily log. Do NOT infer, fabricate, or add details from external
knowledge or prior context, even if they seem plausible. If something is not stated
in the daily log, it must not appear in the summary.`

var boldHeadingRe = regexp.MustCompile(`(?m)^\*\*(#{1,6}\s.+?)\*\*\s*$`)

func cleanSummaryMarkdown(s string) string {
	return boldHeadingRe.ReplaceAllString(s, "$1")
}

func GenerateSummary(ctx context.Context, client *Client, logContent string, memoryContext string) (string, error) {
	systemPrompt := summarySystemPrompt
	if memoryContext != "" {
		systemPrompt += "\n\n" + memoryContext + "\n\nThe terms above may appear in the transcript as misspellings or aliases. If you reference a term that appears in the transcript, use the canonical spelling shown above. Do NOT introduce any of these terms into the summary unless they are explicitly mentioned in the transcript."
	}

	userContent := fmt.Sprintf("Here is my daily log of voice memos from today. Please provide an executive summary:\n\n%s", logContent)

	result, err := client.Chat(ctx, systemPrompt, userContent)
	if err != nil {
		return "", fmt.Errorf("generating summary: %w", err)
	}

	return cleanSummaryMarkdown(result), nil
}
