// Package evalquery generates ordinary retrieval-eval questions offline
// using Chroma-style extract-and-filter. Structure queries stay curated.
package evalquery

const (
	ClassStructure = "structure"
	ClassOrdinary  = "ordinary"
	KindText       = "text"

	defaultWindowChars      = 4000
	defaultExcerptMinCosine = 0.40
	defaultDuplicateMaxCos  = 0.70
	defaultMaxReferences    = 5
	defaultAttemptFactor    = 10
	maxPreviousQuestions    = 50
)

// Fixture is the committed retrieval eval JSON.
type Fixture struct {
	ChunkSize    int     `json:"chunk_size"`
	ChunkOverlap int     `json:"chunk_overlap"`
	Markdown     string  `json:"markdown"`
	Queries      []Query `json:"queries"`
}

type Query struct {
	Class           string `json:"class"`
	Kind            string `json:"kind"`
	Query           string `json:"query"`
	Marker          string `json:"marker"`
	RequiredContext string `json:"required_context"`
	Excerpt         string `json:"excerpt"`
}

type GenerateRequest struct {
	Markdown           string
	Existing           []Query
	Count              int
	WindowChars        int
	ExcerptMinCosine   float64
	DuplicateMaxCosine float64
	MaxAttempts        int
	Seed               int64
	Chat               Chat
	Embed              Embedder
}

type GenerateReport struct {
	Accepted           []Query
	Attempts           int
	RejectedInvalid    int
	RejectedCompound   int
	RejectedStructure  int
	RejectedDuplicate  int
	RejectedIrrelevant int
	RejectedMarker     int
}

type Chat interface {
	Complete(prompt string) (string, error)
}

type Embedder interface {
	Embed(texts []string) ([][]float32, error)
}

type modelResponse struct {
	Oath       string   `json:"oath"`
	Question   string   `json:"question"`
	References []string `json:"references"`
}
