package metrics

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMetricsSessionLabelsDisabledByDefault(t *testing.T) {
	reg := New(false, func() []SessionSnapshot {
		return []SessionSnapshot{{ID: "s1", Listener: "127.0.0.1:1"}}
	})
	reg.SetListeners(1)

	rec := httptest.NewRecorder()
	reg.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))

	body := rec.Body.String()
	if !strings.Contains(body, "udp46_build_info") {
		t.Fatalf("build metric missing:\n%s", body)
	}
	if strings.Contains(body, "udp46_session_info") {
		t.Fatalf("session labels emitted while disabled:\n%s", body)
	}
}

func TestMetricsSessionLabelsEnabled(t *testing.T) {
	reg := New(true, func() []SessionSnapshot {
		now := time.Unix(100, 0)
		return []SessionSnapshot{{
			ID:                    "s1",
			Listener:              "127.0.0.1:1",
			ClientAddress:         "192.0.2.10",
			ClientPort:            62000,
			UpstreamLocalAddress:  "::1",
			UpstreamLocalPort:     40000,
			UpstreamRemoteAddress: "2001:db8::1",
			UpstreamRemotePort:    51820,
			LastClientPacket:      now,
			LastUpstreamPacket:    now,
		}}
	})

	rec := httptest.NewRecorder()
	reg.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))

	if !strings.Contains(rec.Body.String(), "udp46_session_info") {
		t.Fatalf("session labels missing:\n%s", rec.Body.String())
	}
}

func TestDebugSessionsHandler(t *testing.T) {
	handler := DebugSessionsHandler(func() []SessionSnapshot {
		return []SessionSnapshot{{ID: "s1", Listener: "127.0.0.1:1"}}
	})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/debug/sessions", nil))

	if !strings.Contains(rec.Body.String(), `"session_id": "s1"`) {
		t.Fatalf("debug response missing session:\n%s", rec.Body.String())
	}
}
