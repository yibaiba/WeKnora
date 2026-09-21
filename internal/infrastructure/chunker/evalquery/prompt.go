package evalquery

import (
	"strings"
)

const ordinaryQueryPrompt = `You are an agent that generates questions from provided text.
Your job is to generate a question and provide the relevant
sections from the text as references.

Instructions:
1. For each provided text, generate a question that can be
   answered solely by the facts in the text.
2. Extract all significant facts that answer the generated
   question.
3. Format the response in JSON with these fields:
   - "oath": "I will not use the word 'and' in the question unless it is part of a proper noun. I will also make sure the question is concise."
   - "question": A question directly related to these facts,
     ensuring it can only be answered using the references
     provided.
   - "references": A list of all text sections that answer
     the generated question. These must be exact copies from
     the original text and should be whole sentences where
     possible.

Notes:
Make the question more specific.
Do not ask a question about multiple topics.
Do not ask a question with over 5 references.
Do not ask about tables, FAQ pairs, code fences, or record IDs.
Ask about ordinary prose facts only.

DO NOT USE THE WORD 'and' IN THE QUESTION UNLESS IT IS PART OF
A PROPER NOUN.
YOU MUST INCLUDE THE OATH ABOVE IN YOUR RESPONSE.
YOU MUST ALSO NOT REPEAT A QUESTION THAT HAS ALREADY BEEN
USED.`

func buildOrdinaryPrompt(window string, previous []string) string {
	var b strings.Builder
	b.WriteString(ordinaryQueryPrompt)
	b.WriteString("\n\nText:\n")
	b.WriteString(window)
	if len(previous) == 0 {
		return b.String()
	}
	b.WriteString("\n\nAlready used questions:\n")
	limit := min(len(previous), maxPreviousQuestions)
	start := len(previous) - limit
	for _, question := range previous[start:] {
		b.WriteString("- ")
		b.WriteString(question)
		b.WriteByte('\n')
	}
	return b.String()
}

func sampleWindow(markdown string, windowChars int, offset int) string {
	if windowChars <= 0 {
		windowChars = defaultWindowChars
	}
	if markdown == "" {
		return ""
	}
	runes := []rune(markdown)
	if len(runes) <= windowChars {
		return markdown
	}
	if offset < 0 {
		offset = 0
	}
	if offset > len(runes)-windowChars {
		offset = len(runes) - windowChars
	}
	return string(runes[offset : offset+windowChars])
}
