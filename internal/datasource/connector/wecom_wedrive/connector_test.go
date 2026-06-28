package wecom_wedrive

import (
	"context"
	"strings"
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

func TestConnectorFetchExplicitlyNotImplemented(t *testing.T) {
	connector := NewConnector()
	_, err := connector.FetchAll(testContext(t), testDataSourceConfig(nil), nil)
	if err == nil || !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("FetchAll() error = %v", err)
	}
	_, _, err = connector.FetchIncremental(testContext(t), testDataSourceConfig(nil), nil)
	if err == nil || !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("FetchIncremental() error = %v", err)
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
