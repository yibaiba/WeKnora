package wecom_wedrive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	metadataProvider = "wecom_wedrive"
)

var supportedSyncExtensions = map[string]struct{}{
	"pdf": {}, "txt": {}, "docx": {}, "doc": {}, "epub": {}, "mhtml": {},
	"md": {}, "markdown": {}, "png": {}, "jpg": {}, "jpeg": {}, "gif": {},
	"csv": {}, "xlsx": {}, "xls": {}, "pptx": {}, "ppt": {}, "json": {},
	"mp3": {}, "wav": {}, "m4a": {}, "flac": {}, "ogg": {},
}

type syncCursor struct {
	LastSyncTime time.Time                `json:"last_sync_time"`
	Roots        []string                 `json:"roots,omitempty"`
	Files        map[string]syncFileState `json:"files"`
}

type syncFileState struct {
	SpaceID               string `json:"space_id"`
	FileID                string `json:"file_id"`
	FatherID              string `json:"father_id,omitempty"`
	FileName              string `json:"file_name,omitempty"`
	FileType              int64  `json:"file_type,omitempty"`
	FileStatus            int64  `json:"file_status,omitempty"`
	FileSize              int64  `json:"file_size,omitempty"`
	Ctime                 int64  `json:"ctime,omitempty"`
	Mtime                 int64  `json:"mtime,omitempty"`
	MD5                   string `json:"md5,omitempty"`
	SHA                   string `json:"sha,omitempty"`
	PermissionFingerprint string `json:"permission_fingerprint,omitempty"`
}

type syncFileRecord struct {
	File             WeDriveFile
	SpaceID          string
	SelectedResource string
}

func (c *Connector) walkSync(
	ctx context.Context,
	config *types.DataSourceConfig,
	resourceIDs []string,
	prev *syncCursor,
	incremental bool,
) ([]types.FetchedItem, *syncCursor, error) {
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, nil, err
	}
	return c.walkSyncWithConfig(ctx, cfg, resourceIDs, prev, incremental)
}

func (c *Connector) walkSyncWithConfig(
	ctx context.Context,
	cfg *Config,
	resourceIDs []string,
	prev *syncCursor,
	incremental bool,
) ([]types.FetchedItem, *syncCursor, error) {
	if err := cfg.validatePublicSync(); err != nil {
		return nil, nil, err
	}
	roots, err := syncRoots(cfg, resourceIDs)
	if err != nil {
		return nil, nil, err
	}

	client := c.clientFactory(cfg)
	next := &syncCursor{
		LastSyncTime: time.Now().UTC(),
		Roots:        roots,
		Files:        make(map[string]syncFileState),
	}
	seen := make(map[string]struct{})
	items, warnings := c.collectSyncItems(ctx, client, cfg, roots, prev, next, seen, incremental)

	if incremental && prev != nil && sameRootSelection(prev.Roots, roots) {
		items = append(items, deletedItems(prev, seen)...)
	}
	if len(warnings) > 0 {
		if len(next.Files) == 0 && len(items) == 0 {
			return nil, next, fmt.Errorf("%w: %s", datasource.ErrFetchFailed, strings.Join(warnings, "; "))
		}
		return items, next, &datasource.PartialFetchError{Details: warnings}
	}
	return items, next, nil
}

func (c *Connector) collectSyncItems(
	ctx context.Context,
	client *Client,
	cfg *Config,
	roots []string,
	prev *syncCursor,
	next *syncCursor,
	seen map[string]struct{},
	incremental bool,
) ([]types.FetchedItem, []string) {
	var items []types.FetchedItem
	var warnings []string
	for _, root := range roots {
		records, err := c.collectRootFiles(ctx, client, cfg, root)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %s", root, redactURLSecrets(err.Error())))
		}
		for _, record := range records {
			state := fileState(record.SpaceID, record.File)
			key := fileStateKey(state.SpaceID, state.FileID)
			seen[key] = struct{}{}
			next.Files[key] = state
			if incremental && prev != nil && prev.sameFileState(key, state) {
				continue
			}
			items = append(items, c.fetchedItem(ctx, client, cfg, record, state))
		}
	}
	return items, warnings
}

