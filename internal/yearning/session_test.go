package yearning

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// newTestConn 起一个本地 WebSocket 服务端并建立一条测试连接。
func newTestConn(t *testing.T) *gorillaConn {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)

	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	gc, err := dial(url, "", srv.URL, 5*time.Second)
	if err != nil {
		t.Fatalf("dial 失败: %v", err)
	}
	t.Cleanup(gc.close)
	return gc
}

func deadlineNow() time.Time { return time.Now().Add(time.Second) }

// ping 失败后会话必须被标记为失效，否则会留下「IsDead() 为 false 但读写都失败」
// 的僵尸会话，让后续查询反复报 websocket: close sent。
func TestQuerySessionKeepAliveMarksDead(t *testing.T) {
	gc := newTestConn(t)
	qs := &QuerySession{srcID: "src-1", conn: gc}

	if qs.IsDead() {
		t.Fatalf("新建会话不应为失效状态")
	}
	// 模拟 ping 失败路径：keepAlive 会调用 conn.close()。
	gc.close()
	if !qs.IsDead() {
		t.Fatalf("关闭后 IsDead() 应为 true")
	}
	// 已关闭的连接读取应返回 errClosed（归属连接类错误），而不是通用错误。
	if _, _, err := gc.read(deadlineNow()); !errors.Is(err, errClosed) {
		t.Fatalf("已关闭连接的 read 应返回 errClosed, got %v", err)
	}
	if !IsConnError(errClosed) {
		t.Errorf("errClosed 应被 IsConnError 识别")
	}
}

func TestIsConnError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errClosed, true},
		{errors.New("查询等待超时"), false},
		{errors.New("websocket: close sent"), true},
		{errors.New("use of closed network connection"), true},
		{errors.New("connection reset by peer"), true},
	}
	for _, c := range cases {
		if got := IsConnError(c.err); got != c.want {
			t.Errorf("IsConnError(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

// wrappedClosedError 模拟上层用 %w 包装后的连接错误。
type wrappedClosedError struct{ inner error }

func (w wrappedClosedError) Error() string { return "重连后查询仍失败: " + w.inner.Error() }
func (w wrappedClosedError) Unwrap() error { return w.inner }

func TestIsConnErrorWrapped(t *testing.T) {
	err := wrappedClosedError{inner: errClosed}
	if !IsConnError(err) {
		t.Errorf("包装后的连接错误应被识别: %v", err)
	}
}
