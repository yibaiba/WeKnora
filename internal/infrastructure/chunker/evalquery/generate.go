package evalquery

import (
	"fmt"
	"math/rand"
	"strings"
	"unicode/utf8"
)

func Generate(req GenerateRequest) (GenerateReport, error) {
	if req.Chat == nil {
		return GenerateReport{}, fmt.Errorf("chat is required")
	}
	markdown := req.Markdown
	if strings.TrimSpace(markdown) == "" {
		return GenerateReport{}, fmt.Errorf("markdown is empty")
	}
	if req.Count <= 0 {
		return GenerateReport{}, fmt.Errorf("count must be positive")
	}
	windowChars := req.WindowChars
	if windowChars <= 0 {
		windowChars = defaultWindowChars
	}
	excerptMin := req.ExcerptMinCosine
	if excerptMin <= 0 {
		excerptMin = defaultExcerptMinCosine
	}
	dupMax := req.DuplicateMaxCosine
	if dupMax <= 0 {
		dupMax = defaultDuplicateMaxCos
	}
	seed := req.Seed
	if seed == 0 {
		seed = 1
	}
	rng := rand.New(rand.NewSource(seed))

	usedMarkers := make(map[string]struct{})
	seenQuestions := make(map[string]struct{})
	previous := make([]string, 0, len(req.Existing))
	for _, query := range req.Existing {
		if query.Marker != "" {
			usedMarkers[query.Marker] = struct{}{}
		}
		if query.Class == ClassStructure {
			continue
		}
		question := strings.TrimSpace(query.Query)
		if question == "" {
			continue
		}
		previous = append(previous, question)
		seenQuestions[question] = struct{}{}
	}

	var cache *vectorCache
	if req.Embed != nil {
		cache = newVectorCache(req.Embed)
		if _, err := cache.getMany(previous); err != nil {
			return GenerateReport{}, fmt.Errorf("embed existing questions: %w", err)
		}
	}

	report := GenerateReport{}
	maxAttempts := req.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = req.Count * defaultAttemptFactor
	}
	runeCount := utf8.RuneCountInString(markdown)
	for report.Attempts < maxAttempts && len(report.Accepted) < req.Count {
		report.Attempts++
		offset := 0
		if runeCount > windowChars {
			offset = rng.Intn(runeCount - windowChars + 1)
		}
		window := sampleWindow(markdown, windowChars, offset)
		raw, err := req.Chat.Complete(buildOrdinaryPrompt(window, previous))
		if err != nil {
			return report, fmt.Errorf("chat complete: %w", err)
		}
		accepted, reason, err := acceptOrdinary(acceptOrdinaryRequest{
			markdown:     markdown,
			window:       window,
			raw:          raw,
			seen:         seenQuestions,
			usedMarkers:  usedMarkers,
			previous:     previous,
			cache:        cache,
			excerptMin:   excerptMin,
			duplicateMax: dupMax,
		})
		if err != nil {
			return report, err
		}
		switch reason {
		case rejectNone:
			report.Accepted = append(report.Accepted, accepted)
			usedMarkers[accepted.Marker] = struct{}{}
			seenQuestions[accepted.Query] = struct{}{}
			previous = append(previous, accepted.Query)
		case rejectInvalid:
			report.RejectedInvalid++
		case rejectCompound:
			report.RejectedCompound++
		case rejectStructure:
			report.RejectedStructure++
		case rejectDuplicate:
			report.RejectedDuplicate++
		case rejectIrrelevant:
			report.RejectedIrrelevant++
		case rejectMarker:
			report.RejectedMarker++
		}
	}
	return report, nil
}

type rejectReason int

const (
	rejectNone rejectReason = iota
	rejectInvalid
	rejectCompound
	rejectStructure
	rejectDuplicate
	rejectIrrelevant
	rejectMarker
)

type acceptOrdinaryRequest struct {
	markdown     string
	window       string
	raw          string
	seen         map[string]struct{}
	usedMarkers  map[string]struct{}
	previous     []string
	cache        *vectorCache
	excerptMin   float64
	duplicateMax float64
}

func acceptOrdinary(req acceptOrdinaryRequest) (Query, rejectReason, error) {
	parsed, err := parseModelResponse(req.raw)
	if err != nil || parsed.Question == "" {
		return Query{}, rejectInvalid, nil
	}
	if isCompoundQuestion(parsed.Question) {
		return Query{}, rejectCompound, nil
	}
	if _, exists := req.seen[parsed.Question]; exists {
		return Query{}, rejectDuplicate, nil
	}
	excerpt, err := firstExactExcerpt(req.markdown, parsed.References)
	if err != nil {
		return Query{}, rejectInvalid, nil
	}
	if _, err := firstExactExcerpt(req.window, parsed.References); err != nil {
		return Query{}, rejectInvalid, nil
	}
	if anyStructureReference(parsed.References) {
		return Query{}, rejectStructure, nil
	}
	if req.cache != nil {
		duplicate, irrelevant, err := cosineFilters(req.cache, parsed.Question, excerpt, req.previous, req.excerptMin, req.duplicateMax)
		if err != nil {
			return Query{}, rejectNone, err
		}
		if duplicate {
			return Query{}, rejectDuplicate, nil
		}
		if irrelevant {
			return Query{}, rejectIrrelevant, nil
		}
	}
	marker, err := uniqueMarker(excerpt, req.markdown, req.usedMarkers)
	if err != nil {
		return Query{}, rejectMarker, nil
	}
	return Query{
		Class:           ClassOrdinary,
		Kind:            KindText,
		Query:           parsed.Question,
		Marker:          marker,
		RequiredContext: "",
		Excerpt:         excerpt,
	}, rejectNone, nil
}

func cosineFilters(
	cache *vectorCache,
	question, excerpt string,
	previous []string,
	excerptMin, duplicateMax float64,
) (duplicate, irrelevant bool, err error) {
	texts := []string{question, excerpt}
	texts = append(texts, previous...)
	vectors, err := cache.getMany(texts)
	if err != nil {
		return false, false, fmt.Errorf("embed question: %w", err)
	}
	questionVec := vectors[0]
	excerptVec := vectors[1]
	relevance, err := cosineSimilarity(questionVec, excerptVec)
	if err != nil {
		return false, false, err
	}
	if relevance < excerptMin {
		return false, true, nil
	}
	for _, previousVec := range vectors[2:] {
		similarity, err := cosineSimilarity(questionVec, previousVec)
		if err != nil {
			return false, false, err
		}
		if similarity >= duplicateMax {
			return true, false, nil
		}
	}
	return false, false, nil
}
