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

// errTimeout 表示查询等待超时。
var errTimeout = errors.New("查询等待超时")

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
		msgCh: make(chan wsMessage, 8),
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
			return
		}
		select {
		case gc.msgCh <- wsMessage{kind: kind, data: data}:
		case <-gc.done:
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
func (gc *gorillaConn) read(deadline time.Time) (int, []byte, error) {
	_ = gc.ws.SetReadDeadline(deadline)
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case m, ok := <-gc.msgCh:
		if !ok {
			return 0, nil, websocket.ErrCloseSent
		}
		return m.kind, m.data, nil
	case <-timer.C:
		return 0, nil, errTimeout
	case <-gc.done:
		return 0, nil, websocket.ErrCloseSent
	}
}

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
