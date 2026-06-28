package wecom_wedrive

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestConnectorValidateWithoutSpaceIDsOnlyChecksToken(t *testing.T) {
	calls := 0
	connector := NewConnectorWithClientFactory(func(cfg *Config) *Client {
		calls++
		return fakeClient(t, cfg)
	})
	err := connector.Validate(testContext(t), testDataSourceConfig(nil))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("client calls = %d, want 1", calls)
	}
}

func TestConnectorListResourcesRootAndFolder(t *testing.T) {
	connector := NewConnectorWithClientFactory(func(cfg *Config) *Client {
		return fakeClient(t, cfg)
	})
	cfg := testDataSourceConfig(map[string]interface{}{"space_ids": []interface{}{"space-1"}})

	root, err := connector.ListResources(testContext(t), cfg, "")
	if err != nil {
		t.Fatalf("ListResources(root) error = %v", err)
	}
	if len(root) != 1 || root[0].ExternalID != "space:space-1" || root[0].Name != "Engineering" {
		t.Fatalf("root resources = %#v", root)
	}

	children, err := connector.ListResources(testContext(t), cfg, root[0].ExternalID)
	if err != nil {
		t.Fatalf("ListResources(space) error = %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("children = %#v", children)
	}
	if children[0].ExternalID != "folder:space-1:folder-1" || !children[0].HasChildren {
		t.Fatalf("folder resource = %#v", children[0])
	}
	if children[1].ExternalID != "file:space-1:file-1" || children[1].HasChildren {
		t.Fatalf("file resource = %#v", children[1])
	}

	nested, err := connector.ListResources(testContext(t), cfg, children[0].ExternalID)
	if err != nil {
		t.Fatalf("ListResources(folder) error = %v", err)
	}
	if len(nested) != 1 || nested[0].ExternalID != "file:space-1:file-2" {
		t.Fatalf("nested resources = %#v", nested)
	}
}

func TestConnectorResolveResourceAncestors(t *testing.T) {
	connector := NewConnectorWithClientFactory(func(cfg *Config) *Client {
		return fakeClient(t, cfg)
	})
	ancestors, err := connector.ResolveResourceAncestors(
		testContext(t),
		testDataSourceConfig(map[string]interface{}{"space_ids": []interface{}{"space-1"}}),
		[]string{FileResourceID("space-1", "file-2")},
	)
	if err != nil {
		t.Fatalf("ResolveResourceAncestors() error = %v", err)
	}
	if len(ancestors) != 2 || ancestors[0] != "space:space-1" || ancestors[1] != "folder:space-1:folder-1" {
		t.Fatalf("ancestors = %#v", ancestors)
	}
}

func TestConnectorFetchAllDownloadsSupportedFiles(t *testing.T) {
	connector := NewConnectorWithClientFactory(func(cfg *Config) *Client {
		return fakeClient(t, cfg)
	})
	items, err := connector.FetchAll(
		testContext(t),
		testDataSourceConfig(map[string]interface{}{"space_ids": []interface{}{"space-1"}}),
		[]string{FolderResourceID("space-1", "folder-1")},
	)
	if err != nil {
		t.Fatalf("FetchAll() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items len = %d, want 1: %#v", len(items), items)
	}
	item := items[0]
	if item.ExternalID != "wecom_wedrive:space-1:file-2" {
		t.Fatalf("external id = %q", item.ExternalID)
	}
	if string(item.Content) != "docx bytes" {
		t.Fatalf("content = %q", string(item.Content))
	}
	if item.URL != "" {
		t.Fatalf("URL should not be used for WeDrive byte-backed items: %q", item.URL)
	}
	if item.Metadata["source_url"] != "" || item.Metadata["file_type"] != "2" || item.Metadata["access_mode"] != "public" {
		t.Fatalf("metadata = %#v", item.Metadata)
	}
}

func TestConnectorFetchAllSkipsUnsupportedBeforeDownload(t *testing.T) {
	server := newUnsupportedFileServer(t)
	defer server.Close()

	connector := NewConnectorWithClientFactory(func(cfg *Config) *Client {
		return NewClient(cfg, WithBaseURL(server.URL))
	})
	items, err := connector.FetchAll(
		testContext(t),
		testDataSourceConfig(map[string]interface{}{"space_ids": []interface{}{"space-1"}}),
		[]string{SpaceResourceID("space-1")},
	)
	if err != nil {
		t.Fatalf("FetchAll() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items len = %d, want 1: %#v", len(items), items)
	}
	if items[0].Metadata["skip_reason"] != "unsupported file type" {
		t.Fatalf("metadata = %#v", items[0].Metadata)
	}
	if len(items[0].Content) != 0 || items[0].URL != "" {
		t.Fatalf("unsupported item should not carry content or URL: %#v", items[0])
	}
}

func TestConnectorFetchAllLoadsMissingListingMetadata(t *testing.T) {
	server := newMissingMetadataServer(t)
	defer server.Close()

	connector := NewConnectorWithClientFactory(func(cfg *Config) *Client {
		return NewClient(cfg, WithBaseURL(server.URL))
	})
	items, err := connector.FetchAll(
		testContext(t),
		testDataSourceConfig(map[string]interface{}{"space_ids": []interface{}{"space-1"}}),
		[]string{SpaceResourceID("space-1")},
	)
	if err != nil {
		t.Fatalf("FetchAll() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items len = %d, want 1: %#v", len(items), items)
	}
	if items[0].FileName != "Recovered.md" || string(items[0].Content) != "# recovered" {
		t.Fatalf("item = %#v", items[0])
	}
}

func TestConnectorFetchAllRestrictedAttachesSourceACLMetadata(t *testing.T) {
	connector := NewConnectorWithClientFactory(func(cfg *Config) *Client {
		return fakeClient(t, cfg)
	})
	items, err := connector.FetchAll(
		testContext(t),
		testDataSourceConfig(map[string]interface{}{
			"space_ids":          []interface{}{"space-1"},
			"access_mode":        "restricted",
			"require_source_acl": true,
		}),
		[]string{FolderResourceID("space-1", "folder-1")},
	)
	if err != nil {
		t.Fatalf("FetchAll() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items len = %d, want 1: %#v", len(items), items)
	}
	item := items[0]
	if string(item.Content) != "docx bytes" {
		t.Fatalf("content = %q", string(item.Content))
	}
	if item.Metadata["access_mode"] != "restricted" || item.Metadata["queryable_state"] != "restricted" {
		t.Fatalf("metadata = %#v", item.Metadata)
	}
	if item.Metadata["source_acl_visibility"] != "all_company" || item.Metadata["source_acl_status"] != "ready" {
		t.Fatalf("source ACL metadata = %#v", item.Metadata)
	}
	if !strings.Contains(item.Metadata["source_acl_entries"], `"subject_type":"all_company"`) ||
		!strings.Contains(item.Metadata["source_acl_entries"], `"subject_type":"wecom_user"`) ||
		!strings.Contains(item.Metadata["source_acl_entries"], `"subject_id":"42"`) {
		t.Fatalf("source ACL entries = %s", item.Metadata["source_acl_entries"])
	}
	if item.Metadata["permission_fingerprint"] == "" {
		t.Fatalf("missing permission fingerprint: %#v", item.Metadata)
	}
}

func TestConnectorFetchAllRestrictedPermissionFailureDoesNotDownload(t *testing.T) {
	var downloadCalls int
	server := newPermissionFailureServer(t, &downloadCalls)
	defer server.Close()

	connector := NewConnectorWithClientFactory(func(cfg *Config) *Client {
		return NewClient(cfg, WithBaseURL(server.URL))
	})
	items, err := connector.FetchAll(
		testContext(t),
		testDataSourceConfig(map[string]interface{}{
			"space_ids":          []interface{}{"space-1"},
			"access_mode":        "restricted",
			"require_source_acl": true,
		}),
		[]string{SpaceResourceID("space-1")},
	)
	if err != nil {
		t.Fatalf("FetchAll() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items len = %d, want 1: %#v", len(items), items)
	}
	if len(items[0].Content) != 0 || items[0].Metadata["error"] == "" {
		t.Fatalf("item should fail closed without content: %#v", items[0])
	}
	if downloadCalls != 0 {
		t.Fatalf("file_download calls = %d, want 0", downloadCalls)
	}
}

func TestConnectorFetchIncrementalSkipsUnchangedAndEmitsDeletes(t *testing.T) {
	connector := NewConnectorWithClientFactory(func(cfg *Config) *Client {
		return fakeClient(t, cfg)
	})
	cfg := testDataSourceConfig(map[string]interface{}{"space_ids": []interface{}{"space-1"}})

	first, cursor, err := connector.FetchIncremental(testContext(t), cfg, nil)
	if err != nil {
		t.Fatalf("first FetchIncremental() error = %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("first items len = %d, want 2: %#v", len(first), first)
	}
	if cursor == nil || len(cursor.ConnectorCursor) == 0 {
		t.Fatalf("cursor = %#v", cursor)
	}

	second, _, err := connector.FetchIncremental(testContext(t), cfg, cursor)
	if err != nil {
		t.Fatalf("second FetchIncremental() error = %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("second items = %#v, want unchanged skip", second)
	}

	prev, err := decodeSyncCursor(cursor)
	if err != nil {
		t.Fatalf("decodeSyncCursor() error = %v", err)
	}
	prev.Files[fileStateKey("space-1", "deleted-file")] = syncFileState{
		SpaceID:  "space-1",
		FileID:   "deleted-file",
		FatherID: "folder-1",
		FileName: "Deleted.md",
		FileType: 2,
	}
	deletedCursor := prev.toSyncCursor()
	third, _, err := connector.FetchIncremental(testContext(t), cfg, deletedCursor)
	if err != nil {
		t.Fatalf("third FetchIncremental() error = %v", err)
	}
	foundDeleted := false
	for _, item := range third {
		if item.ExternalID == "wecom_wedrive:space-1:deleted-file" && item.IsDeleted {
			foundDeleted = true
		}
	}
	if !foundDeleted {
		t.Fatalf("deleted item not emitted: %#v", third)
	}
}

func TestConnectorFetchIncrementalDoesNotDeleteWhenRootSelectionChanges(t *testing.T) {
	connector := NewConnectorWithClientFactory(func(cfg *Config) *Client {
		return fakeClient(t, cfg)
	})
	cfg := testDataSourceConfig(map[string]interface{}{"space_ids": []interface{}{"space-1"}})
	prev := (&syncCursor{
		LastSyncTime: time.Now().UTC(),
		Roots:        []string{FolderResourceID("space-1", "old-folder")},
		Files: map[string]syncFileState{
			fileStateKey("space-1", "old-file"): {
				SpaceID:  "space-1",
				FileID:   "old-file",
				FileName: "Old.md",
				FileType: 2,
			},
		},
	}).toSyncCursor()

	items, _, err := connector.FetchIncremental(testContext(t), cfg, prev)
	if err != nil {
		t.Fatalf("FetchIncremental() error = %v", err)
	}
	for _, item := range items {
		if item.ExternalID == "wecom_wedrive:space-1:old-file" && item.IsDeleted {
			t.Fatalf("root selection changed but old file was deleted: %#v", items)
		}
	}
}

func TestDecodeSyncCursorRejectsMalformedConnectorCursor(t *testing.T) {
	_, err := decodeSyncCursor(&types.SyncCursor{
		ConnectorCursor: map[string]interface{}{"files": "bad"},
	})
	if err == nil {
		t.Fatal("decodeSyncCursor() expected error")
	}
}

func fakeClient(t *testing.T, cfg *Config) *Client {
	t.Helper()
	if cfg.CorpID != "ww123" || cfg.Secret != "secret" || cfg.UserID != "sync-user" {
		t.Fatalf("unexpected cfg = %#v", cfg)
	}
	server := newFakeWeDriveServer(t)
	t.Cleanup(server.Close)
	return NewClient(cfg, WithBaseURL(server.URL))
}

func testDataSourceConfig(settings map[string]interface{}) *types.DataSourceConfig {
	if settings == nil {
		settings = map[string]interface{}{}
	}
	return &types.DataSourceConfig{
		Type: types.ConnectorTypeWeComWeDrive,
		Credentials: map[string]interface{}{
			"corp_id": "ww123",
			"secret":  "secret",
			"userid":  "sync-user",
		},
		Settings: settings,
	}
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func newUnsupportedFileServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gettoken":
			writeJSON(t, w, map[string]interface{}{"errcode": 0, "access_token": "token-1", "expires_in": 7200})
		case "/wedrive/file_list":
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode file_list request: %v", err)
			}
			spaceID, _ := body["spaceid"].(string)
			writeJSON(t, w, map[string]interface{}{
				"errcode":  0,
				"has_more": false,
				"file_list": map[string]interface{}{"item": []map[string]interface{}{
					{
						"fileid":      "file-exe",
						"file_name":   "Setup.exe",
						"spaceid":     spaceID,
						"file_type":   2,
						"file_status": 1,
						"mtime":       1700000100,
					},
				}},
			})
		case "/wedrive/file_download":
			t.Fatalf("file_download should not be called for unsupported files")
		default:
			t.Fatalf("unexpected fake WeDrive path %s", r.URL.Path)
		}
	}))
}

