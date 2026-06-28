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

func TestWeComWeDriveMetadataIsExposedWithoutSyncCapabilities(t *testing.T) {
	meta := ConnectorMetadataRegistry[types.ConnectorTypeWeComWeDrive]

	if meta.Type != types.ConnectorTypeWeComWeDrive {
		t.Fatalf("metadata type = %q, want %q", meta.Type, types.ConnectorTypeWeComWeDrive)
	}
	if meta.AuthType != "custom" {
		t.Fatalf("auth type = %q, want custom", meta.AuthType)
	}
	if len(meta.Capabilities) != 0 {
		t.Fatalf("capabilities = %#v, want none until sync pipeline is implemented", meta.Capabilities)
	}
}
