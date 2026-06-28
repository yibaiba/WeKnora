// Package wecom_wedrive implements the Enterprise WeChat built-in Microdisk
// (WeDrive / 企业微信微盘) data source connector.
//
// This child task only exposes credential validation and resource browsing.
// File sync is intentionally implemented in a later sync-pipeline task.
package wecom_wedrive

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	DefaultBaseURL  = "https://qyapi.weixin.qq.com/cgi-bin"
	DefaultPageSize = 100

	fileTypeFolder = 1
	fileStatusOK   = 1
)

type Config struct {
	CorpID   string `json:"corp_id"`
	Secret   string `json:"secret"`
	UserID   string `json:"userid"`
	SpaceIDs []string
	PageSize int
}

func parseConfig(config *types.DataSourceConfig) (*Config, error) {
	if config == nil {
		return nil, fmt.Errorf("%w: config is nil", datasource.ErrInvalidConfig)
	}
	credBytes, err := json.Marshal(config.Credentials)
	if err != nil {
		return nil, fmt.Errorf("marshal wecom wedrive credentials: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(credBytes, &cfg); err != nil {
		return nil, fmt.Errorf("parse wecom wedrive credentials: %w", err)
	}
	cfg.CorpID = strings.TrimSpace(cfg.CorpID)
	cfg.Secret = strings.TrimSpace(cfg.Secret)
	cfg.UserID = strings.TrimSpace(cfg.UserID)

	if cfg.CorpID == "" {
		return nil, fmt.Errorf("%w: corp_id is required", datasource.ErrInvalidCredentials)
	}
	if cfg.Secret == "" {
		return nil, fmt.Errorf("%w: secret is required", datasource.ErrInvalidCredentials)
	}
	if cfg.UserID == "" {
		return nil, fmt.Errorf("%w: userid is required", datasource.ErrInvalidCredentials)
	}

	if ids, err := spaceIDsFromConfig(config); err != nil {
		return nil, err
	} else {
		cfg.SpaceIDs = ids
	}
	if pageSize, err := pageSizeFromSettings(config.Settings); err != nil {
		return nil, err
	} else {
		cfg.PageSize = pageSize
	}
	if cfg.PageSize == 0 {
		cfg.PageSize = DefaultPageSize
	}
	return &cfg, nil
}

func spaceIDsFromConfig(config *types.DataSourceConfig) ([]string, error) {
	if raw, ok := config.Settings["space_ids"]; ok {
		return normalizeStringList(raw, "settings.space_ids")
	}
	if raw, ok := config.Credentials["space_ids"]; ok {
		return normalizeStringList(raw, "credentials.space_ids")
	}
	return nil, nil
}

func normalizeStringList(raw interface{}, field string) ([]string, error) {
	var values []string
	switch v := raw.(type) {
	case string:
		values = strings.FieldsFunc(v, func(r rune) bool {
			return r == '\n' || r == '\r' || r == ',' || r == ';'
		})
	case []string:
		values = append(values, v...)
	case []interface{}:
		values = make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%w: %s must contain only strings", datasource.ErrInvalidConfig, field)
			}
			values = append(values, s)
		}
	case nil:
		return nil, nil
	default:
		return nil, fmt.Errorf("%w: %s must be a string or string list", datasource.ErrInvalidConfig, field)
	}

	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out, nil
}

func pageSizeFromSettings(settings map[string]interface{}) (int, error) {
	if len(settings) == 0 {
		return 0, nil
	}
	raw, ok := settings["page_size"]
	if !ok || raw == nil {
		return 0, nil
	}
	switch v := raw.(type) {
	case int:
		return validatePageSize(v)
	case int64:
		return validatePageSize(int(v))
	case float64:
		if v != float64(int(v)) {
			return 0, fmt.Errorf("%w: page_size must be an integer", datasource.ErrInvalidConfig)
		}
		return validatePageSize(int(v))
	case json.Number:
		i, err := v.Int64()
		if err != nil {
			return 0, fmt.Errorf("%w: page_size must be an integer", datasource.ErrInvalidConfig)
		}
		return validatePageSize(int(i))
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0, fmt.Errorf("%w: page_size must be an integer", datasource.ErrInvalidConfig)
		}
		return validatePageSize(i)
	default:
		return 0, fmt.Errorf("%w: page_size must be an integer", datasource.ErrInvalidConfig)
	}
}

func validatePageSize(value int) (int, error) {
	if value <= 0 {
		return 0, fmt.Errorf("%w: page_size must be greater than zero", datasource.ErrInvalidConfig)
	}
	return value, nil
}

type tencentBaseResponse struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
}

type tokenResponse struct {
	tencentBaseResponse
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

type SpaceInfo struct {
	SpaceID      string        `json:"spaceid"`
	SpaceName    string        `json:"space_name"`
	SpaceSubType flexibleInt64 `json:"space_sub_type"`
}

type spaceInfoResponse struct {
	tencentBaseResponse
	SpaceInfo SpaceInfo `json:"space_info"`
}

type newSpaceInfoResponse struct {
	tencentBaseResponse
	SpaceInfo SpaceInfo   `json:"space_info"`
	Spaces    []SpaceInfo `json:"space_list"`
}

type WeDriveFile struct {
	FileID     string        `json:"fileid"`
	FileName   string        `json:"file_name"`
	SpaceID    string        `json:"spaceid"`
	FatherID   string        `json:"fatherid"`
	FileSize   flexibleInt64 `json:"file_size"`
	Ctime      flexibleInt64 `json:"ctime"`
	Mtime      flexibleInt64 `json:"mtime"`
	FileType   flexibleInt64 `json:"file_type"`
	FileStatus flexibleInt64 `json:"file_status"`
	SHA        string        `json:"sha"`
	MD5        string        `json:"md5"`
	URL        string        `json:"url"`
}

func (f WeDriveFile) isFolder() bool {
	return int64(f.FileType) == fileTypeFolder
}

func (f WeDriveFile) isDeleted() bool {
	status := int64(f.FileStatus)
	return status != 0 && status != fileStatusOK
}

type fileListResponse struct {
	tencentBaseResponse
	HasMore   bool               `json:"has_more"`
	NextStart flexibleInt64      `json:"next_start"`
	FileList  fileListCollection `json:"file_list"`
}

type fileInfoResponse struct {
	tencentBaseResponse
	FileInfo WeDriveFile `json:"file_info"`
}

type fileDownloadResponse struct {
	tencentBaseResponse
	DownloadURL string `json:"download_url"`
	CookieName  string `json:"cookie_name"`
	CookieValue string `json:"cookie_value"`
}

type filePermissionResponse struct {
	tencentBaseResponse
	AuthList map[string]interface{} `json:"auth_list"`
}