func (c *Connector) collectRootFiles(
	ctx context.Context, client *Client, cfg *Config, rawResourceID string,
) ([]syncFileRecord, error) {
	rid, err := ParseResourceID(rawResourceID)
	if err != nil {
		return nil, err
	}
	switch rid.Kind {
	case resourceKindSpace:
		return c.collectFolderFiles(ctx, client, cfg, rid.SpaceID, "", rawResourceID, map[string]struct{}{})
	case resourceKindFolder:
		return c.collectFolderFiles(ctx, client, cfg, rid.SpaceID, rid.FileID, rawResourceID, map[string]struct{}{})
	case resourceKindFile:
		file, err := client.FileInfo(ctx, cfg.UserID, rid.SpaceID, rid.FileID)
		if err != nil {
			return nil, fmt.Errorf("load wecom wedrive file %s: %w", rid.FileID, err)
		}
		if file.isDeleted() {
			return nil, nil
		}
		if file.SpaceID == "" {
			file.SpaceID = rid.SpaceID
		}
		if file.isFolder() {
			return c.collectFolderFiles(ctx, client, cfg, rid.SpaceID, file.FileID, rawResourceID, map[string]struct{}{})
		}
		return []syncFileRecord{{File: *file, SpaceID: rid.SpaceID, SelectedResource: rawResourceID}}, nil
	default:
		return nil, fmt.Errorf("unsupported wecom wedrive resource kind %q", rid.Kind)
	}
}

func (c *Connector) collectFolderFiles(
	ctx context.Context,
	client *Client,
	cfg *Config,
	spaceID string,
	fatherID string,
	selectedResource string,
	visited map[string]struct{},
) ([]syncFileRecord, error) {
	visitKey := fileStateKey(spaceID, fatherID)
	if fatherID != "" {
		if _, ok := visited[visitKey]; ok {
			return nil, fmt.Errorf("wecom wedrive folder cycle detected at %s", visitKey)
		}
		visited[visitKey] = struct{}{}
	}

	files, err := listAllFiles(ctx, client, cfg, spaceID, fatherID)
	if err != nil {
		return nil, err
	}

	var records []syncFileRecord
	var warnings []string
	for _, file := range files {
		if file.isDeleted() {
			continue
		}
		if file.SpaceID == "" {
			file.SpaceID = spaceID
		}
		file, err := c.ensureListedFileInfo(ctx, client, cfg, spaceID, file)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("file %s: %s", file.FileID, redactURLSecrets(err.Error())))
			continue
		}
		if file.isFolder() {
			child, err := c.collectFolderFiles(ctx, client, cfg, spaceID, file.FileID, selectedResource, visited)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("folder %s: %s", file.FileID, redactURLSecrets(err.Error())))
				continue
			}
			records = append(records, child...)
			continue
		}
		records = append(records, syncFileRecord{
			File:             file,
			SpaceID:          spaceID,
			SelectedResource: selectedResource,
		})
	}
	if len(warnings) > 0 {
		return records, errors.New(strings.Join(warnings, "; "))
	}
	return records, nil
}

func (c *Connector) ensureListedFileInfo(
	ctx context.Context,
	client *Client,
	cfg *Config,
	spaceID string,
	file WeDriveFile,
) (WeDriveFile, error) {
	if !listedFileNeedsInfo(file) {
		return file, nil
	}
	info, err := client.FileInfo(ctx, cfg.UserID, spaceID, file.FileID)
	if err != nil {
		return file, fmt.Errorf("load wecom wedrive file metadata: %w", err)
	}
	return mergeWeDriveFile(file, *info, spaceID), nil
}

func listedFileNeedsInfo(file WeDriveFile) bool {
	return strings.TrimSpace(file.FileName) == "" || int64(file.FileType) == 0
}

