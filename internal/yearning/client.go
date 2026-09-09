// Package yearning 封装对 Yearning 后端的 HTTP/WebSocket 调用：
// 登录换 JWT、拉取数据源/库列表、建立查询 WebSocket 并执行 SQL。
package yearning

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/toolsHelp/Yearning-sql-proxy/internal/config"
	"github.com/toolsHelp/Yearning-sql-proxy/internal/msgpack"
)

// Client 是 Yearning 后端的 HTTP 客户端，缓存 JWT 与数据源/库列表。
type Client struct {
	cfg            *config.Config
	hc             *http.Client
	token          string
	mu             sync.RWMutex
	source         map[string]string // source 名 -> source_id
	schemaToSource map[string]string // schema 名 -> source_id（跨 source 汇总）
}

// Source 描述一个可用于查询的数据源。
type Source struct {
	Name string // source 字段
	ID   string // source_id 字段
}

// NewClient 构造客户端。
func NewClient(cfg *config.Config) *Client {
	return &Client{
		cfg: cfg,
		hc: &http.Client{
			Timeout: cfg.Timeout.Connect,
		},
		source: make(map[string]string),
	}
}

// loginResp 是 Yearning 登录响应的最小结构。
type loginResp struct {
	Payload struct {
		Token string `json:"token"`
	} `json:"payload"`
	Code int    `json:"code"`
	Text string `json:"text"`
}

// login 调登录接口换取 JWT（调用前需持有 c.mu 写锁）。
func (c *Client) login() (string, error) {
	body, err := json.Marshal(c.cfg.LoginBody())
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodPost, c.cfg.BaseURL()+c.cfg.LoginPath(), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("登录请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("登录返回 HTTP %d", resp.StatusCode)
	}
	var lr loginResp
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return "", fmt.Errorf("解析登录响应失败: %w", err)
	}
	// Yearning 成功码为 1200；失败时返回 {code, text} 且无 payload.token。
	if lr.Code != 0 && lr.Code != 1200 {
		msg := lr.Text
		if msg == "" {
			msg = fmt.Sprintf("code=%d", lr.Code)
		}
		return "", fmt.Errorf("Yearning 登录失败: %s", msg)
	}
	if lr.Payload.Token == "" {
		msg := lr.Text
		if msg == "" {
			msg = "响应中无 token"
		}
		return "", fmt.Errorf("Yearning 登录失败（未取得 token）: %s", msg)
	}
	return lr.Payload.Token, nil
}

// Token 返回当前 JWT，必要时刷新。失败时返回错误。
func (c *Client) Token() (string, error) {
	c.mu.RLock()
	t := c.token
	c.mu.RUnlock()
	if t != "" {
		return t, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" {
		return c.token, nil
	}
	t, err := c.login()
	if err != nil {
		return "", err
	}
	c.token = t
	return t, nil
}

// ClearToken 在收到未授权后清除缓存 token，强制下次重登。
func (c *Client) ClearToken() {
	c.mu.Lock()
	c.token = ""
	c.mu.Unlock()
}

