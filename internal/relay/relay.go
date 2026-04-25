package relay

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/pmuller/udp46/internal/config"
	"github.com/pmuller/udp46/internal/metrics"
)

const (
	directionClientToUpstream = "client_to_upstream"
	directionUpstreamToClient = "upstream_to_client"
)

type Server struct {
	cfg      config.Config
	log      *slog.Logger
	registry *metrics.Registry
	upstream *net.UDPAddr

	mu        sync.Mutex
	listeners map[string]*listener
	sessions  map[sessionKey]*session
	nextID    uint64
	started   bool
	closed    bool

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type listener struct {
	id   string
	conn *net.UDPConn
}

type sessionKey struct {
	listener string
	client   netip.AddrPort
}

type session struct {
	id       string
	key      sessionKey
	client   *net.UDPAddr
	listener *listener
	upstream *net.UDPConn
	remote   *net.UDPAddr

	mu                      sync.Mutex
	created                 time.Time
	lastClientPacket        time.Time
	lastUpstreamPacket      time.Time
	clientToUpstreamPackets uint64
	clientToUpstreamBytes   uint64
	upstreamToClientPackets uint64
	upstreamToClientBytes   uint64
	closed                  bool
}

func New(cfg config.Config, logger *slog.Logger, registry *metrics.Registry) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	upstream, err := net.ResolveUDPAddr("udp6", cfg.Upstream)
	if err != nil {
		if registry != nil {
			registry.IncDNSResolveError()
		}
		return nil, fmt.Errorf("resolve upstream %q: %w", cfg.Upstream, err)
	}
	if upstream.IP == nil || upstream.IP.To4() != nil {
		return nil, fmt.Errorf("upstream %q did not resolve to IPv6", cfg.Upstream)
	}
	return &Server{
		cfg:       cfg,
		log:       logger,
		registry:  registry,
		upstream:  upstream,
		listeners: map[string]*listener{},
		sessions:  map[sessionKey]*session{},
	}, nil
}

func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return errors.New("server already started")
	}
	if s.closed {
		s.mu.Unlock()
		return errors.New("server already closed")
	}
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.started = true
	s.mu.Unlock()

	for _, addr := range s.cfg.Listen {
		udpAddr, err := net.ResolveUDPAddr("udp4", addr)
		if err != nil {
			s.Close()
			return fmt.Errorf("resolve listen address %q: %w", addr, err)
		}
		conn, err := net.ListenUDP("udp4", udpAddr)
		if err != nil {
			s.Close()
			return fmt.Errorf("listen on %q: %w", addr, err)
		}
		id := conn.LocalAddr().String()
		ln := &listener{id: id, conn: conn}

		s.mu.Lock()
		s.listeners[id] = ln
		s.mu.Unlock()

		s.log.Info("listener started", "listener", id)
		s.wg.Add(1)
		go s.runListener(ln)
	}
	if s.registry != nil {
		s.registry.SetListeners(len(s.cfg.Listen))
	}
	s.wg.Add(1)
	go s.expireLoop()
	return nil
}

func (s *Server) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	listeners := make([]*listener, 0, len(s.listeners))
	for _, ln := range s.listeners {
		listeners = append(listeners, ln)
	}
	sessions := make([]*session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		sessions = append(sessions, sess)
	}
	s.mu.Unlock()

	for _, ln := range listeners {
		_ = ln.conn.Close()
	}
	for _, sess := range sessions {
		s.closeSession(sess, "shutdown", false)
	}
}

func (s *Server) Wait() {
	s.wg.Wait()
}

func (s *Server) ListenAddrs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.listeners))
	for _, ln := range s.listeners {
		out = append(out, ln.id)
	}
	return out
}

func (s *Server) UpstreamAddr() string {
	return s.upstream.String()
}

func (s *Server) Snapshots() []metrics.SessionSnapshot {
	s.mu.Lock()
	sessions := make([]*session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		sessions = append(sessions, sess)
	}
	s.mu.Unlock()

	out := make([]metrics.SessionSnapshot, 0, len(sessions))
	for _, sess := range sessions {
		out = append(out, sess.snapshot())
	}
	return out
}

func (s *Server) runListener(ln *listener) {
	defer s.wg.Done()
	buf := make([]byte, s.cfg.ReadBufferSize)
	for {
		n, client, err := ln.conn.ReadFromUDP(buf)
		if err != nil {
			if s.ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				s.log.Info("listener stopped", "listener", ln.id)
				return
			}
			s.recordError(ln.id, "client_read")
			s.log.Warn("client read failed", "listener", ln.id, "error", err)
			continue
		}
		if client.IP == nil || client.IP.To4() == nil {
			s.recordDrop(ln.id, directionClientToUpstream, "non_ipv4_client")
			continue
		}
		sess, ok := s.getOrCreateSession(ln, client)
		if !ok {
			s.recordDrop(ln.id, directionClientToUpstream, "session_unavailable")
			continue
		}
		if err := sess.writeUpstream(buf[:n], s.cfg.WriteTimeout); err != nil {
			s.recordDrop(ln.id, directionClientToUpstream, "upstream_write_error")
			s.recordUpstreamWriteError(ln.id)
			s.log.Warn("upstream write failed", "listener", ln.id, "session", sess.id, "error", err)
			continue
		}
		s.recordDatagram(ln.id, directionClientToUpstream, n)
	}
}

