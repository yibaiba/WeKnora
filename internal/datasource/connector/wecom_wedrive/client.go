package wecom_wedrive

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
)

const (
	defaultHTTPTimeout = 30 * time.Second
	tokenRefreshMargin = 5 * time.Minute
)

type Client struct {
	baseURL string
	corpID  string
	secret  string

	httpClient *http.Client

	tokenMu    sync.Mutex
	tokenCache string
	tokenExpAt time.Time
}

type ClientOption func(*Client)

func WithHTTPClient(httpClient *http.Client) ClientOption {
	return func(c *Client) {
		if httpClient != nil {
			c.httpClient = httpClient
		}
	}
}

func WithBaseURL(baseURL string) ClientOption {
	return func(c *Client) {
		baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
		if baseURL != "" {
			c.baseURL = baseURL
		}
	}
}

func NewClient(config *Config, opts ...ClientOption) *Client {
	c := &Client{
		baseURL:    DefaultBaseURL,
		corpID:     config.CorpID,
		secret:     config.Secret,
		httpClient: &http.Client{Timeout: defaultHTTPTimeout},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) GetToken(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	if c.tokenCache != "" && time.Now().Before(c.tokenExpAt) {
		return c.tokenCache, nil
	}

	reqURL := c.baseURL + "/gettoken?corpid=" + url.QueryEscape(c.corpID) +
		"&corpsecret=" + url.QueryEscape(c.secret)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "", fmt.Errorf("create token request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: request wecom token: %s", datasource.ErrInvalidCredentials, sanitizeHTTPError(err))
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read token response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("%w: token http status=%d", datasource.ErrInvalidCredentials, resp.StatusCode)
	}

	var result tokenResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if result.ErrCode != 0 {
		return "", fmt.Errorf("%w: %w", datasource.ErrInvalidCredentials, &TencentAPIError{
			Endpoint: "/gettoken",
			ErrCode:  result.ErrCode,
			ErrMsg:   result.ErrMsg,
		})
	}
	if strings.TrimSpace(result.AccessToken) == "" {
		return "", fmt.Errorf("%w: token response missing access_token", datasource.ErrInvalidCredentials)
	}

	c.tokenCache = result.AccessToken
	ttl := time.Duration(result.ExpiresIn) * time.Second
	if ttl > tokenRefreshMargin {
		ttl -= tokenRefreshMargin
	}
	if ttl <= 0 {
		ttl = time.Minute
	}
	c.tokenExpAt = time.Now().Add(ttl)
	return c.tokenCache, nil
}

func (c *Client) SpaceInfo(ctx context.Context, userid string, spaceID string) (*SpaceInfo, error) {
	var result spaceInfoResponse
	if err := c.postJSON(ctx, "/wedrive/space_info", map[string]string{
		"userid":  userid,
		"spaceid": spaceID,
	}, &result); err != nil {
		return nil, err
	}
	info := result.SpaceInfo
	if info.SpaceID == "" {
		info.SpaceID = spaceID
	}
	return &info, nil
}

func (c *Client) NewSpaceInfo(ctx context.Context, userid string, spaceID string) (*SpaceInfo, error) {
	var result newSpaceInfoResponse
	if err := c.postJSON(ctx, "/wedrive/new_space_info", map[string]string{
		"userid":  userid,
		"spaceid": spaceID,
	}, &result); err != nil {
		return nil, err
	}
	info := result.SpaceInfo
	if info.SpaceID == "" && len(result.Spaces) > 0 {
		info = result.Spaces[0]
	}
	if info.SpaceID == "" {
		info.SpaceID = spaceID
	}
	return &info, nil
}

func (c *Client) FileList(
	ctx context.Context, userid string, spaceID string, fatherID string, start int64, limit int,
) ([]WeDriveFile, bool, int64, error) {
	body := map[string]interface{}{
		"userid":  userid,
		"spaceid": spaceID,
		"start":   start,
		"limit":   limit,
	}
	if fatherID != "" {
		body["fatherid"] = fatherID
	}

	var result fileListResponse
	if err := c.postJSON(ctx, "/wedrive/file_list", body, &result); err != nil {
		return nil, false, 0, err
	}
	return []WeDriveFile(result.FileList), result.HasMore, int64(result.NextStart), nil
}

