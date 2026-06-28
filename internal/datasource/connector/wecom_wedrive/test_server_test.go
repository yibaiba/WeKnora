package wecom_wedrive

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newFakeWeDriveServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gettoken":
			writeJSON(t, w, map[string]interface{}{"errcode": 0, "access_token": "token-1", "expires_in": 7200})
		case "/wedrive/space_info":
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode space_info request: %v", err)
			}
			writeJSON(t, w, map[string]interface{}{
				"errcode": 0,
				"space_info": map[string]interface{}{
					"spaceid":    body["spaceid"],
					"space_name": "Engineering",
				},
			})
		case "/wedrive/file_list":
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode file_list request: %v", err)
			}
			spaceID, _ := body["spaceid"].(string)
			fatherID, _ := body["fatherid"].(string)
			items := []map[string]interface{}{
				{"fileid": "folder-1", "file_name": "Projects", "spaceid": spaceID, "file_type": 1, "mtime": 1700000000},
				{"fileid": "file-1", "file_name": "Readme.md", "spaceid": spaceID, "file_type": 2, "mtime": 1700000001},
			}
			if fatherID == "folder-1" {
				items = []map[string]interface{}{
					{"fileid": "file-2", "file_name": "Spec.docx", "spaceid": spaceID, "fatherid": fatherID, "file_type": 2, "mtime": 1700000002},
				}
			}
			writeJSON(t, w, map[string]interface{}{
				"errcode":   0,
				"has_more":  false,
				"file_list": map[string]interface{}{"item": items},
			})
		case "/wedrive/file_info":
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode file_info request: %v", err)
			}
			fileID, _ := body["fileid"].(string)
			info := map[string]interface{}{"fileid": fileID, "file_name": fileID, "spaceid": body["spaceid"], "file_type": 2}
			if fileID == "file-2" {
				info["fatherid"] = "folder-1"
			}
			if fileID == "folder-1" {
				info["fatherid"] = ""
				info["file_type"] = 1
			}
			writeJSON(t, w, map[string]interface{}{"errcode": 0, "file_info": info})
		case "/wedrive/file_download":
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode file_download request: %v", err)
			}
			fileID, _ := body["fileid"].(string)
			writeJSON(t, w, map[string]interface{}{
				"errcode":      0,
				"download_url": "http://" + r.Host + "/download/" + fileID,
				"cookie_name":  "wedrive_session",
				"cookie_value": "cookie-secret",
			})
		case "/wedrive/get_file_permission":
			writeJSON(t, w, map[string]interface{}{
				"errcode": 0,
				"share_range": map[string]interface{}{
					"enable_corp_internal": true,
					"corp_internal_auth":   1,
				},
				"file_member_list": []map[string]interface{}{
					{"userid": "wx-a", "auth": 1},
					{"type": 2, "userid": 42, "auth": 1},
				},
			})
		case "/download/file-1":
			_, _ = w.Write([]byte("# Readme"))
		case "/download/file-2":
			_, _ = w.Write([]byte("docx bytes"))
		default:
			t.Fatalf("unexpected fake WeDrive path %s", r.URL.Path)
		}
	}))
}
