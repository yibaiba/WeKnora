package wecom_wedrive

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var sensitiveQueryPattern = regexp.MustCompile(`(?i)(corpsecret|access_token|cookie_value|download_url)=([^&\s]+)`)

// TencentAPIError is safe to surface to users. It carries endpoint and Tencent
// errcode context, but never request credentials, access tokens, cookies, or
// temporary download URLs.
type TencentAPIError struct {
	Endpoint string
	ErrCode  int
	ErrMsg   string
}

func (e *TencentAPIError) Error() string {
	if e == nil {
		return "wecom wedrive api error"
	}
	msg := strings.TrimSpace(e.ErrMsg)
	if msg == "" {
		msg = "unknown"
	}
	return fmt.Sprintf("wecom wedrive api error: endpoint=%s errcode=%d errmsg=%s", e.Endpoint, e.ErrCode, msg)
}

func sanitizeHTTPError(err error) string {
	if err == nil {
		return ""
	}
	return redactURLSecrets(err.Error())
}

func redactURLSecrets(raw string) string {
	redacted := sensitiveQueryPattern.ReplaceAllString(raw, "$1=REDACTED")

	if u, err := url.Parse(redacted); err == nil && u.RawQuery != "" {
		q := u.Query()
		for _, key := range []string{"corpsecret", "access_token", "cookie_value", "download_url"} {
			if q.Has(key) {
				q.Set(key, "REDACTED")
			}
		}
		u.RawQuery = q.Encode()
		return u.String()
	}
	return redacted
}
