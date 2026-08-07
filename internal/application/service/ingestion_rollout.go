package service

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strconv"

	appconfig "github.com/Tencent/WeKnora/internal/config"
)

const ingestionShadowSampleBuckets = 10_000

type ingestionRolloutDecision struct {
	mode    string
	runV2   bool
	applyV2 bool
}

func resolveIngestionRollout(
	config *appconfig.Config,
	tenantID uint64,
) (ingestionRolloutDecision, error) {
	rollout, err := appconfig.SemanticChunkingV2Rollout(config)
	if err != nil {
		return ingestionRolloutDecision{}, err
	}
	decision := ingestionRolloutDecision{mode: rollout.Mode}
	switch rollout.Mode {
	case appconfig.SemanticChunkingV2ModeOff:
		return decision, nil
	case appconfig.SemanticChunkingV2ModeOn:
		decision.runV2 = true
		decision.applyV2 = true
		return decision, nil
	case appconfig.SemanticChunkingV2ModeShadow:
		decision.runV2 = ingestionTenantInShadowSample(tenantID, rollout.ShadowSampleRate)
		return decision, nil
	default:
		return ingestionRolloutDecision{}, fmt.Errorf("unsupported semantic chunking v2 mode %q", rollout.Mode)
	}
}

func ingestionTenantInShadowSample(tenantID uint64, sampleRate float64) bool {
	if sampleRate <= 0 {
		return false
	}
	if sampleRate >= 1 {
		return true
	}
	digest := sha256.Sum256([]byte(strconv.FormatUint(tenantID, 10)))
	bucket := binary.BigEndian.Uint64(digest[:8]) % ingestionShadowSampleBuckets
	threshold := uint64(sampleRate * ingestionShadowSampleBuckets)
	return bucket < threshold
}
