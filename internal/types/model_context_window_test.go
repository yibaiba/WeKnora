package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelParametersContextWindowTokens(t *testing.T) {
	tests := []struct {
		name      string
		value     int
		effective int
		valid     bool
	}{
		{name: "default", value: 0, effective: DefaultModelContextWindowTokens, valid: true},
		{name: "minimum", value: MinModelContextWindowTokens, effective: MinModelContextWindowTokens, valid: true},
		{name: "maximum", value: MaxModelContextWindowTokens, effective: MaxModelContextWindowTokens, valid: true},
		{name: "below minimum", value: MinModelContextWindowTokens - 1, valid: false},
		{name: "above maximum", value: MaxModelContextWindowTokens + 1, valid: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			params := ModelParameters{ContextWindowTokens: test.value}
			err := params.ValidateContextWindowTokens()
			if !test.valid {
				require.ErrorContains(t, err, "context_window_tokens")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.effective, params.EffectiveContextWindowTokens())
		})
	}
}
