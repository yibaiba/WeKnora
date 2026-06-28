package service

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"strings"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	sourceACLMetadataAccessMode       = "access_mode"
	sourceACLMetadataRequireACL       = "require_source_acl"
	sourceACLMetadataQueryableState   = "queryable_state"
	sourceACLMetadataRestricted       = "restricted"
	sourceACLMetadataProvider         = "provider"
	sourceACLAuditTargetTypeKnowledge = "knowledge"
)

type sourceACLGuardService struct {
	repo      interfaces.SourceACLRepository
	policy    interfaces.SourceACLPolicyService
	knowledge interfaces.KnowledgeRepository
	audit     interfaces.AuditLogService
}

func NewSourceACLGuardService(
	repo interfaces.SourceACLRepository,
	policy interfaces.SourceACLPolicyService,
	knowledge interfaces.KnowledgeRepository,
	audit interfaces.AuditLogService,
) interfaces.SourceACLGuardService {
	return &sourceACLGuardService{repo: repo, policy: policy, knowledge: knowledge, audit: audit}
}

func (s *sourceACLGuardService) CanRead(
	ctx context.Context,
	request interfaces.SourceACLGuardRequest,
) (*types.SourceACLDecision, error) {
	req := s.normalizeRequest(ctx, request)
	if req.TenantID == 0 || req.KnowledgeID == "" {
		return sourceACLDeny(sourceACLPolicyRequest(req), "", types.SourceACLReasonInvalidRequest, nil), nil
	}
	record, err := s.repo.FindByKnowledgeID(ctx, req.TenantID, req.KnowledgeID)
	if err != nil && !stderrors.Is(err, apprepo.ErrSourceACLNotFound) {
		return nil, err
	}
	hasRecord := err == nil && record != nil
	if !hasRecord && !sourceACLAppliesToKnowledge(req.Knowledge) {
		return sourceACLAllowedByDefault(req), nil
	}
	decision, err := s.policy.CanRead(ctx, sourceACLPolicyRequest(req))
	if err != nil {
		return nil, err
	}
	s.emitAudit(ctx, req, decision)
	return decision, nil
}

func (s *sourceACLGuardService) RequireRead(ctx context.Context, request interfaces.SourceACLGuardRequest) error {
	decision, err := s.CanRead(ctx, request)
	if err != nil {
		return err
	}
	if decision == nil || !decision.Allowed {
		return werrors.NewForbiddenError("source ACL denied")
	}
	return nil
}

func (s *sourceACLGuardService) FilterKnowledges(
	ctx context.Context,
	purpose string,
	knowledges []*types.Knowledge,
) ([]*types.Knowledge, error) {
	if len(knowledges) == 0 {
		return knowledges, nil
	}
	out := make([]*types.Knowledge, 0, len(knowledges))
	for _, knowledge := range knowledges {
		if knowledge == nil {
			continue
		}
		decision, err := s.CanRead(ctx, interfaces.SourceACLGuardRequest{
			Knowledge: knowledge,
			Purpose:   purpose,
		})
		if err != nil {
			return nil, err
		}
		if decision.Allowed {
			out = append(out, knowledge)
		}
	}
	return out, nil
}

func (s *sourceACLGuardService) FilterIndexCandidates(
	ctx context.Context,
	purpose string,
	candidates []*types.IndexWithScore,
) ([]*types.IndexWithScore, error) {
	if len(candidates) == 0 {
		return candidates, nil
	}
	knowledgeMap, err := s.loadCandidateKnowledges(ctx, candidates)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]bool, len(knowledgeMap))
	for id, knowledge := range knowledgeMap {
		decision, err := s.CanRead(ctx, interfaces.SourceACLGuardRequest{
			Knowledge:   knowledge,
			KnowledgeID: id,
			TenantID:    knowledge.TenantID,
			Purpose:     purpose,
		})
		if err != nil {
			return nil, err
		}
		allowed[id] = decision.Allowed
	}
	return filterSourceACLCandidates(candidates, allowed), nil
}

func (s *sourceACLGuardService) loadCandidateKnowledges(
	ctx context.Context,
	candidates []*types.IndexWithScore,
) (map[string]*types.Knowledge, error) {
	ids := sourceACLKnowledgeIDsFromCandidates(candidates)
	knowledges, err := s.knowledge.GetKnowledgeBatchByIDsOnly(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[string]*types.Knowledge, len(knowledges))
	for _, knowledge := range knowledges {
		if knowledge != nil {
			out[knowledge.ID] = knowledge
		}
	}
	return out, nil
}

