package metrics

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pmuller/udp46/internal/build"
)

type SessionSnapshot struct {
	ID                      string    `json:"session_id"`
	Listener                string    `json:"listener"`
	ClientAddress           string    `json:"client_address"`
	ClientPort              int       `json:"client_port"`
	UpstreamLocalAddress    string    `json:"upstream_local_address"`
	UpstreamLocalPort       int       `json:"upstream_local_port"`
	UpstreamRemoteAddress   string    `json:"upstream_remote_address"`
	UpstreamRemotePort      int       `json:"upstream_remote_port"`
	Created                 time.Time `json:"created"`
	LastClientPacket        time.Time `json:"last_client_packet"`
	LastUpstreamPacket      time.Time `json:"last_upstream_packet"`
	ClientToUpstreamPackets uint64    `json:"client_to_upstream_packets"`
	ClientToUpstreamBytes   uint64    `json:"client_to_upstream_bytes"`
	UpstreamToClientPackets uint64    `json:"upstream_to_client_packets"`
	UpstreamToClientBytes   uint64    `json:"upstream_to_client_bytes"`
}

type SnapshotFunc func() []SessionSnapshot

type Registry struct {
	mu sync.Mutex

	sessionLabels bool
	listeners     float64

	sessionsActive       float64
	sessionsCreatedTotal float64
	sessionsExpiredTotal float64
	dnsResolveErrors     float64

	closed                   map[string]float64
	datagrams                map[labelSet]float64
	bytes                    map[labelSet]float64
	drops                    map[labelSet]float64
	errors                   map[labelSet]float64
	upstreamOpenErrors       map[string]float64
	upstreamWriteErrors      map[string]float64
	clientWriteErrors        map[string]float64
	sessionDurationHistogram histogram
	sessionIdleHistogram     histogram
	snapshots                SnapshotFunc
}

type labelSet struct {
	Listener  string
	Direction string
	Reason    string
	Operation string
}

type histogram struct {
	buckets []float64
	counts  []uint64
	sum     float64
	count   uint64
}

func New(sessionLabels bool, snapshots SnapshotFunc) *Registry {
	return &Registry{
		sessionLabels:            sessionLabels,
		closed:                   map[string]float64{},
		datagrams:                map[labelSet]float64{},
		bytes:                    map[labelSet]float64{},
		drops:                    map[labelSet]float64{},
		errors:                   map[labelSet]float64{},
		upstreamOpenErrors:       map[string]float64{},
		upstreamWriteErrors:      map[string]float64{},
		clientWriteErrors:        map[string]float64{},
		sessionDurationHistogram: newHistogram(),
		sessionIdleHistogram:     newHistogram(),
		snapshots:                snapshots,
	}
}

func (r *Registry) SetSnapshotFunc(snapshots SnapshotFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.snapshots = snapshots
}

func newHistogram() histogram {
	buckets := []float64{1, 5, 15, 30, 60, 120, 300, 600, 1800, 3600}
	return histogram{buckets: buckets, counts: make([]uint64, len(buckets))}
}

func (r *Registry) SetListeners(n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.listeners = float64(n)
}

func (r *Registry) SetActiveSessions(n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessionsActive = float64(n)
}

func (r *Registry) IncSessionsCreated() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessionsCreatedTotal++
}

func (r *Registry) IncSessionsExpired() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessionsExpiredTotal++
}

func (r *Registry) IncClosed(reason string, duration time.Duration, idle time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed[reason]++
	r.sessionDurationHistogram.observe(duration.Seconds())
	r.sessionIdleHistogram.observe(idle.Seconds())
}

func (r *Registry) IncDatagram(listener, direction string, bytes int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := labelSet{Listener: listener, Direction: direction}
	r.datagrams[key]++
	r.bytes[key] += float64(bytes)
}

func (r *Registry) IncDrop(listener, direction, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.drops[labelSet{Listener: listener, Direction: direction, Reason: reason}]++
}

func (r *Registry) IncError(listener, operation string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errors[labelSet{Listener: listener, Operation: operation}]++
}

func (r *Registry) IncUpstreamOpenError(listener string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.upstreamOpenErrors[listener]++
}

func (r *Registry) IncUpstreamWriteError(listener string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.upstreamWriteErrors[listener]++
}

