package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIngestionAdvisorTimeoutUsesDefaultForMissingOrInvalidConfig(t *testing.T) {
	require.Equal(t, DefaultIngestionAdvisorTimeout, IngestionAdvisorTimeout(nil))
	require.Equal(t, DefaultIngestionAdvisorTimeout, IngestionAdvisorTimeout(&Config{}))
	require.Equal(t, DefaultIngestionAdvisorTimeout, IngestionAdvisorTimeout(&Config{
		KnowledgeBase: &KnowledgeBaseConfig{IngestionAdvisorTimeout: -time.Second},
	}))
}

func TestIngestionAdvisorTimeoutUsesConfiguredValue(t *testing.T) {
	cfg := &Config{KnowledgeBase: &KnowledgeBaseConfig{IngestionAdvisorTimeout: 12 * time.Minute}}
	require.Equal(t, 12*time.Minute, IngestionAdvisorTimeout(cfg))
}
