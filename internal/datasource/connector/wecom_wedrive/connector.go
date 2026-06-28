package wecom_wedrive

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

var _ datasource.Connector = (*Connector)(nil)

type Connector struct {
	clientFactory func(*Config) *Client
}

func NewConnector() *Connector {
	return &Connector{clientFactory: func(cfg *Config) *Client {
		return NewClient(cfg)
	}}
}

func NewConnectorWithClientFactory(factory func(*Config) *Client) *Connector {
	if factory == nil {
		return NewConnector()
	}
	return &Connector{clientFactory: factory}
}

func (c *Connector) Type() string {
	return types.ConnectorTypeWeComWeDrive
}

func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	cfg, err := parseConfig(config)
	if err != nil {
		return err
	}
	client := c.clientFactory(cfg)
	if _, err := client.GetToken(ctx); err != nil {
		return err
	}
	if len(cfg.SpaceIDs) == 0 {
		return nil
	}
	for _, spaceID := range cfg.SpaceIDs {
		if _, err := client.SpaceInfo(ctx, cfg.UserID, spaceID); err != nil {
			return fmt.Errorf("validate wecom wedrive space %s: %w", spaceID, err)
		}
	}
	return nil
}

func (c *Connector) ListResources(
	ctx context.Context, config *types.DataSourceConfig, parentID string,
) ([]types.Resource, error) {
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, err
	}
	client := c.clientFactory(cfg)

	if strings.TrimSpace(parentID) == "" {
		return c.listSpaces(ctx, client, cfg)
	}

	rid, err := ParseResourceID(parentID)
	if err != nil {
		return nil, err
	}
	switch rid.Kind {
	case resourceKindSpace:
		return c.listFiles(ctx, client, cfg, rid.SpaceID, "", parentID)
	case resourceKindFolder:
		return c.listFiles(ctx, client, cfg, rid.SpaceID, rid.FileID, parentID)
	case resourceKindFile:
		return []types.Resource{}, nil
	default:
		return nil, fmt.Errorf("unsupported wecom wedrive resource kind %q", rid.Kind)
	}
}

func (c *Connector) ResolveResourceAncestors(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]string, error) {
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, err
	}
	client := c.clientFactory(cfg)

	seen := map[string]struct{}{}
	ancestors := make([]string, 0)
	add := func(id string) {
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		ancestors = append(ancestors, id)
	}

	for _, raw := range resourceIDs {
		rid, err := ParseResourceID(raw)
		if err != nil {
			return nil, err
		}
		if rid.Kind == resourceKindSpace {
			continue
		}
		add(SpaceResourceID(rid.SpaceID))
		if err := c.addFileAncestors(ctx, client, cfg, rid, add); err != nil {
			return nil, err
		}
	}
	return ancestors, nil
}

func (c *Connector) FetchAll(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]types.FetchedItem, error) {
	return nil, errors.New("wecom_wedrive sync is not implemented yet; use the sync-pipeline task before triggering imports")
}

func (c *Connector) FetchIncremental(
	ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	return nil, nil, errors.New("wecom_wedrive incremental sync is not implemented yet; use the sync-pipeline task before triggering imports")
}

func (c *Connector) listSpaces(ctx context.Context, client *Client, cfg *Config) ([]types.Resource, error) {
	if len(cfg.SpaceIDs) == 0 {
		return nil, fmt.Errorf("%w: settings.space_ids is required to browse Enterprise WeChat Microdisk spaces", datasource.ErrInvalidConfig)
	}
	out := make([]types.Resource, 0, len(cfg.SpaceIDs))
	for _, spaceID := range cfg.SpaceIDs {
		info, err := client.SpaceInfo(ctx, cfg.UserID, spaceID)
		if err != nil {
			return nil, fmt.Errorf("load wecom wedrive space %s: %w", spaceID, err)
		}
		out = append(out, spaceInfoToResource(info, spaceID))
	}
	return out, nil
}

