package yearning

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// errTimeout 表示查询等待超时（本次查询本身失败，连接可能仍然可用）。
var errTimeout = errors.New("查询等待超时")

// errClosed 表示底层 WebSocket 已关闭，会话需要重建。
var errClosed = errors.New("会话已关闭")

// binaryMessage / textMessage 是 gorilla websocket 的消息类型常量转写，
// 避免在业务代码里直接引入 gorilla 的类型。
const (
	textMessage   = websocket.TextMessage
	binaryMessage = websocket.BinaryMessage
)

// gorillaConn 封装一条 WebSocket 连接，串行化写入，
// 并用一个后台读泵把消息投递到 channel，供 Exec 消费。
type gorillaConn struct {
	ws     *websocket.Conn
	writeL sync.Mutex
	msgCh  chan wsMessage
	done   chan struct{}
	once   sync.Once
}

type wsMessage struct {
	kind int
	data []byte
}

// dial 建立 WebSocket 连接，认证 token 通过 Sec-WebSocket-Protocol 携带。
// origin 必须传 Yearning 的 base_url：x/net/websocket 服务端默认做同源校验，
// 不带 Origin（或被跨域）会返回 403。
func dial(url, token, origin string, timeout time.Duration) (*gorillaConn, error) {
	header := http.Header{}
	if token != "" {
		header.Set("Sec-WebSocket-Protocol", token)
	}
	if origin != "" {
		header.Set("Origin", origin)
	}
	// 模仿真实浏览器，规避部分前置网关对非浏览器 UA 的拦截。
	header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36")
	dialer := &websocket.Dialer{
		HandshakeTimeout: timeout,
		// 关键：Yearning 服务端的 x/net/websocket 不会合并 continuation 分帧，
		// 每个分片会被当作独立消息交给 msgpack 解码，导致大 SQL 解码失败、
		// 会话被关闭（表现为查询后立即 close 1000）。gorilla 默认写缓冲 4096
		// 字节，超过就自动分帧——必须调大缓冲让消息保持单帧（浏览器即单帧直发）。
		WriteBufferSize: 1 << 20,
	}
	ws, resp, err := dialer.Dial(url, header)
	if err != nil {
		// gorilla 的 ErrBadHandshake 会吞掉状态码，这里补打出来辅助定位。
		if resp != nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			log.Printf("WS 握手失败: url=%s status=%d %s body=%q",
				url, resp.StatusCode, resp.Status, string(body))
		}
		return nil, fmt.Errorf("%w (status=%v)", err, statusOf(resp))
	}
	gc := &gorillaConn{
		ws:    ws,
		msgCh: make(chan wsMessage, 64),
		done:  make(chan struct{}),
	}
	go gc.readPump()
	return gc, nil
}

func statusOf(resp *http.Response) string {
	if resp == nil {
		return "无HTTP响应"
	}
	return resp.Status
}

func (gc *gorillaConn) readPump() {
	defer close(gc.msgCh)
	defer func() {
		// 读端退出意味着连接已不可用（对端关闭或网络错误），标记 done 关闭。
		gc.once.Do(func() { close(gc.done) })
	}()
	for {
		kind, data, err := gc.ws.ReadMessage()
		if err != nil {
			// 打印底层关闭原因（如 close 1009 message too big / 1006 异常关闭），
			// 这是判断「服务端拒绝大消息」还是「服务端崩溃」的唯一线索。
			log.Printf("WS读泵退出: %v", err)
			return
		}
		// 非阻塞投递：channel 满时丢弃消息。
		// msgCh 只在 Exec 等待结果时被消费，查询结果一定有人读；
		// 会被丢弃的只有空闲期间积压的心跳 pong——丢弃它们是安全的，
		// 否则 8 个 pong 塞满 channel 后 readPump 会卡死在投递上。
		select {
		case gc.msgCh <- wsMessage{kind: kind, data: data}:
		case <-gc.done:
			return
		default:
		}
	}
}

// drainPending 清空 channel 里积压的旧消息（空闲期间的心跳 pong 等），
// 在发送新查询前调用，保证随后到达的查询结果不会被非阻塞投递丢弃。
func (gc *gorillaConn) drainPending() {
	for {
		select {
		case <-gc.msgCh:
		default:
			return
		}
	}
}

func (gc *gorillaConn) write(data []byte) error {
	gc.writeL.Lock()
	defer gc.writeL.Unlock()
	return gc.ws.WriteMessage(binaryMessage, data)
}

// ping 打印保活文本消息（"ping"）。
func (gc *gorillaConn) ping() error {
	gc.writeL.Lock()
	defer gc.writeL.Unlock()
	return gc.ws.WriteMessage(textMessage, []byte("ping"))
}

// read 阻塞读取一条消息直到 deadline。
//
// 注意：绝不能在这里调用 ws.SetReadDeadline —— gorilla 的 deadline 是连接级的，
// 会影响后台 readPump 正在阻塞的 ReadMessage。若在这里设置 now+120s，
// 会话空闲 120 秒后 readPump 必然读超时退出、连接死亡（这正是历史上
// 「websocket: close sent」频繁出现的根因）。查询超时由本函数的 timer 分支实现。
//
// 连接已关闭时返回 errClosed，便于调用方区分「本次查询超时」与「会话已失效需重建」。
func (gc *gorillaConn) read(deadline time.Time) (int, []byte, error) {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case m, ok := <-gc.msgCh:
		if !ok {
			return 0, nil, errClosed
		}
		return m.kind, m.data, nil
	case <-timer.C:
		return 0, nil, errTimeout
	case <-gc.done:
		return 0, nil, errClosed
	}
}

// close 关闭连接并标记 done。可重复调用。
func (gc *gorillaConn) close() {
	gc.once.Do(func() {
		close(gc.done)
		_ = gc.ws.Close()
	})
}

// closed 判断连接是否已关闭（done channel 已关闭）。
func (gc *gorillaConn) closed() bool {
	select {
	case <-gc.done:
		return true
	default:
		return false
	}
}
