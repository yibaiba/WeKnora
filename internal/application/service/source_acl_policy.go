package service

import (
	"context"
	"errors"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type sourceACLPolicyService struct {
	repo     interfaces.SourceACLRepository
	identity interfaces.WeComIdentityService
}

func NewSourceACLPolicyService(
	repo interfaces.SourceACLRepository,
	identity interfaces.WeComIdentityService,
) interfaces.SourceACLPolicyService {
	return &sourceACLPolicyService{repo: repo, identity: identity}
}

func (s *sourceACLPolicyService) CanRead(
	ctx context.Context,
	request interfaces.SourceACLDecisionRequest,
) (*types.SourceACLDecision, error) {
	if request.TenantID == 0 || request.KnowledgeID == "" {
		return sourceACLDeny(request, "", types.SourceACLReasonInvalidRequest, nil), nil
	}
	if request.ServiceKeyCall && request.ActorUserID == "" {
		return sourceACLDeny(request, "", types.SourceACLReasonServiceKeyActorMissing, nil), nil
	}

	record, err := s.repo.FindByKnowledgeID(ctx, request.TenantID, request.KnowledgeID)
	if errors.Is(err, apprepo.ErrSourceACLNotFound) {
		return sourceACLDeny(request, "", types.SourceACLReasonSnapshotMissing, nil), nil
	}
	if err != nil {
		return nil, err
	}
	decision := evaluateSnapshotState(request, record)
	if decision != nil {
		return decision, nil
	}

	var subject *types.WeComACLSubject
	if request.ServiceKeyCall {
		subject, err = s.resolveBoundSubject(ctx, request)
		if err != nil {
			return nil, err
		}
		if subject == nil {
			return sourceACLDenyRecord(request, record, types.SourceACLReasonActorUnbound, nil), nil
		}
	}
	if sourceACLHasPublicEntry(record) {
		return sourceACLAllow(request, record, types.SourceACLReasonAllowedPublic, subject), nil
	}
	if record.Snapshot.Visibility == types.SourceACLVisibilityPublic {
		return sourceACLAllow(request, record, types.SourceACLReasonAllowedPublic, subject), nil
	}

	if subject == nil {
		subject, err = s.resolveBoundSubject(ctx, request)
		if err != nil {
			return nil, err
		}
	}
	if subject == nil {
		return sourceACLDenyRecord(request, record, types.SourceACLReasonActorUnbound, subject), nil
	}
	if sourceACLHasAllCompanyAccess(record) {
		return sourceACLAllow(request, record, types.SourceACLReasonAllowedAllCompany, subject), nil
	}
	return sourceACLEvaluateRestricted(request, record, subject), nil
}

func (s *sourceACLPolicyService) resolveBoundSubject(
	ctx context.Context,
	request interfaces.SourceACLDecisionRequest,
) (*types.WeComACLSubject, error) {
	subject, err := s.identity.ResolveSubject(ctx, request.TenantID, request.ActorUserID)
	if err != nil {
		return nil, err
	}
	if subject == nil || !subject.Bound || !types.IsWeComIdentityUsable(subject.Status) {
		return nil, nil
	}
	return subject, nil
}

func evaluateSnapshotState(
	request interfaces.SourceACLDecisionRequest,
	record *types.SourceACLRecord,
) *types.SourceACLDecision {
	if record == nil || record.Snapshot == nil {
		return sourceACLDeny(request, "", types.SourceACLReasonSnapshotMissing, nil)
	}
	switch record.Snapshot.Status {
	case types.SourceACLStatusReady, "":
	case types.SourceACLStatusInvalid:
		return sourceACLDenyRecord(request, record, types.SourceACLReasonSnapshotInvalid, nil)
	case types.SourceACLStatusStale:
		return sourceACLDenyRecord(request, record, types.SourceACLReasonSnapshotStale, nil)
	case types.SourceACLStatusUnmapped:
		return sourceACLDenyRecord(request, record, types.SourceACLReasonSnapshotUnmapped, nil)
	default:
		return sourceACLDenyRecord(request, record, types.SourceACLReasonSnapshotInvalid, nil)
	}
	if isSourceACLSnapshotExpired(request, record.Snapshot) {
		return sourceACLDenyRecord(request, record, types.SourceACLReasonSnapshotStale, nil)
	}
	return nil
}

func sourceACLEvaluateRestricted(
	request interfaces.SourceACLDecisionRequest,
	record *types.SourceACLRecord,
	subject *types.WeComACLSubject,
) *types.SourceACLDecision {
	for _, entry := range record.Entries {
		if entry == nil || entry.Permission != types.SourceACLPermissionRead {
			continue
		}
		switch entry.SubjectType {
		case types.SourceACLSubjectWeComUser:
			if entry.SubjectID == subject.WeComUserID {
				return sourceACLAllow(request, record, types.SourceACLReasonAllowedDirectUser, subject)
			}
		case types.SourceACLSubjectWeComDepartment:
			if sourceACLContainsString(subject.Departments, entry.SubjectID) {
				return sourceACLAllow(request, record, types.SourceACLReasonAllowedDepartment, subject)
			}
		case types.SourceACLSubjectWeComGroup:
			if sourceACLContainsString(subject.Groups, entry.SubjectID) {
				return sourceACLAllow(request, record, types.SourceACLReasonAllowedGroup, subject)
			}
		}
	}
	return sourceACLDenyRecord(request, record, types.SourceACLReasonNoMatchingSubject, subject)
}

func sourceACLHasPublicEntry(record *types.SourceACLRecord) bool {
	for _, entry := range record.Entries {
		if entry != nil &&
			entry.Permission == types.SourceACLPermissionRead &&
			entry.SubjectType == types.SourceACLSubjectPublic {
			return true
		}
	}
	return false
}

func sourceACLHasAllCompanyAccess(record *types.SourceACLRecord) bool {
	if record.Snapshot.Visibility == types.SourceACLVisibilityAllCompany {
		return true
	}
	for _, entry := range record.Entries {
		if entry != nil &&
			entry.Permission == types.SourceACLPermissionRead &&
			entry.SubjectType == types.SourceACLSubjectAllCompany {
			return true
		}
	}
	return false
}

func isSourceACLSnapshotExpired(request interfaces.SourceACLDecisionRequest, snapshot *types.SourceACLSnapshot) bool {
	if snapshot.StaleAfter == nil {
		return false
	}
	now := request.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return !now.Before(*snapshot.StaleAfter)
}

func sourceACLAllow(
	request interfaces.SourceACLDecisionRequest,
	record *types.SourceACLRecord,
	reason string,
	subject *types.WeComACLSubject,
) *types.SourceACLDecision {
	decision := sourceACLDenyRecord(request, record, reason, subject)
	decision.Allowed = true
	return decision
}

func sourceACLDenyRecord(
	request interfaces.SourceACLDecisionRequest,
	record *types.SourceACLRecord,
	reason string,
	subject *types.WeComACLSubject,
) *types.SourceACLDecision {
	provider := ""
	if record != nil && record.Snapshot != nil {
		provider = record.Snapshot.Provider
	}
	decision := sourceACLDeny(request, provider, reason, subjectIDs(subject))
	if subject != nil {
		decision.WeComUserID = subject.WeComUserID
	}
	return decision
}

func sourceACLDeny(
	request interfaces.SourceACLDecisionRequest,
	provider string,
	reason string,
	subjectIDs []string,
) *types.SourceACLDecision {
	return &types.SourceACLDecision{
		Allowed:        false,
		Reason:         reason,
		Provider:       provider,
		KnowledgeID:    request.KnowledgeID,
		ActorUserID:    request.ActorUserID,
		Purpose:        request.Purpose,
		SubjectIDs:     subjectIDs,
		ServiceKeyCall: request.ServiceKeyCall,
	}
}

func subjectIDs(subject *types.WeComACLSubject) []string {
	if subject == nil {
		return nil
	}
	out := make([]string, 0, 1+len(subject.Departments)+len(subject.Groups))
	if subject.WeComUserID != "" {
		out = append(out, types.SourceACLSubjectWeComUser+":"+subject.WeComUserID)
	}
	for _, departmentID := range subject.Departments {
		out = append(out, types.SourceACLSubjectWeComDepartment+":"+departmentID)
	}
	for _, groupID := range subject.Groups {
		out = append(out, types.SourceACLSubjectWeComGroup+":"+groupID)
	}
	return out
}

func sourceACLContainsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
