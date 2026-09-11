// Command sql-relay 伪装成 MySQL 服务端，把只读查询转发到 Yearning 后端执行。
//
// DataGrip 连接约定：host=127.0.0.1，port=config.yaml 里的 listen，
// user=Yearning 数据源名，password=config.yaml 里的 proxy_password。
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/server"

	"github.com/toolsHelp/Yearning-sql-proxy/internal/audit"
	"github.com/toolsHelp/Yearning-sql-proxy/internal/config"
	"github.com/toolsHelp/Yearning-sql-proxy/internal/metacache"
	"github.com/toolsHelp/Yearning-sql-proxy/internal/proxy"
	"github.com/toolsHelp/Yearning-sql-proxy/internal/yearning"
)

func main() {
	cfgPath := flag.String("config", "config.yaml", "配置文件路径")
	report := flag.String("report", "", "从审计日志(JSONL)生成支持度矩阵报告后退出，不启动代理")
	schemas := flag.Bool("schemas", false, "列出 Yearning 数据源及其可访问的库后退出（用于确认库名/数据源名）")
	flag.Parse()

	// 报告模式：只做离线分析，不需要连 Yearning。
	if *report != "" {
		if err := writeReport(*report, os.Stdout); err != nil {
			log.Fatalf("生成报告失败: %v", err)
		}
		return
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 请求审计：未启用时 New 返回 nil，handler 侧为空操作。
	aud, err := audit.New(cfg)
	if err != nil {
		log.Printf("警告: 审计日志启用失败，继续运行: %v", err)
	}
	defer func() {
		if err := aud.Close(); err != nil {
			log.Printf("关闭审计日志失败: %v", err)
		}
	}()

	yc := yearning.NewClient(cfg)

	// 列出数据源与库名后退出，用于排查「未知数据源 / 库名找不到」。
	if *schemas {
		if err := printSchemas(yc); err != nil {
			log.Fatalf("拉取库列表失败: %v", err)
		}
		return
	}

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
		go handleConn(c, cfg, yc, meta, aud)
	}
}

// writeReport 读取审计日志并把支持度矩阵写成 markdown。
func writeReport(path string, out *os.File) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	events, err := audit.ReadEvents(f)
	if err != nil {
		return fmt.Errorf("解析审计日志失败: %w", err)
	}
	s := audit.Aggregate(events, 30, 60)
	if _, err := out.WriteString(audit.RenderMarkdown(s)); err != nil {
		return err
	}
	return nil
}

// printSchemas 打印每个数据源及其可访问的库名，用于确认 user 该填什么。
func printSchemas(yc *yearning.Client) error {
	srcs, err := yc.Sources()
	if err != nil {
		return err
	}
	fmt.Printf("共 %d 个数据源：\n\n", len(srcs))
	for _, s := range srcs {
		schemas, err := yc.Schemas(s.ID)
		if err != nil {
			fmt.Printf("- %s (source_id=%s)：拉取失败 %v\n", s.Name, s.ID, err)
			continue
		}
		fmt.Printf("- %s (source_id=%s)：%d 个库\n", s.Name, s.ID, len(schemas))
		for _, sc := range schemas {
			fmt.Printf("    %s\n", sc)
		}
	}
	fmt.Println("\nDataGrip 的 user 填数据源名（上面 - 后面的名字），不是库名。")
	return nil
}

// handleConn 处理一个客户端连接：握手、设置 user、进入命令循环。
func handleConn(c net.Conn, cfg *config.Config, yc *yearning.Client, meta *metacache.Cache, aud *audit.Logger) {
	defer c.Close()

	// 动态 provider：任意用户名可通过，但密码统一校验为 proxy_password。
	// 这样 DataGrip 的 user 可自由填 Yearning 数据源名，不必为每个数据源预注册用户。
	dynProvider := &anyUserProvider{password: cfg.ProxyPassword}

	svr := server.NewServer(cfg.ServerVersion, mysql.DEFAULT_COLLATION_ID,
		mysql.AUTH_NATIVE_PASSWORD, nil, nil)

	h := proxy.NewHandler(cfg, yc, meta)
	h.SetAudit(aud)
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
	// UseDB 会在握手阶段触发，早于 SetUser，故先写入连接 ID 便于审计关联。
	h.SetConnID(conn.ConnectionID())
	defer h.Close()

	log.Printf("连接 %d 握手成功: user=%q charset=%d capability=%d attrs=%v",
		conn.ConnectionID(), conn.GetUser(), conn.Charset(), conn.Capability(), conn.Attributes())

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