func (s *Server) getOrCreateSession(ln *listener, client *net.UDPAddr) (*session, bool) {
	ap, ok := addrPortFromUDP(client)
	if !ok {
		return nil, false
	}
	key := sessionKey{listener: ln.id, client: ap}

	s.mu.Lock()
	if sess, ok := s.sessions[key]; ok {
		s.mu.Unlock()
		return sess, true
	}
	if s.cfg.MaxSessions > 0 && len(s.sessions) >= s.cfg.MaxSessions {
		s.mu.Unlock()
		return nil, false
	}
	s.nextID++
	id := fmt.Sprintf("%016x", s.nextID)
	s.mu.Unlock()

	upstream, err := net.DialUDP("udp6", nil, s.upstream)
	if err != nil {
		s.recordUpstreamOpenError(ln.id)
		s.log.Warn("upstream socket open failed", "listener", ln.id, "client", client.String(), "error", err)
		return nil, false
	}

	now := time.Now()
	sess := &session{
		id:                 id,
		key:                key,
		client:             cloneUDPAddr(client),
		listener:           ln,
		upstream:           upstream,
		remote:             cloneUDPAddr(s.upstream),
		created:            now,
		lastClientPacket:   now,
		lastUpstreamPacket: now,
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = upstream.Close()
		return nil, false
	}
	if existing, ok := s.sessions[key]; ok {
		s.mu.Unlock()
		_ = upstream.Close()
		return existing, true
	}
	if s.cfg.MaxSessions > 0 && len(s.sessions) >= s.cfg.MaxSessions {
		s.mu.Unlock()
		_ = upstream.Close()
		return nil, false
	}
	s.sessions[key] = sess
	active := len(s.sessions)
	s.mu.Unlock()

	if s.registry != nil {
		s.registry.SetActiveSessions(active)
		s.registry.IncSessionsCreated()
	}
	s.log.Info("session created", "listener", ln.id, "session", id, "client", client.String(), "upstream", s.upstream.String())
	s.wg.Add(1)
	go s.runUpstream(sess)
	return sess, true
}

func (s *Server) runUpstream(sess *session) {
	defer s.wg.Done()
	buf := make([]byte, s.cfg.ReadBufferSize)
	for {
		n, err := sess.upstream.Read(buf)
		if err != nil {
			if s.ctx.Err() != nil || errors.Is(err, net.ErrClosed) || sess.isClosed() {
				return
			}
			s.recordError(sess.key.listener, "upstream_read")
			s.log.Warn("upstream read failed", "listener", sess.key.listener, "session", sess.id, "error", err)
			s.closeSession(sess, "upstream_read_error", false)
			return
		}
		if err := sess.writeClient(buf[:n], s.cfg.WriteTimeout); err != nil {
			s.recordDrop(sess.key.listener, directionUpstreamToClient, "client_write_error")
			s.recordClientWriteError(sess.key.listener)
			s.log.Warn("client write failed", "listener", sess.key.listener, "session", sess.id, "error", err)
			continue
		}
		s.recordDatagram(sess.key.listener, directionUpstreamToClient, n)
	}
}

func (s *Server) expireLoop() {
	defer s.wg.Done()
	interval := minDuration(time.Second, s.cfg.SessionTimeout/2)
	if interval <= 0 {
		interval = time.Second
	}
	timer := time.NewTicker(interval)
	defer timer.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-timer.C:
			s.expireIdleSessions()
		}
	}
}

func (s *Server) expireIdleSessions() {
	now := time.Now()
	s.mu.Lock()
	sessions := make([]*session, 0)
	for _, sess := range s.sessions {
		if now.Sub(sess.lastActivity()) >= s.cfg.SessionTimeout {
			sessions = append(sessions, sess)
		}
	}
	s.mu.Unlock()
	for _, sess := range sessions {
		s.closeExpiredSession(sess, now)
	}
}

func (s *Server) closeExpiredSession(sess *session, now time.Time) {
	s.closeSessionIf(sess, "idle_timeout", true, func(sess *session) bool {
		return now.Sub(maxTime(sess.lastClientPacket, sess.lastUpstreamPacket)) >= s.cfg.SessionTimeout
	})
}

func (s *Server) closeSession(sess *session, reason string, expired bool) {
	s.closeSessionIf(sess, reason, expired, nil)
}