func (c *Client) FileInfo(ctx context.Context, userid string, spaceID string, fileID string) (*WeDriveFile, error) {
	var result fileInfoResponse
	if err := c.postJSON(ctx, "/wedrive/file_info", map[string]string{
		"userid":  userid,
		"spaceid": spaceID,
		"fileid":  fileID,
	}, &result); err != nil {
		return nil, err
	}
	file := result.FileInfo
	if file.FileID == "" {
		file.FileID = fileID
	}
	if file.SpaceID == "" {
		file.SpaceID = spaceID
	}
	return &file, nil
}

func (c *Client) FileDownload(ctx context.Context, userid string, fileID string) (*fileDownloadResponse, error) {
	var result fileDownloadResponse
	if err := c.postJSON(ctx, "/wedrive/file_download", map[string]string{
		"userid": userid,
		"fileid": fileID,
	}, &result); err != nil {
		return nil, err
	}
	if result.DownloadURL == "" {
		return nil, fmt.Errorf("%w: file_download response missing download_url", datasource.ErrFetchFailed)
	}
	return &result, nil
}

func (c *Client) DownloadFileBytes(ctx context.Context, downloadURL, cookieName, cookieValue string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create download request: %s", redactURLSecrets(err.Error()))
	}
	if cookieName != "" && cookieValue != "" {
		req.AddCookie(&http.Cookie{Name: cookieName, Value: cookieValue})
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: download wecom file: %s", datasource.ErrFetchFailed, sanitizeHTTPError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: download http status=%d", datasource.ErrFetchFailed, resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read download response: %w", err)
	}
	return data, nil
}

func (c *Client) GetFilePermission(ctx context.Context, userid string, fileID string) (*filePermissionResponse, error) {
	var result filePermissionResponse
	if err := c.postJSON(ctx, "/wedrive/get_file_permission", map[string]string{
		"userid": userid,
		"fileid": fileID,
	}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) postJSON(ctx context.Context, endpoint string, body interface{}, result interface{}) error {
	token, err := c.GetToken(ctx)
	if err != nil {
		return err
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal %s request: %w", endpoint, err)
	}

	reqURL := c.baseURL + endpoint + "?access_token=" + url.QueryEscape(token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create %s request: %w", endpoint, err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute %s request: %s", endpoint, sanitizeHTTPError(err))
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read %s response: %w", endpoint, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("wecom wedrive api http error: endpoint=%s status=%d", endpoint, resp.StatusCode)
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(respBody, result); err != nil {
		return fmt.Errorf("decode %s response: %w", endpoint, err)
	}
	if withCode, ok := result.(interface {
		getTencentBaseResponse() tencentBaseResponse
	}); ok {
		base := withCode.getTencentBaseResponse()
		if base.ErrCode != 0 {
			return &TencentAPIError{Endpoint: endpoint, ErrCode: base.ErrCode, ErrMsg: base.ErrMsg}
		}
	}
	return nil
}

func (r *spaceInfoResponse) getTencentBaseResponse() tencentBaseResponse {
	return r.tencentBaseResponse
}

func (r *newSpaceInfoResponse) getTencentBaseResponse() tencentBaseResponse {
	return r.tencentBaseResponse
}

func (r *fileListResponse) getTencentBaseResponse() tencentBaseResponse {
	return r.tencentBaseResponse
}

func (r *fileInfoResponse) getTencentBaseResponse() tencentBaseResponse {
	return r.tencentBaseResponse
}

func (r *fileDownloadResponse) getTencentBaseResponse() tencentBaseResponse {
	return r.tencentBaseResponse
}

func (r *filePermissionResponse) getTencentBaseResponse() tencentBaseResponse {
	return r.tencentBaseResponse
}
