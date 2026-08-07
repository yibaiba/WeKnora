package chunker

import (
	"strings"
	"unicode/utf8"
)

const maximumRepeatedPageRegionRunes = 160

type semanticPage struct {
	first int
	last  int
}

func detectRepeatedPageRegions(lines []semanticLine) map[int]struct{} {
	pages := semanticPages(lines)
	if len(pages) < 2 {
		return nil
	}
	candidates := make(map[string][]int)
	for _, page := range pages {
		appendPageRegionCandidate(candidates, lines, page.first)
		if page.last != page.first {
			appendPageRegionCandidate(candidates, lines, page.last)
		}
	}
	result := make(map[int]struct{})
	for _, indexes := range candidates {
		if len(indexes) < 2 {
			continue
		}
		for _, index := range indexes {
			result[index] = struct{}{}
		}
	}
	return result
}

func semanticPages(lines []semanticLine) []semanticPage {
	pages := make([]semanticPage, 0)
	pageStart := 0
	for index, line := range lines {
		if !strings.Contains(line.text, "\f") {
			continue
		}
		if page, ok := semanticPageBounds(lines, pageStart, index); ok {
			pages = append(pages, page)
		}
		pageStart = index
	}
	if page, ok := semanticPageBounds(lines, pageStart, len(lines)); ok {
		pages = append(pages, page)
	}
	return pages
}

func semanticPageBounds(lines []semanticLine, start, end int) (semanticPage, bool) {
	first := -1
	last := -1
	for index := start; index < end; index++ {
		if strings.TrimSpace(strings.ReplaceAll(lines[index].text, "\f", "")) == "" {
			continue
		}
		if first < 0 {
			first = index
		}
		last = index
	}
	return semanticPage{first: first, last: last}, first >= 0
}

func appendPageRegionCandidate(candidates map[string][]int, lines []semanticLine, index int) {
	value := strings.TrimSpace(strings.ReplaceAll(lines[index].text, "\f", ""))
	if value == "" || utf8.RuneCountInString(value) > maximumRepeatedPageRegionRunes {
		return
	}
	key := strings.Join(strings.Fields(value), " ")
	candidates[key] = append(candidates[key], index)
}
