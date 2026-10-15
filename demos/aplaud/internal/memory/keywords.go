package memory

import (
	"regexp"
	"strings"
)

var (
	multiCapitalRe = regexp.MustCompile(`[A-Z][a-z]+(?:\s+[A-Z][a-z]+)+`)
	acronymRe      = regexp.MustCompile(`\b[A-Z]{3,}\b`)
	capitalWordRe  = regexp.MustCompile(`\b[A-Z][a-z]+\b`)
)

var stopWords = map[string]bool{
	"The": true, "This": true, "That": true, "Then": true, "There": true,
	"They": true, "Their": true, "These": true, "Those": true,
	"What": true, "When": true, "Where": true, "Which": true, "With": true,
	"From": true, "Into": true, "About": true, "Just": true, "Like": true,
	"Over": true, "Also": true, "Some": true, "Very": true, "Much": true,
	"Many": true, "More": true, "Most": true, "Only": true, "Been": true,
	"Have": true, "Will": true, "Would": true, "Could": true, "Should": true,
	"Does": true, "Going": true, "Being": true, "Because": true, "Before": true,
	"After": true, "Between": true, "Through": true, "During": true, "Without": true,
	"Monday": true, "Tuesday": true, "Wednesday": true, "Thursday": true,
	"Friday": true, "Saturday": true, "Sunday": true,
	"January": true, "February": true, "March": true, "April": true,
	"May": true, "June": true, "July": true, "August": true,
	"September": true, "October": true, "November": true, "December": true,
	"Today": true, "Tomorrow": true, "Yesterday": true, "Morning": true,
	"Afternoon": true, "Evening": true, "Night": true, "Anyway": true,
	"Actually": true, "Maybe": true, "Really": true, "Right": true,
	"First": true, "Second": true, "Third": true, "Next": true, "Last": true,
	"New": true, "Old": true, "Good": true, "Great": true, "Big": true,
	"Little": true, "Long": true, "Short": true, "Small": true, "Large": true,
	"High": true, "Low": true, "Every": true, "Each": true, "Both": true,
	"All": true, "One": true, "Two": true, "Three": true, "Four": true,
	"Five": true, "Six": true, "Seven": true, "Eight": true, "Nine": true,
	"Ten": true, "Eleven": true, "Twelve": true, "Hundred": true, "Thousand": true,
	"Here": true, "How": true, "Why": true, "Who": true, "So": true,
	"But": true, "And": true, "Not": true, "Or": true, "If": true,
	"No": true, "Yes": true, "Well": true, "Now": true, "Still": true,
	"Even": true, "Back": true, "Out": true, "Up": true, "Down": true,
	"Thing": true, "Things": true, "Something": true, "Anything": true,
	"Nothing": true, "Everything": true, "Everyone": true, "Someone": true,
	"Thanks": true, "Okay": true, "Yeah": true, "Sort": true, "Kind": true,
}

func ExtractKeywords(text string) []string {
	seen := make(map[string]bool)
	var keywords []string

	for _, match := range multiCapitalRe.FindAllString(text, -1) {
		phrase := strings.TrimSpace(match)
		if !isStopPhrase(phrase) && !seen[phrase] {
			seen[phrase] = true
			keywords = append(keywords, phrase)
		}
	}

	for _, match := range acronymRe.FindAllString(text, -1) {
		word := strings.TrimSpace(match)
		if !seen[word] {
			seen[word] = true
			keywords = append(keywords, word)
		}
	}

	wordCounts := make(map[string]int)
	for _, match := range capitalWordRe.FindAllString(text, -1) {
		word := strings.TrimSpace(match)
		if !stopWords[word] {
			wordCounts[word]++
		}
	}
	for word, count := range wordCounts {
		if count >= 2 && !seen[word] {
			seen[word] = true
			keywords = append(keywords, word)
		}
	}

	return keywords
}

func isStopPhrase(phrase string) bool {
	words := strings.Fields(phrase)
	for _, w := range words {
		if !stopWords[w] {
			return false
		}
	}
	return true
}
