package evalquery

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

const generateMarkdown = `# Notes
The calibration interval remains thirty days for all regional sensors.
The retention schedule keeps audit artifacts for ninety days.
The archive verification job runs every Friday.
Regional owners confirm the checklist after deployment.
The support handoff includes the incident contact matrix.
`

func TestGenerateAcceptsOrdinaryExactExcerpt(t *testing.T) {
	chat := &scriptedChat{responses: []string{
		ordinaryJSON("How long is the calibration interval for regional sensors?", "The calibration interval remains thirty days for all regional sensors."),
	}}
	report, err := Generate(GenerateRequest{
		Markdown: generateMarkdown,
		Count:    1,
		Seed:     7,
		Chat:     chat,
	})
	require.NoError(t, err)
	require.Equal(t, 1, report.Attempts)
	require.Len(t, report.Accepted, 1)
	got := report.Accepted[0]
	require.Equal(t, ClassOrdinary, got.Class)
	require.Equal(t, KindText, got.Kind)
	require.Empty(t, got.RequiredContext)
	require.Equal(t, "The calibration interval remains thirty days for all regional sensors.", got.Excerpt)
	require.Contains(t, generateMarkdown, got.Marker)
	require.Contains(t, chat.prompts[0], "The calibration interval remains thirty days")
	require.NotContains(t, chat.prompts[0], "Already used questions")
}

func TestGenerateRejectsCompoundStructureInvalidAndDuplicate(t *testing.T) {
	chat := &scriptedChat{responses: []string{
		"not json",
		ordinaryJSON("What is retention and archival policy?", "The retention schedule keeps audit artifacts for ninety days."),
		ordinaryJSON("Which authentication case passed?", "| TC-LOGIN | Authentication | Pass |"),
		ordinaryJSON("How long is the calibration interval for regional sensors?", "The calibration interval remains thirty days for all regional sensors."),
		ordinaryJSON("How long is the calibration interval for regional sensors?", "The calibration interval remains thirty days for all regional sensors."),
		ordinaryJSON("When does archive verification run?", "The archive verification job runs every Friday."),
	}}
	report, err := Generate(GenerateRequest{
		Markdown: generateMarkdown + "\n| TC-LOGIN | Authentication | Pass |\n",
		Count:    2,
		Seed:     3,
		Chat:     chat,
	})
	require.NoError(t, err)
	require.Equal(t, 6, report.Attempts)
	require.Equal(t, 1, report.RejectedInvalid)
	require.Equal(t, 1, report.RejectedCompound)
	require.Equal(t, 1, report.RejectedStructure)
	require.Equal(t, 1, report.RejectedDuplicate)
	require.Len(t, report.Accepted, 2)
	require.Contains(t, chat.prompts[4], "Already used questions")
	require.Contains(t, chat.prompts[4], "How long is the calibration interval for regional sensors?")
}

func TestGenerateRejectsPartialInvalidReferences(t *testing.T) {
	chat := &scriptedChat{responses: []string{
		`{"question":"What remains thirty days?","references":["The calibration interval remains thirty days for all regional sensors.","invented sentence"]}`,
		ordinaryJSON("When does archive verification run?", "The archive verification job runs every Friday."),
	}}
	report, err := Generate(GenerateRequest{
		Markdown: generateMarkdown,
		Count:    1,
		Seed:     1,
		Chat:     chat,
	})
	require.NoError(t, err)
	require.Equal(t, 1, report.RejectedInvalid)
	require.Equal(t, "When does archive verification run?", report.Accepted[0].Query)
}

func TestGenerateSkipsCosineWhenEmbedderNil(t *testing.T) {
	chat := &scriptedChat{responses: []string{
		ordinaryJSON("Who confirms the checklist after deployment?", "Regional owners confirm the checklist after deployment."),
	}}
	report, err := Generate(GenerateRequest{
		Markdown: generateMarkdown,
		Count:    1,
		Chat:     chat,
	})
	require.NoError(t, err)
	require.Len(t, report.Accepted, 1)
}

