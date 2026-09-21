package evalquery

import "strings"

// Merge keeps every structure query from existing. Ordinary queries from
// generated are appended; pass replaceOrdinary to drop existing ordinary
// items first. Generated structure rows are ignored.
func Merge(existing, generated []Query, replaceOrdinary bool) []Query {
	out := make([]Query, 0, len(existing)+len(generated))
	seenQuery := make(map[string]struct{}, len(existing)+len(generated))
	seenMarker := make(map[string]struct{}, len(existing)+len(generated))
	for _, query := range existing {
		if query.Class != ClassStructure && replaceOrdinary {
			continue
		}
		out = append(out, query)
		seenQuery[query.Query] = struct{}{}
		if query.Marker != "" {
			seenMarker[query.Marker] = struct{}{}
		}
	}
	for _, query := range generated {
		if query.Class == ClassStructure {
			continue
		}
		if query.Query == "" || query.Marker == "" {
			continue
		}
		if _, ok := seenQuery[query.Query]; ok {
			continue
		}
		if _, ok := seenMarker[query.Marker]; ok {
			continue
		}
		out = append(out, query)
		seenQuery[query.Query] = struct{}{}
		seenMarker[query.Marker] = struct{}{}
	}
	return out
}

func MissingMarkers(markdown string, queries []Query) []string {
	var missing []string
	seen := make(map[string]struct{})
	for _, query := range queries {
		if query.Marker == "" {
			continue
		}
		if _, ok := seen[query.Marker]; ok {
			continue
		}
		seen[query.Marker] = struct{}{}
		if !strings.Contains(markdown, query.Marker) {
			missing = append(missing, query.Marker)
		}
	}
	return missing
}
