package yearning

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/toolsHelp/Yearning-sql-proxy/internal/config"
)

// 回归测试：Sources/Token 的锁重入死锁（曾导致 fatal deadlock）。
// 用一个假的 Yearning 服务器验证：首次无缓存时拉取数据源不挂起。
func TestSources_noDeadlock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ldap":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"payload": map[string]interface{}{"token": "fake-jwt"},
				"code":    1200,
			})
		case "/api/v2/fetch/source":
			if r.URL.Query().Get("tp") != "query" {
				http.Error(w, "bad tp", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"payload": []map[string]interface{}{
					{"source": "order_db", "source_id": "src-1"},
					{"source": "user_db", "source_id": "src-2"},
				},
				"code": 1200,
			})
		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cfg := &config.Config{Yearning: config.Yearning{
		BaseURL:       srv.URL,
		LoginUser:     "u",
		LoginPassword: "p",
		AuthMode:      "ldap",
		IsLDAP:        true,
	}}
	cfg.Timeout.Query = 5 * time.Second
	cfg.Timeout.Connect = 5 * time.Second

	c := NewClient(cfg)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = c.Sources() // 触发 Token() -> doJSON() 链路
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Sources() 死锁或超时（锁重入问题未修复）")
	}

	srcs, err := c.Sources()
	if err != nil {
		t.Fatalf("Sources 失败: %v", err)
	}
	if len(srcs) != 2 {
		t.Fatalf("数据源数量 = %d, 期望 2", len(srcs))
	}
	// 缓存命中后不应再触发网络请求。
	if id, err := c.ResolveSourceID("order_db"); err != nil || id != "src-1" {
		t.Fatalf("ResolveSourceID = %q, %v; 期望 src-1", id, err)
	}
}

// SchemaMap 应建立库名→数据源映射，且不把系统库算进去。
func TestSchemaMap_excludesSystemSchemas(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/ldap":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"payload": map[string]interface{}{"token": "fake-jwt"}, "code": 1200})
		case r.URL.Path == "/api/v2/fetch/source":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"payload": []map[string]interface{}{
					{"source": "s1", "source_id": "src-1"},
					{"source": "s2", "source_id": "src-2"},
				}, "code": 1200})
		case r.URL.Path == "/api/v2/query/schema":
			// s1 返回 1 个业务库 + information_schema；s2 只返回 information_schema + 自己的库
			payload := []map[string]interface{}{{"key": "information_schema"}}
			if r.URL.Query().Get("source_id") == "src-1" {
				payload = append(payload, map[string]interface{}{"key": "gaia_edi"})
			} else {
				payload = append(payload, map[string]interface{}{"key": "mysql"})
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"payload": payload, "code": 1200})
		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cfg := &config.Config{Yearning: config.Yearning{
		BaseURL: srv.URL, LoginUser: "u", LoginPassword: "p", AuthMode: "ldap", IsLDAP: true}}
	cfg.Timeout.Connect = 5 * time.Second

	c := NewClient(cfg)
	m, err := c.SchemaMap()
	if err != nil {
		t.Fatalf("SchemaMap 失败: %v", err)
	}
	if _, ok := m["information_schema"]; ok {
		t.Errorf("information_schema 不应进入库→数据源映射")
	}
	if _, ok := m["mysql"]; ok {
		t.Errorf("mysql 系统库不应进入库→数据源映射")
	}
	if m["gaia_edi"] != "src-1" {
		t.Errorf("gaia_edi 应映射到 src-1, got %q", m["gaia_edi"])
	}
	// 大小写不敏感查库
	if id, ok := c.SourceBySchema("GAIA_EDI"); !ok || id != "src-1" {
		t.Errorf("SourceBySchema 应大小写不敏感, got %q %v", id, ok)
	}
	if len(c.AllSourceIDs()) != 2 {
		t.Errorf("AllSourceIDs 应返回 2 个数据源, got %v", c.AllSourceIDs())
	}
}

// 登录失败时应透传 Yearning 的真实报错，而不是笼统的「未返回 token」。
func TestLogin_failureText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ldap" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code": 1301,
				"text": "账号或密码错误",
			})
			return
		}
		http.Error(w, "unexpected", http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := &config.Config{Yearning: config.Yearning{
		BaseURL:       srv.URL,
		LoginUser:     "u",
		LoginPassword: "wrong",
		AuthMode:      "ldap",
		IsLDAP:        true,
	}}
	cfg.Timeout.Connect = 5 * time.Second

	c := NewClient(cfg)
	_, err := c.Token()
	if err == nil {
		t.Fatal("登录失败时应返回错误")
	}
	if !strings.Contains(err.Error(), "账号或密码错误") {
		t.Fatalf("错误未透传 Yearning 文本: %v", err)
	}
}
