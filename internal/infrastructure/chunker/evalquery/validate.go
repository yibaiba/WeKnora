package evalquery

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

func parseModelResponse(raw string) (modelResponse, error) {
	payload := extractJSONObject(raw)
	if payload == "" {
		return modelResponse{}, fmt.Errorf("response is not JSON")
	}
	var parsed modelResponse
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		return modelResponse{}, fmt.Errorf("decode question JSON: %w", err)
	}
	parsed.Question = strings.TrimSpace(parsed.Question)
	refs := make([]string, 0, len(parsed.References))
	for _, ref := range parsed.References {
		ref = strings.TrimSpace(ref)
		if ref != "" {
			refs = append(refs, ref)
		}
	}
	parsed.References = refs
	return parsed, nil
}

func extractJSONObject(raw string) string {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSuffix(trimmed, "```")
	trimmed = strings.TrimSpace(trimmed)
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start < 0 || end <= start {
		return ""
	}
	return trimmed[start : end+1]
}

// firstExactExcerpt returns the first reference when every reference is an
// exact contiguous substring of haystack. A single invalid excerpt rejects
// the whole candidate; non-contiguous references are never joined.
func firstExactExcerpt(haystack string, references []string) (string, error) {
	if len(references) == 0 {
		return "", fmt.Errorf("no references")
	}
	if len(references) > defaultMaxReferences {
		return "", fmt.Errorf("too many references")
	}
	for _, ref := range references {
		if !strings.Contains(haystack, ref) {
			return "", fmt.Errorf("reference is not an exact substring")
		}
	}
	return references[0], nil
}

func isCompoundQuestion(question string) bool {
	lower := strings.ToLower(question)
	if strings.Contains(lower, " and ") {
		return true
	}
	for _, needle := range []string{"以及", "还有", "并且"} {
		if strings.Contains(question, needle) {
			return true
		}
	}
	return false
}

func isStructureExcerpt(excerpt string) bool {
	trimmed := strings.TrimSpace(excerpt)
	if strings.Contains(excerpt, "```") {
		return true
	}
	if strings.HasPrefix(trimmed, "|") || strings.Contains(excerpt, "\n|") {
		return true
	}
	if strings.HasPrefix(trimmed, "Q:") || strings.HasPrefix(trimmed, "A:") {
		return true
	}
	if strings.HasPrefix(trimmed, "Record ID:") {
		return true
	}
	return false
}

func anyStructureReference(references []string) bool {
	for _, ref := range references {
		if isStructureExcerpt(ref) {
			return true
		}
	}
	return false
}

func uniqueMarker(excerpt, markdown string, used map[string]struct{}) (string, error) {
	excerpt = strings.TrimSpace(excerpt)
	if excerpt == "" {
		return "", fmt.Errorf("empty excerpt")
	}
	runes := []rune(excerpt)
	minLen := 8
	if len(runes) < minLen {
		minLen = len(runes)
	}
	maxLen := min(40, len(runes))
	for size := maxLen; size >= minLen; size-- {
		for start := 0; start+size <= len(runes); start++ {
			candidate := strings.TrimSpace(string(runes[start : start+size]))
			if !usableMarker(candidate, minLen) {
				continue
			}
			if _, exists := used[candidate]; exists {
				continue
			}
			if strings.Count(markdown, candidate) == 1 {
				return candidate, nil
			}
		}
	}
	if usableMarker(excerpt, minLen) && strings.Count(markdown, excerpt) == 1 {
		if _, exists := used[excerpt]; !exists {
			return excerpt, nil
		}
	}
	return "", fmt.Errorf("no unique marker in excerpt")
}

func usableMarker(candidate string, minLen int) bool {
	if strings.ContainsAny(candidate, "\n\r") {
		return false
	}
	if utf8.RuneCountInString(candidate) < minLen {
		return false
	}
	letters := 0
	for _, r := range candidate {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			letters++
		}
	}
	return letters >= min(4, minLen)
}