func mergeWeDriveFile(listed WeDriveFile, info WeDriveFile, fallbackSpaceID string) WeDriveFile {
	if info.FileID == "" {
		info.FileID = listed.FileID
	}
	if info.SpaceID == "" {
		if listed.SpaceID != "" {
			info.SpaceID = listed.SpaceID
		} else {
			info.SpaceID = fallbackSpaceID
		}
	}
	if info.FileName == "" {
		info.FileName = listed.FileName
	}
	if info.FatherID == "" {
		info.FatherID = listed.FatherID
	}
	if info.FileSize == 0 {
		info.FileSize = listed.FileSize
	}
	if info.Ctime == 0 {
		info.Ctime = listed.Ctime
	}
	if info.Mtime == 0 {
		info.Mtime = listed.Mtime
	}
	if info.FileType == 0 {
		info.FileType = listed.FileType
	}
	if info.FileStatus == 0 {
		info.FileStatus = listed.FileStatus
	}
	if info.SHA == "" {
		info.SHA = listed.SHA
	}
	if info.MD5 == "" {
		info.MD5 = listed.MD5
	}
	if info.URL == "" {
		info.URL = listed.URL
	}
	return info
}

func (c *Connector) fetchedItem(
	ctx context.Context,
	client *Client,
	cfg *Config,
	record syncFileRecord,
	state syncFileState,
) types.FetchedItem {
	item := baseFetchedItem(record, state)
	if !isSupportedSyncFile(state.FileName) {
		item.Metadata["skip_reason"] = "unsupported file type"
		return item
	}

	link, err := client.FileDownload(ctx, cfg.UserID, state.FileID)
	if err != nil {
		item.Metadata["error"] = redactURLSecrets(err.Error())
		return item
	}
	data, err := client.DownloadFileBytes(ctx, link.DownloadURL, link.CookieName, link.CookieValue)
	if err != nil {
		item.Metadata["error"] = redactURLSecrets(err.Error())
		return item
	}
	item.Content = data
	item.ContentType = contentTypeForName(state.FileName)
	return item
}

func baseFetchedItem(record syncFileRecord, state syncFileState) types.FetchedItem {
	meta := map[string]string{
		"provider":        metadataProvider,
		"space_id":        state.SpaceID,
		"file_id":         state.FileID,
		"father_id":       state.FatherID,
		"file_name":       state.FileName,
		"file_type":       strconv.FormatInt(state.FileType, 10),
		"file_status":     strconv.FormatInt(state.FileStatus, 10),
		"file_size":       strconv.FormatInt(state.FileSize, 10),
		"ctime":           strconv.FormatInt(state.Ctime, 10),
		"mtime":           strconv.FormatInt(state.Mtime, 10),
		"md5":             state.MD5,
		"sha":             state.SHA,
		"access_mode":     accessModePublic,
		"queryable_state": accessModePublic,
		"source_path":     record.SelectedResource,
		"source_url":      record.File.URL,
	}
	if state.PermissionFingerprint != "" {
		meta["permission_fingerprint"] = state.PermissionFingerprint
		meta["permission_sync_time"] = time.Now().UTC().Format(time.RFC3339)
	}

	return types.FetchedItem{
		ExternalID:       externalFileID(state.SpaceID, state.FileID),
		Title:            state.FileName,
		FileName:         state.FileName,
		UpdatedAt:        unixTime(state.Mtime),
		SourceResourceID: record.SelectedResource,
		Metadata:         meta,
	}
}

func deletedItems(prev *syncCursor, seen map[string]struct{}) []types.FetchedItem {
	if prev == nil || len(prev.Files) == 0 {
		return nil
	}
	keys := make([]string, 0, len(prev.Files))
	for key := range prev.Files {
		if _, ok := seen[key]; !ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	out := make([]types.FetchedItem, 0, len(keys))
	for _, key := range keys {
		state := prev.Files[key]
		out = append(out, types.FetchedItem{
			ExternalID:       externalFileID(state.SpaceID, state.FileID),
			Title:            state.FileName,
			FileName:         state.FileName,
			IsDeleted:        true,
			SourceResourceID: state.FatherID,
			Metadata: map[string]string{
				"provider":    metadataProvider,
				"space_id":    state.SpaceID,
				"file_id":     state.FileID,
				"father_id":   state.FatherID,
				"file_name":   state.FileName,
				"file_type":   strconv.FormatInt(state.FileType, 10),
				"file_status": strconv.FormatInt(state.FileStatus, 10),
			},
		})
	}
	return out
}

func syncRoots(cfg *Config, resourceIDs []string) ([]string, error) {
	var roots []string
	if len(resourceIDs) > 0 {
		roots = append(roots, resourceIDs...)
	} else {
		for _, spaceID := range cfg.SpaceIDs {
			roots = append(roots, SpaceResourceID(spaceID))
		}
	}
	out := make([]string, 0, len(roots))
	seen := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		if _, ok := seen[root]; ok {
			continue
		}
		if _, err := ParseResourceID(root); err != nil {
			return nil, err
		}
		seen[root] = struct{}{}
		out = append(out, root)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: no wecom_wedrive resource_ids or settings.space_ids configured", datasource.ErrInvalidConfig)
	}
	return out, nil
}

