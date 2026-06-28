package wecom_wedrive

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

func collectRawPermissionValue(
	collector *permissionEntryCollector,
	value any,
	provenance string,
	inheritedFrom string,
) {
	switch v := value.(type) {
	case nil:
		return
	case map[string]any:
		collectRawPermissionMap(collector, v, provenance, inheritedFrom)
	case []any:
		for _, item := range v {
			collectRawPermissionValue(collector, item, provenance, inheritedFrom)
		}
	}
}

func collectRawPermissionMap(
	collector *permissionEntryCollector,
	values map[string]any,
	provenance string,
	inheritedFrom string,
) {
	if rawPermissionGrantsRead(values) {
		collectRawSubjects(collector, values, provenance, inheritedFrom)
	}
	for _, item := range values {
		collectRawPermissionValue(collector, item, provenance, inheritedFrom)
	}
}

func collectRawSubjects(
	collector *permissionEntryCollector,
	values map[string]any,
	provenance string,
	inheritedFrom string,
) {
	for _, userid := range anyStringSlice(firstAny(values, "userid", "userid_list", "userids", "user_list")) {
		collector.add(types.SourceACLSubjectWeComUser, userid, provenance, inheritedFrom)
	}
	for _, departmentID := range rawDepartmentIDs(values) {
		collector.add(types.SourceACLSubjectWeComDepartment, departmentID, provenance, inheritedFrom)
	}
	for _, groupID := range rawGroupIDs(values) {
		collector.add(types.SourceACLSubjectWeComGroup, groupID, provenance, inheritedFrom)
	}
}

func rawDepartmentIDs(values map[string]any) []string {
	return anyStringSlice(firstAny(
		values,
		"departmentid",
		"department_id",
		"departmentid_list",
		"department_ids",
		"partyid",
		"party_id",
		"partyid_list",
		"party_ids",
		"party_list",
	))
}

func rawGroupIDs(values map[string]any) []string {
	return anyStringSlice(firstAny(
		values,
		"groupid",
		"group_id",
		"groupid_list",
		"group_ids",
		"tagid",
		"tag_id",
		"tagid_list",
		"tag_ids",
	))
}

func rawPermissionGrantsRead(values map[string]any) bool {
	for _, key := range []string{"auth", "auth_type", "role"} {
		value := strings.TrimSpace(strings.ToLower(anyString(values[key])))
		if value == "" {
			continue
		}
		switch value {
		case "0", "false", "none", "deny", "denied", "forbid", "forbidden":
			return false
		default:
			return true
		}
	}
	return true
}

func firstAny(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			return value
		}
	}
	return nil
}

func anyString(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case json.Number:
		return v.String()
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case bool:
		if v {
			return "true"
		}
		return "false"
	}
	return ""
}

func anyStringSlice(value any) []string {
	switch v := value.(type) {
	case []string:
		return cleanStringSlice(v)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s := anyString(item); s != "" {
				out = append(out, s)
			}
		}
		return cleanStringSlice(out)
	case string:
		if strings.TrimSpace(v) == "" {
			return nil
		}
		return cleanStringSlice(strings.FieldsFunc(v, func(r rune) bool {
			return r == ',' || r == ';' || r == '\n' || r == '\r'
		}))
	}
	if s := anyString(value); s != "" {
		return []string{s}
	}
	return nil
}
