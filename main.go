// Command sql-relay 伪装成 MySQL 服务端，把只读查询转发到 Yearning 后端执行。
//
// DataGrip 连接约定：host=127.0.0.1，port=config.yaml 里的 listen，
// user=Yearning 数据源名，password=config.yaml 里的 proxy_password。
package main

import (
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/server"

	"github.com/toolsHelp/Yearning-sql-proxy/internal/config"
	"github.com/toolsHelp/Yearning-sql-proxy/internal/metacache"
	"github.com/toolsHelp/Yearning-sql-proxy/internal/proxy"
	"github.com/toolsHelp/Yearning-sql-proxy/internal/yearning"
)

func main() {
	cfgPath := flag.String("config", "config.yaml", "配置文件路径")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	yc := yearning.NewClient(cfg)

	// 进程级元数据缓存（跨连接共享），TTL 取自配置。
	meta := metacache.New(cfg.Timeout.MetadataCache)

	// 启动时先拉一次数据源列表，便于打印可用数据源名；失败不致命（首查会重试）。
	sources := make([]string, 0)
	if srcs, err := yc.Sources(); err == nil {
		for _, s := range srcs {
			log.Printf("Yearning 数据源: %s (source_id=%s)", s.Name, s.ID)
			sources = append(sources, s.Name)
		}
	} else {
		log.Printf("警告: 拉取数据源列表失败（首次查询时会重试）: %v", err)
	}

	l, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		log.Fatalf("监听 %s 失败: %v", cfg.Listen, err)
	}
	log.Printf("sql-relay 只读代理监听 %s，伪装版本 %s，可用数据源: %v",
		cfg.Listen, cfg.ServerVersion, sources)

	go func() {
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
		<-stop
		log.Println("收到退出信号，关闭监听")
		_ = l.Close()
	}()

	for {
		c, err := l.Accept()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			log.Printf("accept 错误: %v", err)
			return
		}
		go handleConn(c, cfg, yc, meta)
	}
}

// handleConn 处理一个客户端连接：握手、设置 user、进入命令循环。
func handleConn(c net.Conn, cfg *config.Config, yc *yearning.Client, meta *metacache.Cache) {
	defer c.Close()

	// 动态 provider：任意用户名可通过，但密码统一校验为 proxy_password。
	// 这样 DataGrip 的 user 可自由填 Yearning 数据源名，不必为每个数据源预注册用户。
	dynProvider := &anyUserProvider{password: cfg.ProxyPassword}

	svr := server.NewServer(cfg.ServerVersion, mysql.DEFAULT_COLLATION_ID,
		mysql.AUTH_NATIVE_PASSWORD, nil, nil)

	h := proxy.NewHandler(cfg, yc, meta)
	conn, err := server.NewCustomizedConn(c, svr, dynProvider, h)
	if err != nil {
		log.Printf("握手失败: %v", err)
		return
	}

	// 注意：go-mysql 的 HandleCommand 在出错时（客户端 EOF 等）会
	// 自行 Close() 并把内部 conn 置 nil，因此关闭前必须校验 Closed()，
	// 否则二次关闭会 nil 指针 panic。recover 兜底避免单个连接拖垮进程。
	defer func() {
		if r := recover(); r != nil {
			log.Printf("连接 %d 处理异常: %v", conn.ConnectionID(), r)
		}
		if !conn.Closed() {
			conn.Close()
		}
	}()

	// 握手后即可取得客户端填的 user（Yearning 数据源名）。
	h.SetUser(conn.GetUser())
	defer h.Close()

	log.Printf("连接 %d 握手成功: user=%q charset=%d", conn.ConnectionID(), conn.GetUser(), conn.Charset())

	for {
		if err := conn.HandleCommand(); err != nil {
			log.Printf("连接 %d 断连: %v", conn.ConnectionID(), err)
			return
		}
	}
}

// anyUserProvider 允许任意用户名通过，但密码必须匹配 proxy_password。
// 这样 DataGrip 的 user 可自由填 Yearning 数据源名，而不必为每个数据源预注册用户。
type anyUserProvider struct {
	password string
}

func (p *anyUserProvider) CheckUsername(username string) (bool, error) {
	return true, nil
}

func (p *anyUserProvider) GetCredential(username string) (password string, found bool, err error) {
	return p.password, true, nil
}

// 确保实现了 CredentialProvider 接口。
var _ server.CredentialProvider = (*anyUserProvider)(nil)
