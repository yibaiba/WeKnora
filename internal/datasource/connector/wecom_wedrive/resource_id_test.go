package wecom_wedrive

import "testing"

func TestResourceIDRoundtrip(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want ResourceID
	}{
		{
			name: "space",
			raw:  SpaceResourceID("space-1"),
			want: ResourceID{Kind: resourceKindSpace, SpaceID: "space-1"},
		},
		{
			name: "folder",
			raw:  FolderResourceID("space-1", "folder-1"),
			want: ResourceID{Kind: resourceKindFolder, SpaceID: "space-1", FileID: "folder-1"},
		},
		{
			name: "file",
			raw:  FileResourceID("space-1", "file-1"),
			want: ResourceID{Kind: resourceKindFile, SpaceID: "space-1", FileID: "file-1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseResourceID(tt.raw)
			if err != nil {
				t.Fatalf("ParseResourceID() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("ParseResourceID() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestParseResourceIDRejectsInvalid(t *testing.T) {
	tests := []string{
		"",
		"space:",
		"space:a:b",
		"folder:space",
		"folder::file",
		"file:space:",
		"other:space:file",
	}
	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			if _, err := ParseResourceID(raw); err == nil {
				t.Fatalf("ParseResourceID(%q) expected error", raw)
			}
		})
	}
}
