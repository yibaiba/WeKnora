package evalquery

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMergeKeepsStructureAndAppendsOrdinary(t *testing.T) {
	existing := []Query{
		{Class: ClassStructure, Kind: "table", Query: "Authentication Result", Marker: "TC-LOGIN"},
		{Class: ClassOrdinary, Kind: KindText, Query: "old ordinary", Marker: "calibration interval"},
	}
	generated := []Query{
		{Class: ClassStructure, Kind: "faq", Query: "should drop", Marker: "FAQ-ROLLBACK"},
		{Class: ClassOrdinary, Kind: KindText, Query: "new ordinary", Marker: "retention schedule"},
		{Class: ClassOrdinary, Kind: KindText, Query: "old ordinary", Marker: "other marker"},
	}
	got := Merge(existing, generated, false)
	require.Equal(t, []Query{
		existing[0],
		existing[1],
		{Class: ClassOrdinary, Kind: KindText, Query: "new ordinary", Marker: "retention schedule"},
	}, got)
}

func TestMergeReplaceOrdinaryDropsExistingOrdinary(t *testing.T) {
	existing := []Query{
		{Class: ClassStructure, Kind: "table", Query: "Authentication Result", Marker: "TC-LOGIN"},
		{Class: ClassOrdinary, Kind: KindText, Query: "old ordinary", Marker: "calibration interval"},
	}
	generated := []Query{
		{Class: ClassOrdinary, Kind: KindText, Query: "new ordinary", Marker: "retention schedule"},
	}
	got := Merge(existing, generated, true)
	require.Equal(t, []Query{existing[0], generated[0]}, got)
}

func TestMissingMarkers(t *testing.T) {
	markdown := "The calibration interval remains thirty days."
	missing := MissingMarkers(markdown, []Query{
		{Marker: "calibration interval"},
		{Marker: "TC-LOGIN"},
		{Marker: "TC-LOGIN"},
	})
	require.Equal(t, []string{"TC-LOGIN"}, missing)
}

func TestLoadAndMarshalFixtureRoundTrip(t *testing.T) {
	path := filepath.Join("..", "testdata", "semantic_retrieval_eval.json")
	fixture, err := LoadFixture(path)
	require.NoError(t, err)
	require.NotEmpty(t, fixture.Markdown)
	require.NotEmpty(t, fixture.Queries)
	raw, err := MarshalFixture(fixture)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"class": "structure"`)
}

func TestCosineSimilarity(t *testing.T) {
	sim, err := cosineSimilarity([]float32{1, 0}, []float32{1, 0})
	require.NoError(t, err)
	require.InDelta(t, 1.0, sim, 1e-6)
	sim, err = cosineSimilarity([]float32{1, 0}, []float32{0, 1})
	require.NoError(t, err)
	require.InDelta(t, 0.0, sim, 1e-6)
	_, err = cosineSimilarity([]float32{1}, []float32{1, 0})
	require.Error(t, err)
}
