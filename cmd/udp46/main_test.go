package main

import (
	"testing"
)

func TestRunReturnsFailureWhenMetricsCannotBind(t *testing.T) {
	code := run([]string{
		"--listen", "127.0.0.1:0",
		"--upstream", "[::1]:51820",
		"--metrics.enabled",
		"--metrics.listen", "192.0.2.1:9108",
	})
	if code != 1 {
		t.Fatalf("run returned %d, want 1", code)
	}
}
