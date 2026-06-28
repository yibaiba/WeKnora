package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	defaultWeComAPIBaseURL = "https://qyapi.weixin.qq.com/cgi-bin"
	wecomHTTPTimeout       = 30 * time.Second
	wecomTokenMargin       = 5 * time.Minute
	defaultWeComRootDept   = "1"
)

type wecomContactClient struct {
	baseURL    string
	corpID     string
	secret     string
	httpClient *http.Client

	tokenMu    sync.Mutex
	tokenCache string
	tokenExpAt time.Time
}

type wecomContactClientOption func(*wecomContactClient)

func withWeComContactHTTPClient(client *http.Client) wecomContactClientOption {
	return func(c *wecomContactClient) {
		if client != nil {
			c.httpClient = client
		}
	}
}

func withWeComContactBaseURL(baseURL string) wecomContactClientOption {
	return func(c *wecomContactClient) {
		baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
		if baseURL != "" {
			c.baseURL = baseURL
		}
	}
}

func newWeComContactClient(corpID, secret string, opts ...wecomContactClientOption) *wecomContactClient {
	c := &wecomContactClient{
		baseURL:    defaultWeComAPIBaseURL,
		corpID:     strings.TrimSpace(corpID),
		secret:     strings.TrimSpace(secret),
		httpClient: &http.Client{Timeout: wecomHTTPTimeout},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *wecomContactClient) getToken(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	if c.tokenCache != "" && time.Now().Before(c.tokenExpAt) {
		return c.tokenCache, nil
	}
	reqURL := c.baseURL + "/gettoken?corpid=" + url.QueryEscape(c.corpID) +
		"&corpsecret=" + url.QueryEscape(c.secret)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "", fmt.Errorf("create wecom token request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request wecom token: %s", sanitizeWeComHTTPError(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read wecom token response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("wecom token http status=%d", resp.StatusCode)
	}
	var result wecomTokenResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("decode wecom token response: %w", err)
	}
	if result.ErrCode != 0 {
		return "", wecomAPIError{Endpoint: "/gettoken", ErrCode: result.ErrCode, ErrMsg: result.ErrMsg}
	}
	if strings.TrimSpace(result.AccessToken) == "" {
		return "", fmt.Errorf("wecom token response missing access_token")
	}
	c.tokenCache = result.AccessToken
	ttl := time.Duration(result.ExpiresIn) * time.Second
	if ttl > wecomTokenMargin {
		ttl -= wecomTokenMargin
	}
	if ttl <= 0 {
		ttl = time.Minute
	}
	c.tokenExpAt = time.Now().Add(ttl)
	return c.tokenCache, nil
}

func (c *wecomContactClient) departments(ctx context.Context) ([]wecomDepartmentPayload, error) {
	var result wecomDepartmentListResponse
	if err := c.getJSON(ctx, "/department/list", nil, &result); err != nil {
		return nil, err
	}
	return result.Department, nil
}

func (c *wecomContactClient) users(ctx context.Context, departmentID string, fetchChild bool) ([]wecomUserPayload, error) {
	query := url.Values{}
	query.Set("department_id", strings.TrimSpace(departmentID))
	if fetchChild {
		query.Set("fetch_child", "1")
	} else {
		query.Set("fetch_child", "0")
	}
	var result wecomUserListResponse
	if err := c.getJSON(ctx, "/user/list", query, &result); err != nil {
		return nil, err
	}
	return result.UserList, nil
}

func (c *wecomContactClient) getJSON(
	ctx context.Context, endpoint string, query url.Values, result interface{},
) error {
	token, err := c.getToken(ctx)
	if err != nil {
		return err
	}
	if query == nil {
		query = url.Values{}
	}
	query.Set("access_token", token)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return fmt.Errorf("create wecom %s request: %w", endpoint, err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request wecom %s: %s", endpoint, sanitizeWeComHTTPError(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read wecom %s response: %w", endpoint, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("wecom %s http status=%d", endpoint, resp.StatusCode)
	}
	if err := json.Unmarshal(body, result); err != nil {
		return fmt.Errorf("decode wecom %s response: %w", endpoint, err)
	}
	if err := checkWeComErrCode(endpoint, body); err != nil {
		return err
	}
	return nil
}

func checkWeComErrCode(endpoint string, body []byte) error {
	var base struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&base); err != nil {
		return err
	}
	if base.ErrCode != 0 {
		return wecomAPIError{Endpoint: endpoint, ErrCode: base.ErrCode, ErrMsg: base.ErrMsg}
	}
	return nil
}

type wecomTokenResponse struct {
	ErrCode     int    `json:"errcode"`
	ErrMsg      string `json:"errmsg"`
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
}

type wecomDepartmentListResponse struct {
	ErrCode    int                      `json:"errcode"`
	ErrMsg     string                   `json:"errmsg"`
	Department []wecomDepartmentPayload `json:"department"`
}

type wecomDepartmentPayload struct {
	ID       any    `json:"id"`
	Name     string `json:"name"`
	ParentID any    `json:"parentid"`
	Order    int64  `json:"order"`
}

type wecomUserListResponse struct {
	ErrCode  int                `json:"errcode"`
	ErrMsg   string             `json:"errmsg"`
	UserList []wecomUserPayload `json:"userlist"`
}

type wecomUserPayload struct {
	UserID     string `json:"userid"`
	Name       string `json:"name"`
	Department []any  `json:"department"`
	Email      string `json:"email"`
	Mobile     string `json:"mobile"`
	Avatar     string `json:"avatar"`
	Status     int    `json:"status"`
}

type wecomAPIError struct {
	Endpoint string
	ErrCode  int
	ErrMsg   string
}

func (e wecomAPIError) Error() string {
	msg := strings.TrimSpace(e.ErrMsg)
	if msg == "" {
		msg = "unknown"
	}
	return fmt.Sprintf("wecom api error: endpoint=%s errcode=%d errmsg=%s", e.Endpoint, e.ErrCode, msg)
}

var wecomSensitiveQueryPattern = regexp.MustCompile(`(?i)(corpsecret|access_token)=([^&\s]+)`)

func sanitizeWeComHTTPError(err error) string {
	if err == nil {
		return ""
	}
	return wecomSensitiveQueryPattern.ReplaceAllString(err.Error(), "$1=REDACTED")
}