func (r *Registry) IncClientWriteError(listener string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clientWriteErrors[listener]++
}

func (r *Registry) IncDNSResolveError() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dnsResolveErrors++
}

func (h *histogram) observe(v float64) {
	h.count++
	h.sum += v
	for i, b := range h.buckets {
		if v <= b {
			h.counts[i]++
		}
	}
}

func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		r.WritePrometheus(w)
	})
}

func (r *Registry) WritePrometheus(w io.Writer) {
	r.mu.Lock()
	listeners := r.listeners
	active := r.sessionsActive
	created := r.sessionsCreatedTotal
	expired := r.sessionsExpiredTotal
	dnsErrors := r.dnsResolveErrors
	closed := copyStringMap(r.closed)
	datagrams := copyLabelMap(r.datagrams)
	bytes := copyLabelMap(r.bytes)
	drops := copyLabelMap(r.drops)
	errors := copyLabelMap(r.errors)
	upstreamOpenErrors := copyStringMap(r.upstreamOpenErrors)
	upstreamWriteErrors := copyStringMap(r.upstreamWriteErrors)
	clientWriteErrors := copyStringMap(r.clientWriteErrors)
	duration := cloneHistogram(r.sessionDurationHistogram)
	idle := cloneHistogram(r.sessionIdleHistogram)
	sessionLabels := r.sessionLabels
	snapshots := r.snapshots
	r.mu.Unlock()

	fmt.Fprintf(w, "# HELP udp46_build_info Build metadata.\n# TYPE udp46_build_info gauge\n")
	fmt.Fprintf(w, "udp46_build_info{version=%q,commit=%q,date=%q} 1\n", esc(build.Version), esc(build.Commit), esc(build.Date))
	fmt.Fprintf(w, "# HELP udp46_listeners Configured UDP listeners.\n# TYPE udp46_listeners gauge\nudp46_listeners %.0f\n", listeners)
	fmt.Fprintf(w, "# HELP udp46_sessions_active Active sessions.\n# TYPE udp46_sessions_active gauge\nudp46_sessions_active %.0f\n", active)
	fmt.Fprintf(w, "# HELP udp46_sessions_created_total Sessions created.\n# TYPE udp46_sessions_created_total counter\nudp46_sessions_created_total %.0f\n", created)
	fmt.Fprintf(w, "# HELP udp46_sessions_expired_total Sessions expired by idle timeout.\n# TYPE udp46_sessions_expired_total counter\nudp46_sessions_expired_total %.0f\n", expired)
	writeStringCounters(w, "udp46_sessions_closed_total", "Sessions closed.", "reason", closed)
	writeLabelCounters(w, "udp46_datagrams_total", "Relayed datagrams.", []string{"listener", "direction"}, datagrams, func(k labelSet) []string { return []string{k.Listener, k.Direction} })
	writeLabelCounters(w, "udp46_bytes_total", "Relayed bytes.", []string{"listener", "direction"}, bytes, func(k labelSet) []string { return []string{k.Listener, k.Direction} })
	writeLabelCounters(w, "udp46_drops_total", "Dropped datagrams.", []string{"listener", "direction", "reason"}, drops, func(k labelSet) []string { return []string{k.Listener, k.Direction, k.Reason} })
	writeLabelCounters(w, "udp46_errors_total", "Socket and runtime errors.", []string{"listener", "operation"}, errors, func(k labelSet) []string { return []string{k.Listener, k.Operation} })
	writeStringCounters(w, "udp46_upstream_socket_open_errors_total", "Upstream socket open errors.", "listener", upstreamOpenErrors)
	writeStringCounters(w, "udp46_upstream_write_errors_total", "Upstream write errors.", "listener", upstreamWriteErrors)
	writeStringCounters(w, "udp46_client_write_errors_total", "Client write errors.", "listener", clientWriteErrors)
	fmt.Fprintf(w, "# HELP udp46_dns_resolve_errors_total Upstream DNS resolution errors.\n# TYPE udp46_dns_resolve_errors_total counter\nudp46_dns_resolve_errors_total %.0f\n", dnsErrors)
	writeHistogram(w, "udp46_session_duration_seconds", "Session lifetime at close.", duration)
	writeHistogram(w, "udp46_session_idle_seconds", "Session idle age at close.", idle)

	if sessionLabels && snapshots != nil {
		writeSessionMetrics(w, snapshots())
	}
}

