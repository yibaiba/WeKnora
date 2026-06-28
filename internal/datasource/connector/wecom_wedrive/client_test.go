package wecom_wedrive

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientGetTokenSuccessAndCache(t *testing.T) {
	tokenCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/gettoken" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		tokenCalls++
		if r.URL.Query().Get("corpid") != "ww123" || r.URL.Query().Get("corpsecret") != "secret" {
			t.Fatalf("unexpected token query: %s", r.URL.RawQuery)
		}
		writeJSON(t, w, map[string]interface{}{
			"errcode":      0,
			"errmsg":       "ok",
			"access_token": "token-1",
			"expires_in":   7200,
		})
	}))
	defer server.Close()

	client := NewClient(testConfig(), WithBaseURL(server.URL))
	for i := 0; i < 2; i++ {
		token, err := client.GetToken(testContext(t))
		if err != nil {
			t.Fatalf("GetToken() error = %v", err)
		}
		if token != "token-1" {
			t.Fatalf("token = %q", token)
		}
	}
	if tokenCalls != 1 {
		t.Fatalf("token calls = %d, want 1", tokenCalls)
	}
}

func TestClientGetTokenErrcodeIsSanitized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]interface{}{
			"errcode": 40014,
			"errmsg":  "invalid secret",
		})
	}))
	defer server.Close()

	_, err := NewClient(testConfig(), WithBaseURL(server.URL)).GetToken(testContext(t))
	if err == nil {
		t.Fatal("GetToken() expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "errcode=40014") || strings.Contains(msg, "secret") && strings.Contains(msg, "corpsecret=") {
		t.Fatalf("unexpected error message: %s", msg)
	}
}

func TestRedactURLSecretsRemovesSensitiveQueryValues(t *testing.T) {
	raw := "Get \"https://qyapi.weixin.qq.com/cgi-bin/gettoken?corpid=ww123&corpsecret=real-secret&access_token=token-1&cookie_value=cookie-secret\": error"
	got := redactURLSecrets(raw)
	for _, secret := range []string{"real-secret", "token-1", "cookie-secret"} {
		if strings.Contains(got, secret) {
			t.Fatalf("redacted error still contains %q: %s", secret, got)
		}
	}
	for _, want := range []string{"corpsecret=REDACTED", "access_token=REDACTED", "cookie_value=REDACTED"} {
		if !strings.Contains(got, want) {
			t.Fatalf("redacted error = %s, want %s", got, want)
		}
	}
}

func TestClientGetTokenRejectsMissingAccessToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]interface{}{"errcode": 0, "expires_in": 7200})
	}))
	defer server.Close()

	_, err := NewClient(testConfig(), WithBaseURL(server.URL)).GetToken(testContext(t))
	if err == nil || !strings.Contains(err.Error(), "missing access_token") {
		t.Fatalf("GetToken() error = %v, want missing access_token", err)
	}
}

func TestClientFileListPaginationPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gettoken":
			writeJSON(t, w, map[string]interface{}{
				"errcode":      0,
				"access_token": "token-1",
				"expires_in":   7200,
			})
		case "/wedrive/file_list":
			if r.URL.Query().Get("access_token") != "token-1" {
				t.Fatalf("missing access token in query")
			}
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			assertBodyString(t, body, "userid", "sync-user")
			assertBodyString(t, body, "spaceid", "space-1")
			assertBodyString(t, body, "fatherid", "folder-1")
			if body["start"].(float64) != 20 || body["limit"].(float64) != 50 {
				t.Fatalf("unexpected pagination body: %#v", body)
			}
			writeJSON(t, w, map[string]interface{}{
				"errcode":    0,
				"has_more":   true,
				"next_start": "40",
				"file_list": map[string]interface{}{
					"item": []map[string]interface{}{
						{"fileid": "file-1", "file_name": "Plan.pdf", "file_type": 2, "mtime": "1700000000"},
					},
				},
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	files, hasMore, nextStart, err := NewClient(testConfig(), WithBaseURL(server.URL)).
		FileList(testContext(t), "sync-user", "space-1", "folder-1", 20, 50)
	if err != nil {
		t.Fatalf("FileList() error = %v", err)
	}
	if !hasMore || nextStart != 40 || len(files) != 1 || files[0].FileID != "file-1" {
		t.Fatalf("FileList() = files=%#v hasMore=%v next=%d", files, hasMore, nextStart)
	}
}

func TestClientMalformedJSONAndTencentErrcode(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "malformed", body: `{`, want: "decode /wedrive/file_list response"},
		{name: "errcode", body: `{"errcode":60001,"errmsg":"no permission"}`, want: "errcode=60001"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/gettoken" {
					writeJSON(t, w, map[string]interface{}{"errcode": 0, "access_token": "token-1", "expires_in": 7200})
					return
				}
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			_, _, _, err := NewClient(testConfig(), WithBaseURL(server.URL)).
				FileList(testContext(t), "sync-user", "space-1", "", 0, 10)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("FileList() error = %v, want contains %q", err, tt.want)
			}
		})
	}
}

func TestClientDownloadUsesReturnedCookie(t *testing.T) {
	var downloadCookie string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gettoken":
			writeJSON(t, w, map[string]interface{}{"errcode": 0, "access_token": "token-1", "expires_in": 7200})
		case "/wedrive/file_download":
			writeJSON(t, w, map[string]interface{}{
				"errcode":      0,
				"download_url": "http://" + r.Host + "/download/file-1",
				"cookie_name":  "wedrive_ticket",
				"cookie_value": "cookie-secret",
			})
		case "/download/file-1":
			cookie, err := r.Cookie("wedrive_ticket")
			if err == nil {
				downloadCookie = cookie.Value
			}
			_, _ = w.Write([]byte("file-bytes"))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient(testConfig(), WithBaseURL(server.URL))
	link, err := client.FileDownload(testContext(t), "sync-user", "file-1")
	if err != nil {
		t.Fatalf("FileDownload() error = %v", err)
	}
	data, err := client.DownloadFileBytes(testContext(t), link.DownloadURL, link.CookieName, link.CookieValue)
	if err != nil {
		t.Fatalf("DownloadFileBytes() error = %v", err)
	}
	if string(data) != "file-bytes" || downloadCookie != "cookie-secret" {
		t.Fatalf("download data=%q cookie=%q", data, downloadCookie)
	}
}

func TestClientFileDownloadRejectsMissingDownloadURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gettoken":
			writeJSON(t, w, map[string]interface{}{"errcode": 0, "access_token": "token-1", "expires_in": 7200})
		case "/wedrive/file_download":
			writeJSON(t, w, map[string]interface{}{"errcode": 0})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	_, err := NewClient(testConfig(), WithBaseURL(server.URL)).FileDownload(testContext(t), "sync-user", "file-1")
	if err == nil || !strings.Contains(err.Error(), "missing download_url") {
		t.Fatalf("FileDownload() error = %v, want missing download_url", err)
	}
}

func testConfig() *Config {
	return &Config{CorpID: "ww123", Secret: "secret"}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value interface{}) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode json: %v", err)
	}
}

func assertBodyString(t *testing.T, body map[string]interface{}, key string, want string) {
	t.Helper()
	got, ok := body[key].(string)
	if !ok || got != want {
		t.Fatalf("body[%s] = %#v, want %q", key, body[key], want)
	}
}