// doJSON 执行带 Bearer token 的 GET 并解析 JSON 到 v。
func (c *Client) doJSON(path string, v interface{}) error {
	token, err := c.Token()
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodGet, c.cfg.BaseURL()+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("请求 %s 失败: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		c.ClearToken()
		return fmt.Errorf("请求 %s 返回 HTTP %d（鉴权失败，请重试）", path, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("请求 %s 返回 HTTP %d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return fmt.Errorf("解析 %s 响应失败: %w", path, err)
	}
	return nil
}

// enumPayload 是 fetch.source / query.schema 等接口的 payload 包装。
type enumPayload struct {
	Payload []map[string]interface{} `json:"payload"`
	Code    int                      `json:"code"`
}

// EnsureQueryOrder 调 POST /api/v2/query/post 创建/续期查询工单。
// 对应 Yearning 网页端「查询」前的前置动作：
//   - 查询审核关闭时自动生成 status=2（已批准）工单；
//   - 审核开启时生成 status=1 待审工单并通知审批人。
func (c *Client) EnsureQueryOrder(sourceID string) error {
	body, _ := json.Marshal(map[string]interface{}{
		"source_id": sourceID,
		"export":    0,
	})
	token, err := c.Token()
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, c.cfg.BaseURL()+"/api/v2/query/post", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("提交查询工单失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		c.ClearToken()
		return fmt.Errorf("提交查询工单返回 HTTP %d", resp.StatusCode)
	}
	var gr struct {
		Code int    `json:"code"`
		Text string `json:"text"`
	}
	// 注意：审核关闭时 ReferQueryOrder 直接 return，返回空 body（工单已创建成功）。
	// 空 body 应视为成功。
	if err := json.NewDecoder(resp.Body).Decode(&gr); err != nil {
		log.Printf("提交查询工单返回空 body（审核关闭时正常，工单已创建）: %v", err)
		return nil
	}
	log.Printf("提交查询工单: code=%d text=%q", gr.Code, gr.Text)
	return nil
}

// Sources 拉取并缓存可查询的数据源列表（source 名 -> source_id）。
func (c *Client) Sources() ([]Source, error) {
	c.mu.RLock()
	if len(c.source) > 0 {
		srcs := c.sourcesLocked()
		c.mu.RUnlock()
		return srcs, nil
	}
	c.mu.RUnlock()

	// 注意：网络请求期间不能持有 c.mu（Token/doJSON 内部会再次加锁，
	// RWMutex 不可重入，持写锁再读锁会死锁）。
	var p enumPayload
	if err := c.doJSON("/api/v2/fetch/source?tp=query", &p); err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.source) == 0 {
		for _, it := range p.Payload {
			name, _ := it["source"].(string)
			id, _ := it["source_id"].(string)
			if name != "" && id != "" {
				c.source[name] = id
			}
		}
	}
	return c.sourcesLocked(), nil
}

func (c *Client) sourcesLocked() []Source {
	out := make([]Source, 0, len(c.source))
	for name, id := range c.source {
		out = append(out, Source{Name: name, ID: id})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ResolveSourceID 把 DataGrip 的 user（Yearning 数据源名 source）解析为 source_id。
func (c *Client) ResolveSourceID(user string) (string, error) {
	srcs, err := c.Sources()
	if err != nil {
		return "", err
	}
	for _, s := range srcs {
		if s.Name == user {
			return s.ID, nil
		}
	}
	return "", fmt.Errorf("未知数据源: %s", user)
}

// Schemas 拉取某数据源下的库名列表并按 year.client 字典序返回。
func (c *Client) Schemas(sourceID string) ([]string, error) {
	var p enumPayload
	if err := c.doJSON(fmt.Sprintf("/api/v2/query/schema?source_id=%s", sourceID), &p); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, it := range p.Payload {
		key, _ := it["key"].(string)
		if key == "" {
			continue
		}
		if !seen[key] {
			seen[key] = true
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out, nil
}

// SchemaMap 建立「库名(schema) → source_id」的全局映射，供一个连接内跨 source 汇总。
// 懒加载：遍历所有可查询 source，为每个 source 拉取其 schema 列表。
// 返回去重后的全量库名列表，映射结果存入 c.schemaToSource。
func (c *Client) SchemaMap() (map[string]string, error) {
	c.mu.RLock()
	if len(c.schemaToSource) > 0 {
		m := cloneMap(c.schemaToSource)
		c.mu.RUnlock()
		return m, nil
	}
	c.mu.RUnlock()

	srcs, err := c.Sources()
	if err != nil {
		return nil, err
	}
	m := make(map[string]string)
	for _, s := range srcs {
		schemas, err := c.Schemas(s.ID)
		if err != nil {
			// 单个 source 拉取失败不阻塞整体，记录后继续。
			log.Printf("拉取数据源 %s 的库列表失败: %v", s.Name, err)
			continue
		}
		for _, schema := range schemas {
			// 同名库多个 source 时，保留第一个（可按需改成 source 名优先）。
			if _, exists := m[schema]; !exists {
				m[schema] = s.ID
			}
		}
	}
	c.mu.Lock()
	c.schemaToSource = m
	c.mu.Unlock()
	return cloneMap(m), nil
}

// SourceBySchema 返回某个库名对应的 source_id。
func (c *Client) SourceBySchema(schema string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	id, ok := c.schemaToSource[schema]
	return id, ok
}

func cloneMap(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// QuerySession 是到 Yearning 查询 WebSocket 的一个会话。
type QuerySession struct {
	client *Client
	cfg    *config.Config
	srcID  string
	conn   *gorillaConn
}

// NewQuerySession 建立到 /api/v2/query/results?source_id=... 的 WebSocket，
// 认证通过 Sec-WebSocket-Protocol 携带 JWT（与 Yearning WsTokenParse 一致）。
func (c *Client) NewQuerySession(sourceID string) (*QuerySession, error) {
	token, err := c.Token()
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/api/v2/query/results?source_id=%s", c.cfg.WSBaseURL(), sourceID)
	conn, err := dial(url, token, c.cfg.BaseURL(), c.cfg.Timeout.Connect)
	if err != nil {
		return nil, err
	}
	qs := &QuerySession{client: c, cfg: c.cfg, srcID: sourceID, conn: conn}
	go qs.keepAlive()
	return qs, nil
}

// Exec 发送一条查询并返回结果。
func (qs *QuerySession) Exec(sql, schema string) (*msgpack.QueryResults, error) {
	payload, err := msgpack.MarshalQuery(sql, schema)
	if err != nil {
		return nil, err
	}
	if err := qs.conn.write(payload); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(qs.cfg.Timeout.Query)
	for {
		msgType, data, err := qs.conn.read(deadline)
		if err != nil {
			log.Printf("WS读失败(srcID=%s): %v", qs.srcID, err)
			return nil, err
		}
		if msgType != binaryMessage {
			continue // 忽略文本消息（如 pong）
		}
		res, err := msgpack.UnmarshalResults(data)
		if err != nil {
			// 可能是心跳的纯文本 msgpack，直接忽略。
			continue
		}
		// 心跳响应（heartbeat == "pong"）没有业务结果，跳过等待真正的结果。
		if res.Heartbeat != "" {
			continue
		}
		return res, nil
	}
}

// keepAlive 每 10s 发文本 "ping" 维持会话，连接关闭时退出。
func (qs *QuerySession) keepAlive() {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for range t.C {
		if err := qs.conn.ping(); err != nil {
			return
		}
	}
}

// Close 关闭会话。
func (qs *QuerySession) Close() {
	qs.conn.close()
}

// IsDead 判断会话是否已失效（底层连接已断开/关闭）。
func (qs *QuerySession) IsDead() bool {
	return qs.conn.closed()
}

// IsConnError 判断错误是否属于「连接已断开」类错误，可用于触发重连。
func IsConnError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "close sent") ||
		strings.Contains(s, "close received") ||
		strings.Contains(s, "use of closed network connection") ||
		strings.Contains(s, "connection reset") ||
		strings.Contains(s, "broken pipe")
}
