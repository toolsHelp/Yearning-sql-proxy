// Package config 负责加载并校验 sql-relay 的本地配置文件。
//
// 代理伪装成 MySQL 服务端，通过 Yearning 后端执行只读查询。
// 本地配置文件只存放 Yearning 的登录口令，
// 后端 MySQL 的真实凭据始终由 Yearning 服务端持有，不落盘在此。
package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 是 config.yaml 的完整结构。
type Config struct {
	// Listen 是本地 MySQL 协议监听地址，通常绑定回环。
	Listen string `yaml:"listen"`
	// ServerVersion 是伪装的 MySQL 服务端版本，
	// 设为 5.7.x 可促使客户端使用 mysql_native_password 认证。
	ServerVersion string `yaml:"server_version"`
	// ProxyPassword 是 DataGrip 里 password 字段填写的本地鉴权口令，
	// 用于防止本机其他进程蹭连。
	ProxyPassword string `yaml:"proxy_password"`

	Yearning Yearning `yaml:"yearning"`

	Timeout Timeout `yaml:"timeout"`
}

// Yearning 描述后端 Yearning 服务的连接信息与登录凭据。
type Yearning struct {
	// BaseURL 形如 http://yearning.example.com 或 https://...，
	// 不带末尾斜杠；http/https 会自动推导出 ws/wss。
	BaseURL string `yaml:"base_url"`
	// LoginUser / LoginPassword 用于调 Yearning 的登录接口换取 JWT。
	LoginUser     string `yaml:"login_user"`
	LoginPassword string `yaml:"login_password"`
	// AuthMode 取值 ldap 或 general，分别对应 POST /ldap 与 POST /login。
	AuthMode string `yaml:"auth_mode"`
	// LDAP / OIDC 标志，与 Yearning /ldap 接口入参一致。
	IsLDAP bool `yaml:"is_ldap"`
	IsOIDC bool `yaml:"is_oidc"`
}

type Timeout struct {
	// Query 是单次查询的超时。
	Query time.Duration `yaml:"query"`
	// Connect 是 HTTP/WebSocket 建连超时。
	Connect time.Duration `yaml:"connect"`
	// MetadataCache 是 information_schema 元数据（表结构）本地缓存的 TTL。
	MetadataCache time.Duration `yaml:"metadata_cache"`
}

// Load 从指定路径加载并校验配置。path 为空时尝试同目录 config.yaml。
func Load(path string) (*Config, error) {
	if path == "" {
		path = "config.yaml"
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}
	cfg := &Config{
		Listen:        "127.0.0.1:3307",
		ServerVersion: "5.7.36",
	}
	if err := yaml.Unmarshal(b, cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Listen == "" {
		c.Listen = "127.0.0.1:3307"
	}
	if c.ServerVersion == "" {
		c.ServerVersion = "5.7.36"
	}
	if c.Yearning.AuthMode == "" {
		c.Yearning.AuthMode = "ldap"
	}
	if !c.Yearning.IsLDAP && !c.Yearning.IsOIDC && c.Yearning.AuthMode == "ldap" {
		c.Yearning.IsLDAP = true
	}
	if c.Timeout.Query == 0 {
		c.Timeout.Query = 60 * time.Second
	}
	if c.Timeout.Connect == 0 {
		c.Timeout.Connect = 10 * time.Second
	}
	if c.Timeout.MetadataCache == 0 {
		c.Timeout.MetadataCache = 5 * time.Minute
	}
}

// Validate 检查必填项与取值合法性。
func (c *Config) Validate() error {
	if c.ProxyPassword == "" {
		return fmt.Errorf("proxy_password 不能为空")
	}
	if c.Yearning.BaseURL == "" {
		return fmt.Errorf("yearning.base_url 不能为空")
	}
	if c.Yearning.LoginUser == "" {
		return fmt.Errorf("yearning.login_user 不能为空")
	}
	switch strings.ToLower(c.Yearning.AuthMode) {
	case "ldap", "general":
	default:
		return fmt.Errorf("yearning.auth_mode 必须是 ldap 或 general")
	}
	return nil
}

// BaseURL 归一化去除末尾斜杠。
func (c *Config) BaseURL() string {
	return strings.TrimRight(c.Yearning.BaseURL, "/")
}

// WSBaseURL 由 BaseURL 推导出 WebSocket 地址前缀。
func (c *Config) WSBaseURL() string {
	u := c.BaseURL()
	switch {
	case strings.HasPrefix(u, "https://"):
		return "wss://" + strings.TrimPrefix(u, "https://")
	case strings.HasPrefix(u, "http://"):
		return "ws://" + strings.TrimPrefix(u, "http://")
	default:
		return "ws://" + u
	}
}

// LoginPath 返回登录接口路径。
func (c *Config) LoginPath() string {
	if strings.ToLower(c.Yearning.AuthMode) == "general" {
		return "/login"
	}
	return "/ldap"
}

// LoginBody 构造登录请求体（与 Yearning login 入参一致）。
func (c *Config) LoginBody() map[string]interface{} {
	body := map[string]interface{}{
		"username": c.Yearning.LoginUser,
		"password": c.Yearning.LoginPassword,
	}
	if c.Yearning.AuthMode == "" || strings.EqualFold(c.Yearning.AuthMode, "ldap") {
		body["is_ldap"] = c.Yearning.IsLDAP
		body["is_oidc"] = c.Yearning.IsOIDC
	}
	return body
}