func sameRootSelection(prev []string, current []string) bool {
	if len(prev) != len(current) {
		return false
	}
	for i := range prev {
		if prev[i] != current[i] {
			return false
		}
	}
	return true
}

func fileState(spaceID string, file WeDriveFile) syncFileState {
	if file.SpaceID != "" {
		spaceID = file.SpaceID
	}
	return syncFileState{
		SpaceID:    spaceID,
		FileID:     file.FileID,
		FatherID:   file.FatherID,
		FileName:   safeSyncFileName(file.FileName, file.FileID),
		FileType:   int64(file.FileType),
		FileStatus: int64(file.FileStatus),
		FileSize:   int64(file.FileSize),
		Ctime:      int64(file.Ctime),
		Mtime:      int64(file.Mtime),
		MD5:        strings.TrimSpace(file.MD5),
		SHA:        strings.TrimSpace(file.SHA),
	}
}

func (c *syncCursor) sameFileState(key string, state syncFileState) bool {
	if c == nil || c.Files == nil {
		return false
	}
	prev, ok := c.Files[key]
	if !ok {
		return false
	}
	return prev.FileSize == state.FileSize &&
		prev.Mtime == state.Mtime &&
		prev.MD5 == state.MD5 &&
		prev.SHA == state.SHA &&
		prev.PermissionFingerprint == state.PermissionFingerprint
}

func fileStateKey(spaceID, fileID string) string {
	return spaceID + ":" + fileID
}

func externalFileID(spaceID, fileID string) string {
	return metadataProvider + ":" + spaceID + ":" + fileID
}

func safeSyncFileName(name string, fallback string) string {
	name = strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	if name != "" {
		name = path.Base(name)
	}
	if name == "" || name == "." || name == "/" {
		name = strings.TrimSpace(fallback)
	}
	if name == "" {
		return "wedrive-file"
	}
	return name
}

func isSupportedSyncFile(filename string) bool {
	_, ok := supportedSyncExtensions[fileExtension(filename)]
	return ok
}

func fileExtension(filename string) string {
	filename = strings.TrimSpace(filename)
	dot := strings.LastIndex(filename, ".")
	if dot < 0 || dot == len(filename)-1 {
		return ""
	}
	return strings.ToLower(filename[dot+1:])
}

func contentTypeForName(filename string) string {
	if ext := fileExtension(filename); ext != "" {
		if ctype := mime.TypeByExtension("." + ext); ctype != "" {
			return ctype
		}
	}
	return "application/octet-stream"
}

func decodeSyncCursor(cursor *types.SyncCursor) (*syncCursor, error) {
	if cursor == nil || cursor.ConnectorCursor == nil {
		return nil, nil
	}
	var out syncCursor
	data, err := json.Marshal(cursor.ConnectorCursor)
	if err != nil {
		return nil, fmt.Errorf("marshal wecom_wedrive cursor: %w", err)
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse wecom_wedrive cursor: %w", err)
	}
	if out.Files == nil {
		out.Files = make(map[string]syncFileState)
	}
	return &out, nil
}

func (c *syncCursor) toSyncCursor() *types.SyncCursor {
	if c == nil {
		return nil
	}
	cursorMap := make(map[string]interface{})
	data, _ := json.Marshal(c)
	_ = json.Unmarshal(data, &cursorMap)
	return &types.SyncCursor{
		LastSyncTime:    c.LastSyncTime,
		ConnectorCursor: cursorMap,
	}
}
