package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"aplaud/internal/llm"
)

const extractSystemPrompt = `You are an entity extraction assistant. Analyze voice memo transcriptions and identify proper nouns, project names, people, companies, technical terms, and domain-specific concepts.

For each entity found, return a JSON object with:
- "term": the correct/official spelling
- "category": one of: person, project, company, technology, term, concept
- "description": a brief 1-sentence description
- "aliases": any alternative spellings, abbreviations, or transcription errors found in the text (include lowercase/misspelled variants)

IMPORTANT:
- Voice transcriptions often misspell names and terms. Use context clues to determine the correct spelling.
- Only extract entities that are specific proper nouns, named concepts, or domain-specific terms.
- Do NOT extract common English words, dates, times, or generic phrases.
- If a term already exists in the known terms list, do NOT re-extract it unless you find new aliases.

Return ONLY a valid JSON array of objects, no other text. If no new entities are found, return [].`

type extractedEntity struct {
	Term        string   `json:"term"`
	Category    string   `json:"category"`
	Description string   `json:"description"`
	Aliases     []string `json:"aliases"`
}

func ExtractEntities(ctx context.Context, client *llm.Client, dailyLog string, existingTerms []string) ([]Entity, error) {
	var existingSection string
	if len(existingTerms) > 0 {
		existingSection = fmt.Sprintf("\n\nThese terms are already known (do NOT re-extract them, but DO include new alias variants you find):\n%s", formatTermList(existingTerms))
	}

	userContent := fmt.Sprintf("Analyze this daily log and extract new entities:%s\n\n---\n%s", existingSection, dailyLog)

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	result, err := client.Chat(ctx, extractSystemPrompt, userContent)
	if err != nil {
		return nil, fmt.Errorf("entity extraction LLM call: %w", err)
	}

	return parseEntityResponse(result)
}

func formatTermList(terms []string) string {
	if len(terms) > 50 {
		return strings.Join(terms[:50], ", ") + fmt.Sprintf(" (and %d more)", len(terms)-50)
	}
	return strings.Join(terms, ", ")
}

const editDiffSystemPrompt = `You are analyzing user corrections to an AI-generated summary. The user manually edited the summary to fix mistakes.

Compare the ORIGINAL and EDITED versions. Only identify:
1. Name spelling corrections (e.g., "Jon Smith" → "John Smith")
2. Technical term spelling corrections (e.g., "kubernates" → "Kubernetes")
3. Terminology preference corrections (e.g., "frontend" → "front-end")

For each correction, return a JSON object with:
- "term": the CORRECTED form (from the edited version)
- "category": one of: person, project, company, technology, term, concept
- "description": "" (leave empty)
- "aliases": the INCORRECT or original forms that were replaced

IMPORTANT:
- Only return entities where the user corrected a SPELLING or TERMINOLOGY error.
- Do NOT return entities for text that was merely reworded, removed, or restructured.
- Do NOT infer descriptions or add entities that weren't explicitly corrected.
- Leave "description" as an empty string "" for all entities.
- If no spelling/terminology corrections were found, return [].

Return ONLY a valid JSON array of objects, no other text.`

func ExtractFromEditDiff(ctx context.Context, client *llm.Client, original, edited string, existingTerms []string) ([]Entity, error) {
	if original == edited {
		return nil, nil
	}

	var existingSection string
	if len(existingTerms) > 0 {
		existingSection = fmt.Sprintf("\n\nAlready known terms (skip these unless you find new aliases):\n%s", formatTermList(existingTerms))
	}

	userContent := fmt.Sprintf(`ORIGINAL SUMMARY:
---
%s
---

EDITED SUMMARY (user's corrections):
---
%s
---%s

Identify the corrections and new terms the user added.`, original, edited, existingSection)

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	result, err := client.Chat(ctx, editDiffSystemPrompt, userContent)
	if err != nil {
		return nil, fmt.Errorf("edit diff extraction LLM call: %w", err)
	}

	return parseEntityResponse(result)
}

func parseEntityResponse(result string) ([]Entity, error) {
	result = strings.TrimSpace(result)

	if idx := strings.Index(result, "["); idx > 0 {
		result = result[idx:]
	}
	if lastIdx := strings.LastIndex(result, "]"); lastIdx >= 0 {
		result = result[:lastIdx+1]
	}

	if result == "[]" || result == "" {
		return nil, nil
	}

	var extracted []extractedEntity
	if err := json.Unmarshal([]byte(result), &extracted); err != nil {
		log.Printf("memory: failed to parse entity extraction response: %v", err)
		log.Printf("memory: raw response was: %s", result)
		return nil, nil
	}

	var entities []Entity
	for _, e := range extracted {
		if e.Term == "" || e.Category == "" {
			continue
		}
		e.Term = cleanTerm(e.Term)
		e.Category = normalizeCategory(e.Category)
		if e.Term == "" {
			continue
		}
		cleanAliases := make([]string, 0, len(e.Aliases))
		for _, a := range e.Aliases {
			a = strings.TrimSpace(a)
			if a != "" && a != e.Term {
				cleanAliases = append(cleanAliases, a)
			}
		}
		entities = append(entities, Entity{
			Term:        e.Term,
			Category:    e.Category,
			Description: strings.TrimSpace(e.Description),
			Aliases:     cleanAliases,
		})
	}

	return entities, nil
}

func cleanTerm(term string) string {
	term = strings.TrimSpace(term)
	term = strings.Trim(term, "\"'`*#")
	term = strings.TrimSpace(term)
	return term
}

func normalizeCategory(cat string) string {
	cat = strings.ToLower(strings.TrimSpace(cat))
	switch cat {
	case "person", "people":
		return "person"
	case "project":
		return "project"
	case "company", "organization", "org":
		return "company"
	case "technology", "tech", "tool", "framework", "language":
		return "technology"
	case "term", "acronym", "abbreviation":
		return "term"
	case "concept", "idea", "method", "methodology":
		return "concept"
	default:
		return "term"
	}
}