func (c *Connector) listFiles(
	ctx context.Context, client *Client, cfg *Config, spaceID string, fatherID string, parentID string,
) ([]types.Resource, error) {
	files, err := listAllFiles(ctx, client, cfg, spaceID, fatherID)
	if err != nil {
		return nil, err
	}
	out := make([]types.Resource, 0, len(files))
	for _, file := range files {
		if file.isDeleted() {
			continue
		}
		out = append(out, fileToResource(file, spaceID, parentID))
	}
	return out, nil
}

func listAllFiles(ctx context.Context, client *Client, cfg *Config, spaceID string, fatherID string) ([]WeDriveFile, error) {
	var all []WeDriveFile
	var start int64
	for {
		files, hasMore, nextStart, err := client.FileList(ctx, cfg.UserID, spaceID, fatherID, start, cfg.PageSize)
		if err != nil {
			return nil, fmt.Errorf("list wecom wedrive files space=%s father=%s: %w", spaceID, fatherID, err)
		}
		all = append(all, files...)
		if !hasMore {
			break
		}
		if nextStart == start {
			return nil, fmt.Errorf("list wecom wedrive files space=%s father=%s: pagination did not advance", spaceID, fatherID)
		}
		start = nextStart
	}
	return all, nil
}

func (c *Connector) addFileAncestors(
	ctx context.Context, client *Client, cfg *Config, rid ResourceID, add func(string),
) error {
	currentID := rid.FileID
	visited := map[string]struct{}{}
	for currentID != "" {
		if _, ok := visited[currentID]; ok {
			return fmt.Errorf("wecom wedrive ancestor cycle detected at file %s", currentID)
		}
		visited[currentID] = struct{}{}

		file, err := client.FileInfo(ctx, cfg.UserID, rid.SpaceID, currentID)
		if err != nil {
			return fmt.Errorf("resolve wecom wedrive file ancestor %s: %w", currentID, err)
		}
		parentID := strings.TrimSpace(file.FatherID)
		if parentID == "" {
			break
		}
		add(FolderResourceID(rid.SpaceID, parentID))
		currentID = parentID
	}
	return nil
}

func spaceInfoToResource(info *SpaceInfo, fallbackID string) types.Resource {
	spaceID := strings.TrimSpace(fallbackID)
	name := spaceID
	if info != nil {
		if info.SpaceID != "" {
			spaceID = info.SpaceID
		}
		if strings.TrimSpace(info.SpaceName) != "" {
			name = strings.TrimSpace(info.SpaceName)
		}
	}
	if name == "" {
		name = spaceID
	}
	return types.Resource{
		ExternalID:  SpaceResourceID(spaceID),
		Name:        name,
		Type:        "wedrive_space",
		HasChildren: true,
		Metadata: map[string]interface{}{
			"provider": "wecom_wedrive",
			"space_id": spaceID,
		},
	}
}

func fileToResource(file WeDriveFile, spaceID string, parentID string) types.Resource {
	if file.SpaceID != "" {
		spaceID = file.SpaceID
	}
	name := strings.TrimSpace(file.FileName)
	if name == "" {
		name = file.FileID
	}
	resourceType := "wedrive_file"
	externalID := FileResourceID(spaceID, file.FileID)
	if file.isFolder() {
		resourceType = "wedrive_folder"
		externalID = FolderResourceID(spaceID, file.FileID)
	}
	return types.Resource{
		ExternalID:  externalID,
		Name:        name,
		Type:        resourceType,
		URL:         file.URL,
		ParentID:    parentID,
		HasChildren: file.isFolder(),
		ModifiedAt:  unixTime(int64(file.Mtime)),
		Metadata: map[string]interface{}{
			"provider":    "wecom_wedrive",
			"space_id":    spaceID,
			"file_id":     file.FileID,
			"father_id":   file.FatherID,
			"file_type":   int64(file.FileType),
			"file_status": int64(file.FileStatus),
			"file_size":   int64(file.FileSize),
			"ctime":       int64(file.Ctime),
			"mtime":       int64(file.Mtime),
			"sha":         file.SHA,
			"md5":         file.MD5,
			"url":         file.URL,
		},
	}
}

func unixTime(seconds int64) time.Time {
	if seconds <= 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0).UTC()
}
