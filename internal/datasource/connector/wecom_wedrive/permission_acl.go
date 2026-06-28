package wecom_wedrive

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	metadataRequireSourceACL = "require_source_acl"
)

type normalizedSourceACL struct {
	Visibility              string
	Status                  string
	Provenance              string
	InheritedFromResourceID string
	Entries                 []types.SourceACLMetadataEntry
	SourceHash              string
	SyncedAt                time.Time
	Error                   string
}

func normalizeFilePermission(
	permission *filePermissionResponse,
	fileID string,
	now time.Time,
) (*normalizedSourceACL, error) {
	if permission == nil {
		return nil, fmt.Errorf("wecom wedrive permission response is nil")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	acl := &normalizedSourceACL{
		Visibility: types.SourceACLVisibilityRestricted,
		Status:     types.SourceACLStatusUnmapped,
		Provenance: types.SourceACLProvenanceDirect,
		SyncedAt:   now.UTC(),
	}
	collector := newPermissionEntryCollector()

	if permission.ShareRange.allCompanyRead() {
		acl.Visibility = types.SourceACLVisibilityAllCompany
		collector.add(types.SourceACLSubjectAllCompany, "*", types.SourceACLProvenanceDirect, "")
	}

	collectPermissionNodes(collector, permission.FileMemberList, types.SourceACLProvenanceDirect, "")
	collectPermissionNodes(collector, permission.AuthList.Item, types.SourceACLProvenanceDirect, "")
	if permission.AuthList.isJSONObject() {
		collectRawPermissionValue(collector, permission.AuthList.Raw, types.SourceACLProvenanceDirect, "")
	}
	if permission.InheritedAuth != nil {
		acl.Provenance = types.SourceACLProvenanceInherited
		acl.InheritedFromResourceID = permission.InheritedAuth.effectiveFatherID()
		inheritedFrom := acl.InheritedFromResourceID
		collectPermissionNodes(collector, permission.InheritedAuth.AuthList.Item, types.SourceACLProvenanceInherited, inheritedFrom)
		if permission.InheritedAuth.AuthList.isJSONObject() {
			collectRawPermissionValue(collector, permission.InheritedAuth.AuthList.Raw, types.SourceACLProvenanceInherited, inheritedFrom)
		}
		collectPermissionNodes(collector, permission.InheritedAuth.FileMemberList, types.SourceACLProvenanceInherited, inheritedFrom)
	}

	acl.Entries = collector.entries()
	if len(acl.Entries) > 0 {
		acl.Status = types.SourceACLStatusReady
	}
	if acl.Visibility == types.SourceACLVisibilityAllCompany || acl.Visibility == types.SourceACLVisibilityPublic {
		acl.Status = types.SourceACLStatusReady
	}
	acl.SourceHash = permissionFingerprint(fileID, acl)
	return acl, nil
}

func applySourceACLMetadata(item *types.FetchedItem, acl *normalizedSourceACL) error {
	if item == nil || acl == nil {
		return nil
	}
	if item.Metadata == nil {
		item.Metadata = map[string]string{}
	}
	entries, err := json.Marshal(acl.Entries)
	if err != nil {
		return fmt.Errorf("marshal source ACL metadata entries: %w", err)
	}
	item.Metadata["access_mode"] = accessModeRestricted
	item.Metadata["queryable_state"] = accessModeRestricted
	item.Metadata[metadataRequireSourceACL] = "true"
	item.Metadata[types.SourceACLMetadataKeyVisibility] = acl.Visibility
	item.Metadata[types.SourceACLMetadataKeyStatus] = acl.Status
	item.Metadata[types.SourceACLMetadataKeyProvenance] = acl.Provenance
	item.Metadata[types.SourceACLMetadataKeyInheritedFromResourceID] = acl.InheritedFromResourceID
	item.Metadata[types.SourceACLMetadataKeySourceHash] = acl.SourceHash
	item.Metadata[types.SourceACLMetadataKeySyncedAt] = acl.SyncedAt.Format(time.RFC3339)
	item.Metadata[types.SourceACLMetadataKeyEntryCount] = strconv.Itoa(len(acl.Entries))
	item.Metadata[types.SourceACLMetadataKeyEntries] = string(entries)
	item.Metadata["permission_fingerprint"] = acl.SourceHash
	item.Metadata["permission_sync_time"] = acl.SyncedAt.Format(time.RFC3339)
	return nil
}

func permissionFingerprint(fileID string, acl *normalizedSourceACL) string {
	if acl == nil {
		return ""
	}
	payload := struct {
		FileID                  string                         `json:"file_id"`
		Visibility              string                         `json:"visibility"`
		Status                  string                         `json:"status"`
		Provenance              string                         `json:"provenance"`
		InheritedFromResourceID string                         `json:"inherited_from_resource_id"`
		Entries                 []types.SourceACLMetadataEntry `json:"entries"`
	}{
		FileID:                  strings.TrimSpace(fileID),
		Visibility:              acl.Visibility,
		Status:                  acl.Status,
		Provenance:              acl.Provenance,
		InheritedFromResourceID: acl.InheritedFromResourceID,
		Entries:                 append([]types.SourceACLMetadataEntry(nil), acl.Entries...),
	}
	sort.Slice(payload.Entries, func(i, j int) bool {
		left := payload.Entries[i]
		right := payload.Entries[j]
		return permissionEntrySortKey(left) < permissionEntrySortKey(right)
	})
	data, _ := json.Marshal(payload)
	sum := sha256.Sum256(data)
	return "acl:" + hex.EncodeToString(sum[:])[:16]
}

func permissionEntrySortKey(entry types.SourceACLMetadataEntry) string {
	return entry.SubjectType + "\x00" + entry.SubjectID + "\x00" + entry.Permission
}

type permissionEntryCollector struct {
	seen  map[string]struct{}
	items []types.SourceACLMetadataEntry
}

func newPermissionEntryCollector() *permissionEntryCollector {
	return &permissionEntryCollector{seen: map[string]struct{}{}}
}

func (c *permissionEntryCollector) add(subjectType, subjectID, provenance, inheritedFrom string) {
	subjectType = strings.TrimSpace(subjectType)
	subjectID = strings.TrimSpace(subjectID)
	if subjectType == "" || subjectID == "" {
		return
	}
	if subjectID == "*" {
		switch subjectType {
		case types.SourceACLSubjectAllCompany, types.SourceACLSubjectPublic:
		default:
			return
		}
	}
	key := subjectType + "\x00" + subjectID + "\x00" + types.SourceACLPermissionRead
	if _, ok := c.seen[key]; ok {
		return
	}
	c.seen[key] = struct{}{}
	c.items = append(c.items, types.SourceACLMetadataEntry{
		SubjectType:             subjectType,
		SubjectID:               subjectID,
		Permission:              types.SourceACLPermissionRead,
		Provenance:              defaultPermissionString(provenance, types.SourceACLProvenanceDirect),
		InheritedFromResourceID: strings.TrimSpace(inheritedFrom),
	})
}

func (c *permissionEntryCollector) entriesSorted() []types.SourceACLMetadataEntry {
	out := append([]types.SourceACLMetadataEntry(nil), c.items...)
	sort.Slice(out, func(i, j int) bool {
		return permissionEntrySortKey(out[i]) < permissionEntrySortKey(out[j])
	})
	return out
}

func (c *permissionEntryCollector) entries() []types.SourceACLMetadataEntry {
	return c.entriesSorted()
}

func collectPermissionNodes(
	collector *permissionEntryCollector,
	nodes []permissionNode,
	provenance string,
	inheritedFrom string,
) {
	for _, node := range nodes {
		collectPermissionNode(collector, node, provenance, inheritedFrom)
	}
}

func collectPermissionNode(
	collector *permissionEntryCollector,
	node permissionNode,
	provenance string,
	inheritedFrom string,
) {
	if !node.grantsRead() {
		return
	}
	for _, userid := range node.userIDs() {
		collector.add(types.SourceACLSubjectWeComUser, userid, provenance, inheritedFrom)
	}
	for _, departmentID := range node.departmentIDs() {
		collector.add(types.SourceACLSubjectWeComDepartment, departmentID, provenance, inheritedFrom)
	}
	for _, groupID := range node.groupIDs() {
		collector.add(types.SourceACLSubjectWeComGroup, groupID, provenance, inheritedFrom)
	}
}

func cleanStringSlice(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func defaultPermissionString(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	return fallback
}
