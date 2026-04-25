package relay

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/pmuller/udp46/internal/config"
	"github.com/pmuller/udp46/internal/metrics"
)

type upstreamPacket struct {
	body []byte
	from *net.UDPAddr
}

func TestRelayIPv4ClientToIPv6UpstreamAndReply(t *testing.T) {
	upstream, packets := startIPv6Upstream(t, true)
	srv, reg := startRelay(t, config.Config{
		Listen:         []string{"127.0.0.1:0"},
		Upstream:       upstream.LocalAddr().String(),
		SessionTimeout: 2 * time.Second,
		ReadBufferSize: 1024,
		WriteTimeout:   time.Second,
	})
	defer stopRelay(t, srv)

	client := listenIPv4Client(t)
	defer client.Close()

	sendToRelay(t, client, srv.ListenAddrs()[0], []byte("hello"))
	got := <-packets
	if string(got.body) != "hello" {
		t.Fatalf("upstream got %q, want hello", string(got.body))
	}

	reply := readClient(t, client)
	if string(reply) != "reply:hello" {
		t.Fatalf("client got %q, want reply:hello", string(reply))
	}
	if len(srv.Snapshots()) != 1 {
		t.Fatalf("expected one session, got %d", len(srv.Snapshots()))
	}

	var out bytes.Buffer
	reg.WritePrometheus(&out)
	if !strings.Contains(out.String(), `udp46_datagrams_total`) {
		t.Fatalf("datagram metrics missing:\n%s", out.String())
	}
}

func TestSameClientSameListenerReusesSession(t *testing.T) {
	upstream, packets := startIPv6Upstream(t, true)
	srv, _ := startRelay(t, config.Config{
		Listen:         []string{"127.0.0.1:0"},
		Upstream:       upstream.LocalAddr().String(),
		SessionTimeout: 2 * time.Second,
		ReadBufferSize: 1024,
		WriteTimeout:   time.Second,
	})
	defer stopRelay(t, srv)

	client := listenIPv4Client(t)
	defer client.Close()
	listener := srv.ListenAddrs()[0]

	sendToRelay(t, client, listener, []byte("one"))
	first := <-packets
	_ = readClient(t, client)
	sendToRelay(t, client, listener, []byte("two"))
	second := <-packets
	_ = readClient(t, client)

	if first.from.Port != second.from.Port {
		t.Fatalf("same client/listener used different upstream ports: %d != %d", first.from.Port, second.from.Port)
	}
	if len(srv.Snapshots()) != 1 {
		t.Fatalf("expected one reused session, got %d", len(srv.Snapshots()))
	}
}

func TestSameClientDifferentListenersCreatesDistinctSessions(t *testing.T) {
	upstream, packets := startIPv6Upstream(t, true)
	srv, _ := startRelay(t, config.Config{
		Listen:         []string{"127.0.0.1:0", "127.0.0.1:0"},
		Upstream:       upstream.LocalAddr().String(),
		SessionTimeout: 2 * time.Second,
		ReadBufferSize: 1024,
		WriteTimeout:   time.Second,
	})
	defer stopRelay(t, srv)

	client := listenIPv4Client(t)
	defer client.Close()
	addrs := srv.ListenAddrs()

	sendToRelay(t, client, addrs[0], []byte("one"))
	first := <-packets
	_ = readClient(t, client)
	sendToRelay(t, client, addrs[1], []byte("two"))
	second := <-packets
	_ = readClient(t, client)

	if first.from.Port == second.from.Port {
		t.Fatalf("different listeners reused upstream source port %d", first.from.Port)
	}
	waitForSessions(t, srv, 2)
}

func TestMultipleClientsGetDistinctUpstreamPortsAndReplies(t *testing.T) {
	upstream, packets := startIPv6Upstream(t, true)
	srv, _ := startRelay(t, config.Config{
		Listen:         []string{"127.0.0.1:0"},
		Upstream:       upstream.LocalAddr().String(),
		SessionTimeout: 2 * time.Second,
		ReadBufferSize: 1024,
		WriteTimeout:   time.Second,
	})
	defer stopRelay(t, srv)

	clientA := listenIPv4Client(t)
	defer clientA.Close()
	clientB := listenIPv4Client(t)
	defer clientB.Close()
	listener := srv.ListenAddrs()[0]

	sendToRelay(t, clientA, listener, []byte("a"))
	first := <-packets
	sendToRelay(t, clientB, listener, []byte("b"))
	second := <-packets

	if first.from.Port == second.from.Port {
		t.Fatalf("different clients reused upstream source port %d", first.from.Port)
	}
	if got := string(readClient(t, clientA)); got != "reply:a" {
		t.Fatalf("client A got %q", got)
	}
	if got := string(readClient(t, clientB)); got != "reply:b" {
		t.Fatalf("client B got %q", got)
	}
	waitForSessions(t, srv, 2)
}

