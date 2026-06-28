package datasource

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestFeishuMetadataDoesNotAdvertiseWebhook(t *testing.T) {
	meta := ConnectorMetadataRegistry[types.ConnectorTypeFeishu]

	for _, capability := range meta.Capabilities {
		if capability == "webhook" {
			t.Fatalf("Feishu connector should not advertise webhook until webhook sync is implemented")
		}
	}
}

func TestWeComWeDriveMetadataExposesSyncCapabilities(t *testing.T) {
	meta := ConnectorMetadataRegistry[types.ConnectorTypeWeComWeDrive]

	if meta.Type != types.ConnectorTypeWeComWeDrive {
		t.Fatalf("metadata type = %q, want %q", meta.Type, types.ConnectorTypeWeComWeDrive)
	}
	if meta.AuthType != "custom" {
		t.Fatalf("auth type = %q, want custom", meta.AuthType)
	}
	if !hasCapability(meta.Capabilities, "incremental") || !hasCapability(meta.Capabilities, "deletion_sync") {
		t.Fatalf("capabilities = %#v, want incremental and deletion_sync", meta.Capabilities)
	}
}

func hasCapability(capabilities []string, want string) bool {
	for _, capability := range capabilities {
		if capability == want {
			return true
		}
	}
	return false
}
