package tools

import "context"

type redactTaskToolPayloadsKey struct{}

// WithRedactedToolPayloads marks a non-chat task whose tool arguments and
// outputs must stay inside the model context rather than observability/UI data.
func WithRedactedToolPayloads(ctx context.Context) context.Context {
	return context.WithValue(ctx, redactTaskToolPayloadsKey{}, true)
}

// ToolPayloadsRedacted reports whether task tool payloads must be omitted.
func ToolPayloadsRedacted(ctx context.Context) bool {
	redacted, _ := ctx.Value(redactTaskToolPayloadsKey{}).(bool)
	return redacted
}