func TestIdleSessionsExpireAndActivityRefreshesDeadline(t *testing.T) {
	upstream, _ := startIPv6Upstream(t, true)
	srv, _ := startRelay(t, config.Config{
		Listen:         []string{"127.0.0.1:0"},
		Upstream:       upstream.LocalAddr().String(),
		SessionTimeout: 80 * time.Millisecond,
		ReadBufferSize: 1024,
		WriteTimeout:   time.Second,
	})
	defer stopRelay(t, srv)

	client := listenIPv4Client(t)
	defer client.Close()
	listener := srv.ListenAddrs()[0]

	sendToRelay(t, client, listener, []byte("one"))
	_ = readClient(t, client)
	waitForSessions(t, srv, 1)
	time.Sleep(50 * time.Millisecond)
	sendToRelay(t, client, listener, []byte("two"))
	_ = readClient(t, client)
	waitForSessions(t, srv, 1)
	time.Sleep(45 * time.Millisecond)
	if got := len(srv.Snapshots()); got != 1 {
		t.Fatalf("activity did not refresh session deadline, sessions=%d", got)
	}
	waitUntil(t, 500*time.Millisecond, func() bool { return len(srv.Snapshots()) == 0 })
}

func TestGracefulShutdownClosesSessions(t *testing.T) {
	upstream, _ := startIPv6Upstream(t, true)
	srv, _ := startRelay(t, config.Config{
		Listen:         []string{"127.0.0.1:0"},
		Upstream:       upstream.LocalAddr().String(),
		SessionTimeout: time.Second,
		ReadBufferSize: 1024,
		WriteTimeout:   time.Second,
	})

	client := listenIPv4Client(t)
	defer client.Close()
	sendToRelay(t, client, srv.ListenAddrs()[0], []byte("one"))
	_ = readClient(t, client)
	waitForSessions(t, srv, 1)

	srv.Close()
	srv.Wait()
	if got := len(srv.Snapshots()); got != 0 {
		t.Fatalf("sessions still present after shutdown: %d", got)
	}
}

func startRelay(t *testing.T, cfg config.Config) (*Server, *metrics.Registry) {
	t.Helper()
	cfg = relayTestConfig(cfg)
	reg := metrics.New(cfg.MetricsSessionLabels, nil)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := New(cfg, logger, reg)
	if err != nil {
		t.Fatalf("new relay: %v", err)
	}
	reg.SetSnapshotFunc(srv.Snapshots)
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("start relay: %v", err)
	}
	t.Cleanup(func() { stopRelay(t, srv) })
	return srv, reg
}

func relayTestConfig(overrides config.Config) config.Config {
	cfg := config.Default()
	cfg.Listen = overrides.Listen
	cfg.Upstream = overrides.Upstream
	if overrides.SessionTimeout != 0 {
		cfg.SessionTimeout = overrides.SessionTimeout
	}
	if overrides.ReadBufferSize != 0 {
		cfg.ReadBufferSize = overrides.ReadBufferSize
	}
	if overrides.WriteTimeout != 0 {
		cfg.WriteTimeout = overrides.WriteTimeout
	}
	if overrides.LogLevel != "" {
		cfg.LogLevel = overrides.LogLevel
	}
	cfg.MetricsEnabled = overrides.MetricsEnabled
	if overrides.MetricsListen != "" {
		cfg.MetricsListen = overrides.MetricsListen
	}
	cfg.MetricsSessionLabels = overrides.MetricsSessionLabels
	cfg.MaxSessions = overrides.MaxSessions
	return cfg
}

func stopRelay(t *testing.T, srv *Server) {
	t.Helper()
	srv.Close()
	done := make(chan struct{})
	go func() {
		srv.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("relay did not stop")
	}
}

func startIPv6Upstream(t *testing.T, echo bool) (*net.UDPConn, <-chan upstreamPacket) {
	t.Helper()
	addr := &net.UDPAddr{IP: net.ParseIP("::1"), Port: 0}
	conn, err := net.ListenUDP("udp6", addr)
	if err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
	}
	packets := make(chan upstreamPacket, 16)
	t.Cleanup(func() { _ = conn.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			body := append([]byte(nil), buf[:n]...)
			packets <- upstreamPacket{body: body, from: cloneUDPAddr(from)}
			if echo {
				_, _ = conn.WriteToUDP(append([]byte("reply:"), body...), from)
			}
		}
	}()
	return conn, packets
}

func listenIPv4Client(t *testing.T) *net.UDPConn {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("listen client: %v", err)
	}
	return conn
}

func sendToRelay(t *testing.T, conn *net.UDPConn, addr string, body []byte) {
	t.Helper()
	remote, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		t.Fatalf("resolve relay addr: %v", err)
	}
	if _, err := conn.WriteToUDP(body, remote); err != nil {
		t.Fatalf("write client datagram: %v", err)
	}
}

func readClient(t *testing.T, conn *net.UDPConn) []byte {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	buf := make([]byte, 2048)
	n, _, err := conn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("read client reply: %v", err)
	}
	return append([]byte(nil), buf[:n]...)
}

func waitForSessions(t *testing.T, srv *Server, want int) {
	t.Helper()
	waitUntil(t, time.Second, func() bool { return len(srv.Snapshots()) == want })
}

func waitUntil(t *testing.T, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}
