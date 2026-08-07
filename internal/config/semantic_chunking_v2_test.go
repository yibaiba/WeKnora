package config

import (
	"math"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestSemanticChunkingV2RolloutDefaultsToTenPercentShadow(t *testing.T) {
	for _, cfg := range []*Config{nil, {}, {KnowledgeBase: &KnowledgeBaseConfig{}}} {
		rollout, err := SemanticChunkingV2Rollout(cfg)
		require.NoError(t, err)
		require.Equal(t, SemanticChunkingV2ModeShadow, rollout.Mode)
		require.Equal(t, 0.10, rollout.ShadowSampleRate)
	}
}

func TestSemanticChunkingV2RolloutPreservesExplicitValues(t *testing.T) {
	zero := 0.0
	rollout, err := SemanticChunkingV2Rollout(&Config{KnowledgeBase: &KnowledgeBaseConfig{
		SemanticChunkingV2Mode: SemanticChunkingV2ModeOff,
		ShadowSampleRate:       &zero,
	}})

	require.NoError(t, err)
	require.Equal(t, SemanticChunkingV2ModeOff, rollout.Mode)
	require.Zero(t, rollout.ShadowSampleRate)
}

func TestSemanticChunkingV2RolloutRejectsInvalidConfiguration(t *testing.T) {
	invalidRates := []float64{-0.01, 1.01, math.NaN(), math.Inf(1)}
	for _, rate := range invalidRates {
		_, err := SemanticChunkingV2Rollout(&Config{KnowledgeBase: &KnowledgeBaseConfig{
			SemanticChunkingV2Mode: SemanticChunkingV2ModeShadow,
			ShadowSampleRate:       &rate,
		}})
		require.ErrorContains(t, err, "shadow_sample_rate")
	}

	_, err := SemanticChunkingV2Rollout(&Config{KnowledgeBase: &KnowledgeBaseConfig{
		SemanticChunkingV2Mode: "gradual",
	}})
	require.ErrorContains(t, err, "semantic_chunking_v2_mode")
}

func TestValidateConfigMaterializesSemanticChunkingDefaults(t *testing.T) {
	cfg := &Config{KnowledgeBase: &KnowledgeBaseConfig{ChunkSize: 512, ChunkOverlap: 50}}

	require.NoError(t, ValidateConfig(cfg))
	require.Equal(t, SemanticChunkingV2ModeShadow, cfg.KnowledgeBase.SemanticChunkingV2Mode)
	require.NotNil(t, cfg.KnowledgeBase.ShadowSampleRate)
	require.Equal(t, 0.10, *cfg.KnowledgeBase.ShadowSampleRate)
}

func TestProductionConfigStartsInTenPercentShadowMode(t *testing.T) {
	data, err := os.ReadFile("../../config/config.yaml")
	require.NoError(t, err)
	var document struct {
		KnowledgeBase KnowledgeBaseConfig `yaml:"knowledge_base"`
	}
	require.NoError(t, yaml.Unmarshal(data, &document))

	rollout, err := SemanticChunkingV2Rollout(&Config{KnowledgeBase: &document.KnowledgeBase})
	require.NoError(t, err)
	require.Equal(t, SemanticChunkingV2ModeShadow, rollout.Mode)
	require.Equal(t, 0.10, rollout.ShadowSampleRate)
}