func newPermissionFailureServer(t *testing.T, downloadCalls *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gettoken":
			writeJSON(t, w, map[string]interface{}{"errcode": 0, "access_token": "token-1", "expires_in": 7200})
		case "/wedrive/file_list":
			writeJSON(t, w, map[string]interface{}{
				"errcode":  0,
				"has_more": false,
				"file_list": map[string]interface{}{"item": []map[string]interface{}{
					{
						"fileid":      "file-1",
						"file_name":   "Plan.md",
						"spaceid":     "space-1",
						"file_type":   2,
						"file_status": 1,
						"mtime":       1700000100,
					},
				}},
			})
		case "/wedrive/get_file_permission":
			writeJSON(t, w, map[string]interface{}{"errcode": 60001, "errmsg": "no permission"})
		case "/wedrive/file_download":
			(*downloadCalls)++
			t.Fatalf("file_download should not be called when permission sync fails")
		default:
			t.Fatalf("unexpected fake WeDrive path %s", r.URL.Path)
		}
	}))
}

func newMissingMetadataServer(t *testing.T) *httptest.Server {
	t.Helper()
	var fileInfoCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gettoken":
			writeJSON(t, w, map[string]interface{}{"errcode": 0, "access_token": "token-1", "expires_in": 7200})
		case "/wedrive/file_list":
			writeJSON(t, w, map[string]interface{}{
				"errcode":  0,
				"has_more": false,
				"file_list": map[string]interface{}{"item": []map[string]interface{}{
					{
						"fileid":      "file-missing-metadata",
						"spaceid":     "space-1",
						"file_status": 1,
						"mtime":       1700000200,
					},
				}},
			})
		case "/wedrive/file_info":
			atomic.AddInt32(&fileInfoCalls, 1)
			writeJSON(t, w, map[string]interface{}{
				"errcode": 0,
				"file_info": map[string]interface{}{
					"fileid":      "file-missing-metadata",
					"file_name":   "Recovered.md",
					"spaceid":     "space-1",
					"file_type":   2,
					"file_status": 1,
					"mtime":       1700000200,
				},
			})
		case "/wedrive/file_download":
			if got := atomic.LoadInt32(&fileInfoCalls); got == 0 {
				t.Fatalf("file_info should run before file_download")
			}
			writeJSON(t, w, map[string]interface{}{
				"errcode":      0,
				"download_url": "http://" + r.Host + "/download/file-missing-metadata",
			})
		case "/download/file-missing-metadata":
			_, _ = w.Write([]byte("# recovered"))
		default:
			t.Fatalf("unexpected fake WeDrive path %s", r.URL.Path)
		}
	}))
	return server
}
