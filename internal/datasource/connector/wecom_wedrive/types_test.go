package wecom_wedrive

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestParseConfigFromCredentialsAndSettings(t *testing.T) {
	cfg, err := parseConfig(&types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"corp_id": " ww123 ",
			"secret":  " secret ",
			"userid":  " user1 ",
		},
		Settings: map[string]interface{}{
			"space_ids": "space-a\nspace-b, space-a",
			"page_size": "50",
		},
	})
	if err != nil {
		t.Fatalf("parseConfig() error = %v", err)
	}
	if cfg.CorpID != "ww123" || cfg.Secret != "secret" || cfg.UserID != "user1" {
		t.Fatalf("unexpected credentials: %#v", cfg)
	}
	if got := cfg.SpaceIDs; len(got) != 2 || got[0] != "space-a" || got[1] != "space-b" {
		t.Fatalf("space ids = %#v", got)
	}
	if cfg.PageSize != 50 {
		t.Fatalf("page size = %d, want 50", cfg.PageSize)
	}
}

func TestParseConfigSupportsCredentialSpaceIDsForRawValidation(t *testing.T) {
	cfg, err := parseConfig(&types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"corp_id":   "ww123",
			"secret":    "secret",
			"userid":    "user1",
			"space_ids": "space-a\nspace-b",
		},
	})
	if err != nil {
		t.Fatalf("parseConfig() error = %v", err)
	}
	if got := cfg.SpaceIDs; len(got) != 2 || got[0] != "space-a" || got[1] != "space-b" {
		t.Fatalf("space ids = %#v", got)
	}
}

func TestParseConfigSettingsSpaceIDsOverrideCredentialFallback(t *testing.T) {
	cfg, err := parseConfig(&types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"corp_id":   "ww123",
			"secret":    "secret",
			"userid":    "user1",
			"space_ids": "credential-space",
		},
		Settings: map[string]interface{}{
			"space_ids": []interface{}{"settings-space"},
		},
	})
	if err != nil {
		t.Fatalf("parseConfig() error = %v", err)
	}
	if got := cfg.SpaceIDs; len(got) != 1 || got[0] != "settings-space" {
		t.Fatalf("space ids = %#v", got)
	}
}

func TestParseConfigRejectsMissingCredentials(t *testing.T) {
	_, err := parseConfig(&types.DataSourceConfig{
		Credentials: map[string]interface{}{"corp_id": "ww123", "secret": "secret"},
	})
	if err == nil {
		t.Fatal("parseConfig() expected error")
	}
}

func TestFileListCollectionUnmarshalSupportsWrappedAndArray(t *testing.T) {
	var wrapped fileListResponse
	err := json.Unmarshal([]byte(`{
		"errcode":0,
		"file_list":{"item":[{"fileid":"f1","file_type":"1"}]},
		"next_start":"10",
		"has_more":true
	}`), &wrapped)
	if err != nil {
		t.Fatalf("unmarshal wrapped: %v", err)
	}
	if len(wrapped.FileList) != 1 || wrapped.FileList[0].FileID != "f1" || !wrapped.FileList[0].isFolder() {
		t.Fatalf("wrapped file list = %#v", wrapped.FileList)
	}
	if int64(wrapped.NextStart) != 10 || !wrapped.HasMore {
		t.Fatalf("pagination = %d %v", wrapped.NextStart, wrapped.HasMore)
	}

	var array fileListResponse
	err = json.Unmarshal([]byte(`{"errcode":0,"file_list":[{"fileid":"f2","file_type":2}]}`), &array)
	if err != nil {
		t.Fatalf("unmarshal array: %v", err)
	}
	if len(array.FileList) != 1 || array.FileList[0].FileID != "f2" {
		t.Fatalf("array file list = %#v", array.FileList)
	}
}
