package wecom_wedrive

import (
	"fmt"
	"strings"
)

const (
	resourceKindSpace  = "space"
	resourceKindFolder = "folder"
	resourceKindFile   = "file"
)

type ResourceID struct {
	Kind    string
	SpaceID string
	FileID  string
}

func SpaceResourceID(spaceID string) string {
	return resourceKindSpace + ":" + spaceID
}

func FolderResourceID(spaceID, fileID string) string {
	return resourceKindFolder + ":" + spaceID + ":" + fileID
}

func FileResourceID(spaceID, fileID string) string {
	return resourceKindFile + ":" + spaceID + ":" + fileID
}

func ParseResourceID(raw string) (ResourceID, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ResourceID{}, fmt.Errorf("wecom wedrive resource id is empty")
	}

	parts := strings.Split(raw, ":")
	switch parts[0] {
	case resourceKindSpace:
		if len(parts) != 2 || parts[1] == "" {
			return ResourceID{}, fmt.Errorf("invalid wecom wedrive space resource id %q", raw)
		}
		return ResourceID{Kind: resourceKindSpace, SpaceID: parts[1]}, nil
	case resourceKindFolder, resourceKindFile:
		if len(parts) != 3 || parts[1] == "" || parts[2] == "" {
			return ResourceID{}, fmt.Errorf("invalid wecom wedrive %s resource id %q", parts[0], raw)
		}
		return ResourceID{Kind: parts[0], SpaceID: parts[1], FileID: parts[2]}, nil
	default:
		return ResourceID{}, fmt.Errorf("unknown wecom wedrive resource kind %q", parts[0])
	}
}