func TestGenerateCosineFiltersDuplicateAndIrrelevant(t *testing.T) {
	question := "How long is the calibration interval for regional sensors?"
	excerpt := "The calibration interval remains thirty days for all regional sensors."
	other := "When does archive verification run?"
	otherExcerpt := "The archive verification job runs every Friday."
	unrelated := "Who publishes the support handoff?"
	unrelatedExcerpt := "The support handoff includes the incident contact matrix."
	embed := &scriptedEmbed{vectors: map[string][]float32{
		question:         {1, 0},
		excerpt:          {1, 0},
		"near duplicate": {0.99, 0.01},
		other:            {0, 1},
		otherExcerpt:     {0, 1},
		unrelated:        {1, 0},
		unrelatedExcerpt: {0, 1},
	}}
	chat := &scriptedChat{responses: []string{
		ordinaryJSON(question, excerpt),
		ordinaryJSON("near duplicate", excerpt),
		ordinaryJSON(unrelated, unrelatedExcerpt),
		ordinaryJSON(other, otherExcerpt),
	}}
	report, err := Generate(GenerateRequest{
		Markdown: generateMarkdown,
		Count:    2,
		Chat:     chat,
		Embed:    embed,
	})
	require.NoError(t, err)
	require.Equal(t, 1, report.RejectedDuplicate)
	require.Equal(t, 1, report.RejectedIrrelevant)
	require.Equal(t, []string{question, other}, []string{report.Accepted[0].Query, report.Accepted[1].Query})
}

func TestGenerateHonorsMaxAttempts(t *testing.T) {
	chat := &scriptedChat{responses: []string{"not json", "not json", "not json"}}
	report, err := Generate(GenerateRequest{
		Markdown:    generateMarkdown,
		Count:       3,
		MaxAttempts: 2,
		Chat:        chat,
	})
	require.NoError(t, err)
	require.Empty(t, report.Accepted)
	require.Equal(t, 2, report.Attempts)
	require.Len(t, chat.prompts, 2)
}

func TestGenerateStopsAtAttemptBudget(t *testing.T) {
	chat := &scriptedChat{}
	for range defaultAttemptFactor {
		chat.responses = append(chat.responses, "not json")
	}
	report, err := Generate(GenerateRequest{
		Markdown: generateMarkdown,
		Count:    1,
		Chat:     chat,
	})
	require.NoError(t, err)
	require.Empty(t, report.Accepted)
	require.Equal(t, defaultAttemptFactor, report.Attempts)
	require.Equal(t, defaultAttemptFactor, report.RejectedInvalid)
}

func TestGenerateRequiresChatAndMarkdown(t *testing.T) {
	_, err := Generate(GenerateRequest{Markdown: generateMarkdown, Count: 1})
	require.Error(t, err)
	_, err = Generate(GenerateRequest{Count: 1, Chat: &scriptedChat{}})
	require.Error(t, err)
	_, err = Generate(GenerateRequest{Markdown: generateMarkdown, Chat: &scriptedChat{}})
	require.Error(t, err)
}

type scriptedChat struct {
	responses []string
	prompts   []string
}

func (s *scriptedChat) Complete(prompt string) (string, error) {
	s.prompts = append(s.prompts, prompt)
	index := len(s.prompts) - 1
	if index >= len(s.responses) {
		return "", fmt.Errorf("unexpected chat call %d", index+1)
	}
	return s.responses[index], nil
}

type scriptedEmbed struct {
	vectors map[string][]float32
}

func (s *scriptedEmbed) Embed(texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, text := range texts {
		vec, ok := s.vectors[text]
		if !ok {
			return nil, fmt.Errorf("missing vector for %q", text)
		}
		out[i] = vec
	}
	return out, nil
}

func ordinaryJSON(question, excerpt string) string {
	return fmt.Sprintf(`{"oath":"I will not use the word 'and' in the question unless it is part of a proper noun. I will also make sure the question is concise.","question":%q,"references":[%q]}`, question, excerpt)
}
