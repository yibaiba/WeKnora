package service

import (
	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/types"
)

func normalizeSplitterConfig(cc types.ChunkingConfig, allowZeroOverlap bool) chunker.SplitterConfig {
	return chunker.NormalizeSplitterConfig(chunker.SplitterConfig{
		ChunkSize:        cc.ChunkSize,
		ChunkOverlap:     cc.ChunkOverlap,
		Separators:       cc.Separators,
		Strategy:         cc.Strategy,
		TokenLimit:       cc.TokenLimit,
		Languages:        cc.Languages,
		AllowZeroOverlap: allowZeroOverlap,
	})
}

func buildSplitterConfigFromEffective(eff types.EffectiveProcessConfig) chunker.SplitterConfig {
	return normalizeSplitterConfig(eff.ChunkingConfig, eff.IngestionAdvisorApplied)
}