func (s *Server) closeSessionIf(sess *session, reason string, expired bool, shouldClose func(*session) bool) {
	sess.mu.Lock()
	if sess.closed {
		sess.mu.Unlock()
		return
	}
	if shouldClose != nil && !shouldClose(sess) {
		sess.mu.Unlock()
		return
	}
	sess.closed = true
	created := sess.created
	idle := time.Since(maxTime(sess.lastClientPacket, sess.lastUpstreamPacket))
	clientPackets := sess.clientToUpstreamPackets
	clientBytes := sess.clientToUpstreamBytes
	upstreamPackets := sess.upstreamToClientPackets
	upstreamBytes := sess.upstreamToClientBytes
	sess.mu.Unlock()

	_ = sess.upstream.Close()

	s.mu.Lock()
	delete(s.sessions, sess.key)
	active := len(s.sessions)
	s.mu.Unlock()

	if s.registry != nil {
		s.registry.SetActiveSessions(active)
		if expired {
			s.registry.IncSessionsExpired()
		}
		s.registry.IncClosed(reason, time.Since(created), idle)
	}
	s.log.Info("session closed",
		"listener", sess.key.listener,
		"session", sess.id,
		"reason", reason,
		"client_to_upstream_packets", clientPackets,
		"client_to_upstream_bytes", clientBytes,
		"upstream_to_client_packets", upstreamPackets,
		"upstream_to_client_bytes", upstreamBytes,
	)
}

func (s *session) writeUpstream(packet []byte, timeout time.Duration) error {
	now := time.Now()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return net.ErrClosed
	}
	s.lastClientPacket = now
	s.mu.Unlock()

	if timeout > 0 {
		_ = s.upstream.SetWriteDeadline(now.Add(timeout))
	}
	n, err := s.upstream.Write(packet)
	if err != nil {
		return err
	}
	if n != len(packet) {
		return fmt.Errorf("short upstream write: wrote %d of %d bytes", n, len(packet))
	}
	s.mu.Lock()
	s.clientToUpstreamPackets++
	s.clientToUpstreamBytes += uint64(len(packet))
	s.mu.Unlock()
	return nil
}

func (s *session) writeClient(packet []byte, timeout time.Duration) error {
	if timeout > 0 {
		_ = s.listener.conn.SetWriteDeadline(time.Now().Add(timeout))
	}
	n, err := s.listener.conn.WriteToUDP(packet, s.client)
	if err != nil {
		return err
	}
	if n != len(packet) {
		return fmt.Errorf("short client write: wrote %d of %d bytes", n, len(packet))
	}
	s.mu.Lock()
	s.lastUpstreamPacket = time.Now()
	s.upstreamToClientPackets++
	s.upstreamToClientBytes += uint64(len(packet))
	s.mu.Unlock()
	return nil
}

func (s *session) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *session) lastActivity() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maxTime(s.lastClientPacket, s.lastUpstreamPacket)
}

func (s *session) snapshot() metrics.SessionSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	local := s.upstream.LocalAddr().(*net.UDPAddr)
	return metrics.SessionSnapshot{
		ID:                      s.id,
		Listener:                s.key.listener,
		ClientAddress:           s.client.IP.String(),
		ClientPort:              s.client.Port,
		UpstreamLocalAddress:    local.IP.String(),
		UpstreamLocalPort:       local.Port,
		UpstreamRemoteAddress:   s.remote.IP.String(),
		UpstreamRemotePort:      s.remote.Port,
		Created:                 s.created,
		LastClientPacket:        s.lastClientPacket,
		LastUpstreamPacket:      s.lastUpstreamPacket,
		ClientToUpstreamPackets: s.clientToUpstreamPackets,
		ClientToUpstreamBytes:   s.clientToUpstreamBytes,
		UpstreamToClientPackets: s.upstreamToClientPackets,
		UpstreamToClientBytes:   s.upstreamToClientBytes,
	}
}

func addrPortFromUDP(addr *net.UDPAddr) (netip.AddrPort, bool) {
	if addr == nil || addr.IP == nil {
		return netip.AddrPort{}, false
	}
	ip, ok := netip.AddrFromSlice(addr.IP)
	if !ok {
		return netip.AddrPort{}, false
	}
	if ip.Is4In6() {
		ip4 := addr.IP.To4()
		ip = netip.AddrFrom4([4]byte{ip4[0], ip4[1], ip4[2], ip4[3]})
	}
	if !ip.Is4() || addr.Port < 0 || addr.Port > 65535 {
		return netip.AddrPort{}, false
	}
	return netip.AddrPortFrom(ip, uint16(addr.Port)), true
}

func cloneUDPAddr(addr *net.UDPAddr) *net.UDPAddr {
	out := *addr
	if addr.IP != nil {
		out.IP = append(net.IP(nil), addr.IP...)
	}
	return &out
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func (s *Server) recordDatagram(listener, direction string, n int) {
	if s.registry != nil {
		s.registry.IncDatagram(listener, direction, n)
	}
}

func (s *Server) recordDrop(listener, direction, reason string) {
	if s.registry != nil {
		s.registry.IncDrop(listener, direction, reason)
	}
}

func (s *Server) recordError(listener, operation string) {
	if s.registry != nil {
		s.registry.IncError(listener, operation)
	}
}

func (s *Server) recordUpstreamOpenError(listener string) {
	if s.registry != nil {
		s.registry.IncUpstreamOpenError(listener)
	}
}

func (s *Server) recordUpstreamWriteError(listener string) {
	if s.registry != nil {
		s.registry.IncUpstreamWriteError(listener)
	}
}

func (s *Server) recordClientWriteError(listener string) {
	if s.registry != nil {
		s.registry.IncClientWriteError(listener)
	}
}
