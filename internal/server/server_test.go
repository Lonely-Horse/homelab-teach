package server_test

import (
	"bytes"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"homelab-teach/internal/config"
	"homelab-teach/internal/db"
	"homelab-teach/internal/server"
)

const testPassword = "test-password-1234567890"

type testEnv struct {
	t      *testing.T
	base   string
	client *http.Client
	store  *db.Store
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "app.db")

	salt := []byte("0123456789abcdef0123456789abcdef")
	hash, err := pbkdf2.Key(sha256.New, testPassword, salt, 1000, 32)
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("0123456789abcdef0123456789abcdef")

	cfg := &config.Config{
		Addr:         "127.0.0.1:0",
		Origin:       "http://app.test",
		DBPath:       dbPath,
		DevMode:      true,
		AdminSaltB64: base64.StdEncoding.EncodeToString(salt),
		AdminHashB64: base64.StdEncoding.EncodeToString(hash),
		AdminSalt:    salt,
		AdminHash:    hash,
		AdminIters:   1000,
		PwdSecret:    secret,
	}

	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := server.New(cfg, store, os.DirFS("../../web"), logger).Handler()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	return &testEnv{t: t, base: ts.URL, client: client, store: store}
}

func (e *testEnv) request(method, path string, body io.Reader, headers map[string]string) *http.Response {
	e.t.Helper()
	req, err := http.NewRequest(method, e.base+path, body)
	if err != nil {
		e.t.Fatal(err)
	}
	for k, v := range headers {
		if strings.EqualFold(k, "Host") {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func (e *testEnv) login(password, ip string) *http.Response {
	e.t.Helper()
	form := url.Values{"password": {password}}
	return e.request(http.MethodPost, "/admin/login", strings.NewReader(form.Encode()), map[string]string{
		"Content-Type":    "application/x-www-form-urlencoded",
		"X-Forwarded-For": ip,
	})
}

func decode(t *testing.T, resp *http.Response, dst any) {
	t.Helper()
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type filePart struct {
	name    string
	content string
}

func multipartBody(t *testing.T, fields map[string]string, files []filePart) (io.Reader, string) {
	t.Helper()
	return multipartBodyField(t, "files", fields, files)
}

func multipartBodyField(t *testing.T, field string, fields map[string]string, files []filePart) (io.Reader, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range files {
		fw, err := w.CreateFormFile(field, f.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(fw, f.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, w.FormDataContentType()
}

func TestEndToEnd(t *testing.T) {
	e := newTestEnv(t)

	// 迁移结果
	var version int
	if err := e.store.DB.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("user_version = %d, want 1", version)
	}

	// 健康检查
	resp := e.request(http.MethodGet, "/healthz", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("healthz = %d", resp.StatusCode)
	}

	// 未登录 401
	resp = e.request(http.MethodPost, "/admin/api/folders", strings.NewReader(`{"parent_id":1,"name":"x"}`), nil)
	if resp.StatusCode != 401 {
		t.Fatalf("unauthorized = %d, want 401", resp.StatusCode)
	}

	// 登录
	resp = e.login(testPassword, "198.51.100.1")
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/admin/" {
		t.Fatalf("login = %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	// Origin 与请求 host 一致即可登录（不要求等于写死的 siteOrigin）
	form := url.Values{"password": {testPassword}}
	resp = e.request(http.MethodPost, "/admin/login", strings.NewReader(form.Encode()), map[string]string{
		"Content-Type":    "application/x-www-form-urlencoded",
		"Origin":          e.base,
		"X-Forwarded-For": "198.51.100.2",
	})
	if resp.StatusCode != 303 {
		t.Fatalf("same-host origin login = %d, want 303", resp.StatusCode)
	}

	// 跨站 Origin 仍然拒绝
	resp = e.request(http.MethodPost, "/admin/login", strings.NewReader(form.Encode()), map[string]string{
		"Content-Type":    "application/x-www-form-urlencoded",
		"Origin":          "http://evil.test",
		"X-Forwarded-For": "198.51.100.3",
	})
	if resp.StatusCode != 403 {
		t.Fatalf("cross-site origin login = %d, want 403", resp.StatusCode)
	}

	// 建文件夹
	resp = e.request(http.MethodPost, "/admin/api/folders", strings.NewReader(`{"parent_id":1,"name":"数据结构"}`), map[string]string{
		"Content-Type": "application/json",
	})
	if resp.StatusCode != 201 {
		t.Fatalf("create folder = %d %s", resp.StatusCode, readAll(t, resp))
	}
	var f1 struct {
		ID int64 `json:"id"`
	}
	decode(t, resp, &f1)

	resp = e.request(http.MethodPost, "/admin/api/folders", strings.NewReader(`{"parent_id":`+itoa(f1.ID)+`,"name":"链表"}`), map[string]string{
		"Content-Type": "application/json",
	})
	if resp.StatusCode != 201 {
		t.Fatalf("create subfolder = %d %s", resp.StatusCode, readAll(t, resp))
	}
	var f2 struct {
		ID int64 `json:"id"`
	}
	decode(t, resp, &f2)

	// 环检测
	resp = e.request(http.MethodPatch, "/admin/api/folders/"+itoa(f1.ID), strings.NewReader(`{"parent_id":`+itoa(f2.ID)+`}`), map[string]string{
		"Content-Type": "application/json",
	})
	if resp.StatusCode != 400 {
		t.Fatalf("cycle = %d, want 400", resp.StatusCode)
	}

	// 设为公开
	resp = e.request(http.MethodPatch, "/admin/api/folders/"+itoa(f1.ID), strings.NewReader(`{"is_public":1}`), map[string]string{
		"Content-Type": "application/json",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("set public = %d %s", resp.StatusCode, readAll(t, resp))
	}

	// 上传
	html := `<!doctype html><html><head><title>第 1 课 · 单链表</title></head>
<body><script>alert('x')</script><p>单链表的每个节点只保存指向下一个节点的指针。</p></body></html>`
	body, ct := multipartBodyField(t, "file", map[string]string{"folder_id": itoa(f2.ID)}, []filePart{{"lesson1.html", html}})
	resp = e.request(http.MethodPost, "/admin/api/pages", body, map[string]string{"Content-Type": ct})
	if resp.StatusCode != 201 {
		t.Fatalf("upload = %d %s", resp.StatusCode, readAll(t, resp))
	}
	var up struct {
		Status   string `json:"status"`
		ID       string `json:"id"`
		Filename string `json:"filename"`
		Title    string `json:"title"`
	}
	decode(t, resp, &up)
	if up.Status != "created" || up.Title != "第 1 课 · 单链表" || up.Filename != "lesson1.html" {
		t.Fatalf("upload response = %+v", up)
	}

	// 重传同内容 -> skipped
	body, ct = multipartBodyField(t, "file", map[string]string{"folder_id": itoa(f2.ID)}, []filePart{{"lesson1.html", html}})
	resp = e.request(http.MethodPost, "/admin/api/pages", body, map[string]string{"Content-Type": ct})
	var up2 struct {
		Status string `json:"status"`
	}
	decode(t, resp, &up2)
	if up2.Status != "skipped" {
		t.Fatalf("reupload status = %q", up2.Status)
	}

	// 公开树
	resp = e.request(http.MethodGet, "/api/tree", nil, nil)
	var tree struct {
		Root    int64 `json:"root"`
		Folders []struct {
			ID int64 `json:"id"`
		} `json:"folders"`
	}
	decode(t, resp, &tree)
	if tree.Root != 1 || len(tree.Folders) != 2 {
		t.Fatalf("public tree = %+v", tree)
	}

	// 公开课件列表
	resp = e.request(http.MethodGet, "/api/pages?folder_id="+itoa(f2.ID), nil, nil)
	var pages struct {
		Items []struct {
			Filename string `json:"filename"`
		} `json:"items"`
	}
	decode(t, resp, &pages)
	if len(pages.Items) != 1 || pages.Items[0].Filename != "lesson1.html" {
		t.Fatalf("public pages = %+v", pages)
	}

	// 搜索命中正文
	resp = e.request(http.MethodGet, "/api/search?q="+url.QueryEscape("单链表"), nil, nil)
	var sr struct {
		Count int `json:"count"`
	}
	decode(t, resp, &sr)
	if sr.Count != 1 {
		t.Fatalf("search count = %d", sr.Count)
	}

	// % 被转义，不应命中全表
	resp = e.request(http.MethodGet, "/api/search?q=%25", nil, nil)
	decode(t, resp, &sr)
	if sr.Count != 0 {
		t.Fatalf("search %% count = %d, want 0", sr.Count)
	}

	// 课件域
	cw := e.request(http.MethodGet, "/f/"+itoa(f2.ID)+"/lesson1.html", nil, nil)
	if cw.StatusCode != 200 {
		t.Fatalf("courseware = %d", cw.StatusCode)
	}
	if got := cw.Header.Get("Content-Security-Policy"); got != "frame-ancestors 'self'" {
		t.Fatalf("courseware CSP = %q", got)
	}
	if got := readAll(t, cw); got != html {
		t.Fatalf("courseware body changed")
	}

	// 路径穿越
	for _, p := range []string{"/f/" + itoa(f2.ID) + "/../lesson1.html", "/f/abc/lesson1.html"} {
		r := e.request(http.MethodGet, p, nil, nil)
		if r.StatusCode != 404 {
			t.Fatalf("%s = %d, want 404", p, r.StatusCode)
		}
	}

	// 私有文件夹 + 签名预览
	resp = e.request(http.MethodPost, "/admin/api/folders", strings.NewReader(`{"parent_id":1,"name":"草稿"}`), map[string]string{
		"Content-Type": "application/json",
	})
	var priv struct {
		ID int64 `json:"id"`
	}
	decode(t, resp, &priv)
	body, ct = multipartBodyField(t, "file", map[string]string{"folder_id": itoa(priv.ID)}, []filePart{{"secret.html", html}})
	resp = e.request(http.MethodPost, "/admin/api/pages", body, map[string]string{"Content-Type": ct})
	var privPage struct {
		ID string `json:"id"`
	}
	decode(t, resp, &privPage)

	cw = e.request(http.MethodGet, "/f/"+itoa(priv.ID)+"/secret.html", nil, nil)
	if cw.StatusCode != 404 {
		t.Fatalf("private courseware = %d, want 404", cw.StatusCode)
	}

	resp = e.request(http.MethodGet, "/admin/api/pages/"+privPage.ID+"/preview", nil, nil)
	var preview struct {
		URL string `json:"url"`
	}
	decode(t, resp, &preview)
	u, err := url.Parse(preview.URL)
	if err != nil {
		t.Fatal(err)
	}
	cw = e.request(http.MethodGet, u.RequestURI(), nil, nil)
	if cw.StatusCode != 200 {
		t.Fatalf("signed courseware = %d", cw.StatusCode)
	}
	// 篡改签名
	q := u.Query()
	q.Set("t", strings.Repeat("0", 64))
	cw = e.request(http.MethodGet, u.Path+"?"+q.Encode(), nil, nil)
	if cw.StatusCode != 404 {
		t.Fatalf("tampered signature = %d, want 404", cw.StatusCode)
	}

	// 后台预览跳转（<a target=_blank> 用，避免 window.open 被拦截）
	resp = e.request(http.MethodGet, "/admin/preview/"+privPage.ID, nil, nil)
	if resp.StatusCode != 303 {
		t.Fatalf("preview redirect = %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/f/"+itoa(priv.ID)+"/secret.html?e=") {
		t.Fatalf("preview redirect location = %q", loc)
	}

	// 非空文件夹删除 -> 409
	resp = e.request(http.MethodDelete, "/admin/api/folders/"+itoa(f1.ID), nil, nil)
	if resp.StatusCode != 409 {
		t.Fatalf("delete non-empty = %d, want 409", resp.StatusCode)
	}

	// 递归删除：连同子文件夹与课件一起删
	resp = e.request(http.MethodDelete, "/admin/api/folders/"+itoa(f1.ID)+"?recursive=1", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("recursive delete = %d %s", resp.StatusCode, readAll(t, resp))
	}
	var del struct {
		Folders int `json:"deleted_folders"`
		Pages   int `json:"deleted_pages"`
	}
	decode(t, resp, &del)
	if del.Folders != 2 || del.Pages != 1 {
		t.Fatalf("recursive delete = %+v", del)
	}
	resp = e.request(http.MethodGet, "/admin/api/tree", nil, nil)
	var after struct {
		Folders []struct {
			ID int64 `json:"id"`
		} `json:"folders"`
	}
	decode(t, resp, &after)
	for _, f := range after.Folders {
		if f.ID == f1.ID || f.ID == f2.ID {
			t.Fatalf("folder %d still exists after recursive delete", f.ID)
		}
	}

	// 管理端列表（含私有）
	resp = e.request(http.MethodGet, "/admin/api/pages?folder_id="+itoa(priv.ID), nil, nil)
	var adminPages struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	decode(t, resp, &adminPages)
	if len(adminPages.Items) != 1 {
		t.Fatalf("admin pages = %+v", adminPages)
	}

	// 导入
	importHTML := `<!doctype html><html><head><title>导入课件</title><meta name="lesson" content="2"></head><body>批量导入正文</body></html>`
	body, ct = multipartBody(t, map[string]string{"target_folder_id": "1"}, []filePart{
		{"导入课程/第一章/lesson1.html", importHTML},
		{"导入课程/第一章/lesson2.html", strings.ReplaceAll(importHTML, "导入课件", "导入课件 2")},
		{"style.css", "body{}"},
		{"../evil.html", importHTML},
	})
	resp = e.request(http.MethodPost, "/admin/api/import", body, map[string]string{"Content-Type": ct})
	if resp.StatusCode != 200 {
		t.Fatalf("import = %d %s", resp.StatusCode, readAll(t, resp))
	}
	var ir struct {
		Created int      `json:"created"`
		Updated int      `json:"updated"`
		Skipped int      `json:"skipped"`
		Ignored []string `json:"ignored"`
		Errors  []struct {
			Path   string `json:"path"`
			Reason string `json:"reason"`
		} `json:"errors"`
		FoldersCreated []string `json:"folders_created"`
	}
	decode(t, resp, &ir)
	if ir.Created != 2 || len(ir.Ignored) != 1 || len(ir.Errors) != 1 || len(ir.FoldersCreated) != 2 {
		t.Fatalf("import result = %+v", ir)
	}

	// 备份
	resp = e.request(http.MethodPost, "/admin/api/backup", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("backup = %d %s", resp.StatusCode, readAll(t, resp))
	}
	var bk struct {
		File string `json:"file"`
		Size int64  `json:"size"`
	}
	decode(t, resp, &bk)
	if _, err := os.Stat(filepath.Join(dirOf(e.store), bk.File)); err != nil {
		t.Fatalf("backup file: %v", err)
	}

	// 会话列表
	resp = e.request(http.MethodGet, "/admin/api/sessions", nil, nil)
	var sessions struct {
		Items []struct {
			Current bool `json:"current"`
		} `json:"items"`
	}
	decode(t, resp, &sessions)
	current := 0
	for _, it := range sessions.Items {
		if it.Current {
			current++
		}
	}
	// 前面登录了两次（正常登录 + 同源 Origin 登录），只应有 1 个当前会话
	if len(sessions.Items) != 2 || current != 1 {
		t.Fatalf("sessions = %+v", sessions)
	}

	// 登出
	resp = e.request(http.MethodPost, "/admin/logout", nil, nil)
	if resp.StatusCode != 204 {
		t.Fatalf("logout = %d", resp.StatusCode)
	}
	resp = e.request(http.MethodGet, "/admin/api/tree", nil, nil)
	if resp.StatusCode != 401 {
		t.Fatalf("after logout = %d, want 401", resp.StatusCode)
	}

	// 方法绊线
	resp = e.request(http.MethodPost, "/api/tree", nil, nil)
	if resp.StatusCode != 405 {
		t.Fatalf("method guard = %d, want 405", resp.StatusCode)
	}

	// 登录限流：5 次失败后第 6 次 429
	for i := 0; i < 5; i++ {
		r := e.login("wrong", "203.0.113.9")
		if r.StatusCode != 303 {
			t.Fatalf("failure %d = %d", i+1, r.StatusCode)
		}
	}
	r := e.login("wrong", "203.0.113.9")
	if r.StatusCode != 429 {
		t.Fatalf("rate limit = %d, want 429", r.StatusCode)
	}
	if r.Header.Get("Retry-After") == "" {
		t.Fatal("missing Retry-After")
	}
}

func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}

func dirOf(store *db.Store) string {
	var path string
	_ = store.DB.QueryRow("PRAGMA database_list").Scan(new(int), new(string), &path)
	return filepath.Dir(path)
}
