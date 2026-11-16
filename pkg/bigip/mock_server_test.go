package bigip

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"time"

	"myf5-cli/pkg/client"
)

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type mockServer struct {
	URL    string
	client *http.Client
}

func (m *mockServer) Close()               {}
func (m *mockServer) Client() *http.Client { return m.client }

var (
	mockPortSeqMu sync.Mutex
	mockPortSeq   = 18000
)

func newMockServer(handler http.Handler) *mockServer {
	mockPortSeqMu.Lock()
	mockPortSeq++
	port := mockPortSeq
	mockPortSeqMu.Unlock()

	return &mockServer{
		URL: fmt.Sprintf("http://127.0.0.1:%d", port),
		client: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				return rec.Result(), nil
			}),
		},
	}
}

type bufferedPipeConn struct {
	rCh       <-chan []byte
	wCh       chan<- []byte
	rClosed   <-chan struct{}
	wClosed   chan struct{}
	closeOnce sync.Once
	readBuf   []byte
}

func newBufferedConnPair() (net.Conn, net.Conn) {
	ch1 := make(chan []byte, 1024)
	ch2 := make(chan []byte, 1024)
	closed1 := make(chan struct{})
	closed2 := make(chan struct{})
	c1 := &bufferedPipeConn{rCh: ch1, wCh: ch2, rClosed: closed1, wClosed: closed2}
	c2 := &bufferedPipeConn{rCh: ch2, wCh: ch1, rClosed: closed2, wClosed: closed1}
	return c1, c2
}

func (c *bufferedPipeConn) Read(b []byte) (int, error) {
	if len(c.readBuf) > 0 {
		n := copy(b, c.readBuf)
		c.readBuf = c.readBuf[n:]
		return n, nil
	}
	// Prioritize draining data channel first
	select {
	case data, ok := <-c.rCh:
		if !ok {
			return 0, io.EOF
		}
		n := copy(b, data)
		if n < len(data) {
			c.readBuf = data[n:]
		}
		return n, nil
	default:
	}

	select {
	case data, ok := <-c.rCh:
		if !ok {
			return 0, io.EOF
		}
		n := copy(b, data)
		if n < len(data) {
			c.readBuf = data[n:]
		}
		return n, nil
	case <-c.rClosed:
		select {
		case data, ok := <-c.rCh:
			if ok {
				n := copy(b, data)
				if n < len(data) {
					c.readBuf = data[n:]
				}
				return n, nil
			}
		default:
		}
		return 0, io.EOF
	}
}

func (c *bufferedPipeConn) Write(b []byte) (int, error) {
	select {
	case <-c.wClosed:
		return 0, io.ErrClosedPipe
	default:
	}
	cp := make([]byte, len(b))
	copy(cp, b)
	select {
	case c.wCh <- cp:
		return len(b), nil
	case <-c.wClosed:
		return 0, io.ErrClosedPipe
	}
}

func (c *bufferedPipeConn) Close() error {
	c.closeOnce.Do(func() {
		close(c.wClosed)
	})
	return nil
}

func (c *bufferedPipeConn) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 22}
}
func (c *bufferedPipeConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 22}
}
func (c *bufferedPipeConn) SetDeadline(t time.Time) error      { return nil }
func (c *bufferedPipeConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *bufferedPipeConn) SetWriteDeadline(t time.Time) error { return nil }

type pipeSSHServer struct {
	addr  string
	conns chan net.Conn
	done  chan struct{}
	port  int
}

var (
	pipeSSHMu      sync.Mutex
	pipeSSHServers = make(map[string]*pipeSSHServer)
	pipeSSHPortSeq = 22000
)

func newPipeSSHServer() (*pipeSSHServer, int) {
	pipeSSHMu.Lock()
	defer pipeSSHMu.Unlock()
	pipeSSHPortSeq++
	port := pipeSSHPortSeq
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	s := &pipeSSHServer{
		addr:  addr,
		conns: make(chan net.Conn, 1),
		done:  make(chan struct{}),
		port:  port,
	}
	pipeSSHServers[addr] = s
	return s, port
}

func (s *pipeSSHServer) Accept() (net.Conn, error) {
	select {
	case c := <-s.conns:
		return c, nil
	case <-s.done:
		return nil, net.ErrClosed
	}
}

func (s *pipeSSHServer) Close() error {
	pipeSSHMu.Lock()
	delete(pipeSSHServers, s.addr)
	pipeSSHMu.Unlock()
	close(s.done)
	return nil
}

func init() {
	_ = os.Setenv("NO_PROXY", "127.0.0.1,localhost,::1")
	client.SetDialContext(func(ctx context.Context, network, addr string) (net.Conn, error) {
		pipeSSHMu.Lock()
		s, ok := pipeSSHServers[addr]
		pipeSSHMu.Unlock()
		if ok {
			c1, c2 := newBufferedConnPair()
			select {
			case s.conns <- c2:
				return c1, nil
			case <-s.done:
				c1.Close()
				c2.Close()
				return nil, net.ErrClosed
			case <-ctx.Done():
				c1.Close()
				c2.Close()
				return nil, ctx.Err()
			}
		}
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	})
}