func (s *sourceACLGuardService) normalizeRequest(
	ctx context.Context,
	request interfaces.SourceACLGuardRequest,
) interfaces.SourceACLGuardRequest {
	if request.Knowledge != nil {
		request.KnowledgeID = sourceACLDefaultString(request.KnowledgeID, request.Knowledge.ID)
		request.TenantID = defaultUint64(request.TenantID, request.Knowledge.TenantID)
	}
	if !request.ServiceKeyCall {
		request.ServiceKeyCall = types.ServiceKeyCallFromContext(ctx)
	}
	if request.ActorUserID == "" && request.ServiceKeyCall {
		request.ActorUserID, _ = types.SourceACLActorUserIDFromContext(ctx)
	}
	if request.ActorUserID == "" && !request.ServiceKeyCall {
		request.ActorUserID, _ = types.UserIDFromContext(ctx)
	}
	if request.Now.IsZero() {
		request.Now = time.Now().UTC()
	}
	return request
}

func sourceACLPolicyRequest(request interfaces.SourceACLGuardRequest) interfaces.SourceACLDecisionRequest {
	return interfaces.SourceACLDecisionRequest{
		TenantID:       request.TenantID,
		KnowledgeID:    request.KnowledgeID,
		ActorUserID:    request.ActorUserID,
		Purpose:        request.Purpose,
		ServiceKeyCall: request.ServiceKeyCall,
		Now:            request.Now,
	}
}

func (s *sourceACLGuardService) emitAudit(
	ctx context.Context,
	request interfaces.SourceACLGuardRequest,
	decision *types.SourceACLDecision,
) {
	if s.audit == nil || decision == nil || decision.Reason == types.SourceACLReasonInvalidRequest {
		return
	}
	action := types.AuditActionSourceACLReadDenied
	outcome := types.AuditOutcomeDenied
	if decision.Allowed {
		action = types.AuditActionSourceACLReadAllowed
		outcome = types.AuditOutcomeSuccess
	}
	if err := s.audit.Log(ctx, &types.AuditLog{
		TenantID:    request.TenantID,
		ActorUserID: auditActor(ctx),
		ActorRole:   string(types.TenantRoleFromContext(ctx)),
		Action:      action,
		TargetType:  sourceACLAuditTargetTypeKnowledge,
		TargetID:    request.KnowledgeID,
		Outcome:     outcome,
		Details:     sourceACLAuditDetails(decision),
	}); err != nil {
		logger.Warnf(ctx, "source ACL audit write failed: %v", err)
	}
}

func sourceACLAuditDetails(decision *types.SourceACLDecision) types.JSON {
	payload := map[string]interface{}{
		"provider":         decision.Provider,
		"purpose":          decision.Purpose,
		"reason":           decision.Reason,
		"policy_actor":     decision.ActorUserID,
		"wecom_userid":     decision.WeComUserID,
		"subject_ids":      decision.SubjectIDs,
		"service_key_call": decision.ServiceKeyCall,
	}
	data, err := jsonMarshal(payload)
	if err != nil {
		return nil
	}
	return types.JSON(data)
}

func sourceACLAllowedByDefault(request interfaces.SourceACLGuardRequest) *types.SourceACLDecision {
	return &types.SourceACLDecision{
		Allowed:        true,
		Reason:         types.SourceACLReasonAllowedPublic,
		KnowledgeID:    request.KnowledgeID,
		ActorUserID:    request.ActorUserID,
		Purpose:        request.Purpose,
		ServiceKeyCall: request.ServiceKeyCall,
	}
}

func sourceACLAppliesToKnowledge(knowledge *types.Knowledge) bool {
	if knowledge == nil {
		return false
	}
	if knowledge.Channel != types.ChannelWeComWeDrive && sourceACLProvider(knowledge) != types.ChannelWeComWeDrive {
		return false
	}
	metadata := knowledge.GetMetadata()
	return strings.EqualFold(metadata[sourceACLMetadataAccessMode], sourceACLMetadataRestricted) ||
		strings.EqualFold(metadata[sourceACLMetadataQueryableState], sourceACLMetadataRestricted) ||
		strings.EqualFold(metadata[sourceACLMetadataRequireACL], "true")
}

func sourceACLProvider(knowledge *types.Knowledge) string {
	if knowledge == nil {
		return ""
	}
	return knowledge.GetMetadata()[sourceACLMetadataProvider]
}

func sourceACLKnowledgeIDsFromCandidates(candidates []*types.IndexWithScore) []string {
	seen := make(map[string]bool, len(candidates))
	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate == nil || candidate.KnowledgeID == "" || seen[candidate.KnowledgeID] {
			continue
		}
		seen[candidate.KnowledgeID] = true
		ids = append(ids, candidate.KnowledgeID)
	}
	return ids
}

func filterSourceACLCandidates(
	candidates []*types.IndexWithScore,
	allowed map[string]bool,
) []*types.IndexWithScore {
	out := make([]*types.IndexWithScore, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate == nil || !allowed[candidate.KnowledgeID] {
			continue
		}
		out = append(out, candidate)
	}
	return out
}

func defaultUint64(value uint64, fallback uint64) uint64 {
	if value != 0 {
		return value
	}
	return fallback
}

func sourceACLDefaultString(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func jsonMarshal(value interface{}) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal source ACL audit details: %w", err)
	}
	return data, nil
}