func writeStringCounters(w io.Writer, name, help, label string, values map[string]float64) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n", name, help, name)
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(w, "%s{%s=%q} %.0f\n", name, label, esc(key), values[key])
	}
}

func writeLabelCounters(w io.Writer, name, help string, labels []string, values map[labelSet]float64, labelValues func(labelSet) []string) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n", name, help, name)
	keys := make([]labelSet, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return fmt.Sprint(keys[i]) < fmt.Sprint(keys[j]) })
	for _, key := range keys {
		vals := labelValues(key)
		parts := make([]string, len(labels))
		for i := range labels {
			parts[i] = fmt.Sprintf("%s=%q", labels[i], esc(vals[i]))
		}
		fmt.Fprintf(w, "%s{%s} %.0f\n", name, strings.Join(parts, ","), values[key])
	}
}

func writeHistogram(w io.Writer, name, help string, h histogram) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s histogram\n", name, help, name)
	for i, b := range h.buckets {
		fmt.Fprintf(w, "%s_bucket{le=%q} %d\n", name, fmt.Sprintf("%g", b), h.counts[i])
	}
	fmt.Fprintf(w, "%s_bucket{le=\"+Inf\"} %d\n", name, h.count)
	fmt.Fprintf(w, "%s_sum %g\n%s_count %d\n", name, h.sum, name, h.count)
}

func writeSessionMetrics(w io.Writer, snapshots []SessionSnapshot) {
	fmt.Fprintf(w, "# HELP udp46_session_info Per-session metadata. High-cardinality; enable only for small deployments or temporary debugging.\n# TYPE udp46_session_info gauge\n")
	for _, s := range snapshots {
		fmt.Fprintf(w, "udp46_session_info{session_id=%q,listener=%q,client_address=%q,client_port=%q,upstream_local_address=%q,upstream_local_port=%q,upstream_remote_address=%q,upstream_remote_port=%q} 1\n",
			esc(s.ID), esc(s.Listener), esc(s.ClientAddress), esc(fmt.Sprint(s.ClientPort)), esc(s.UpstreamLocalAddress), esc(fmt.Sprint(s.UpstreamLocalPort)), esc(s.UpstreamRemoteAddress), esc(fmt.Sprint(s.UpstreamRemotePort)))
		fmt.Fprintf(w, "udp46_session_packets_total{session_id=%q,listener=%q,direction=\"client_to_upstream\"} %d\n", esc(s.ID), esc(s.Listener), s.ClientToUpstreamPackets)
		fmt.Fprintf(w, "udp46_session_packets_total{session_id=%q,listener=%q,direction=\"upstream_to_client\"} %d\n", esc(s.ID), esc(s.Listener), s.UpstreamToClientPackets)
		fmt.Fprintf(w, "udp46_session_bytes_total{session_id=%q,listener=%q,direction=\"client_to_upstream\"} %d\n", esc(s.ID), esc(s.Listener), s.ClientToUpstreamBytes)
		fmt.Fprintf(w, "udp46_session_bytes_total{session_id=%q,listener=%q,direction=\"upstream_to_client\"} %d\n", esc(s.ID), esc(s.Listener), s.UpstreamToClientBytes)
		fmt.Fprintf(w, "udp46_session_last_activity_timestamp_seconds{session_id=%q,listener=%q,direction=\"client_to_upstream\"} %d\n", esc(s.ID), esc(s.Listener), s.LastClientPacket.Unix())
		fmt.Fprintf(w, "udp46_session_last_activity_timestamp_seconds{session_id=%q,listener=%q,direction=\"upstream_to_client\"} %d\n", esc(s.ID), esc(s.Listener), s.LastUpstreamPacket.Unix())
	}
}

func copyStringMap(in map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyLabelMap(in map[labelSet]float64) map[labelSet]float64 {
	out := make(map[labelSet]float64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneHistogram(in histogram) histogram {
	in.counts = append([]uint64(nil), in.counts...)
	return in
}

func esc(v string) string {
	return v
}
