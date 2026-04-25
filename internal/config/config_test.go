package config

import "testing"

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{
			name:    "valid",
			mutate:  func(c *Config) { c.Listen = []string{"0.0.0.0:51820"}; c.Upstream = "[2001:db8::10]:51820" },
			wantErr: false,
		},
		{
			name:    "no listeners",
			mutate:  func(c *Config) { c.Upstream = "[2001:db8::10]:51820" },
			wantErr: true,
		},
		{
			name:    "invalid IPv4 listener",
			mutate:  func(c *Config) { c.Listen = []string{"[2001:db8::1]:51820"}; c.Upstream = "[2001:db8::10]:51820" },
			wantErr: true,
		},
		{
			name:    "invalid upstream",
			mutate:  func(c *Config) { c.Listen = []string{"0.0.0.0:51820"}; c.Upstream = "2001:db8::10:51820" },
			wantErr: true,
		},
		{
			name: "invalid timeout",
			mutate: func(c *Config) {
				c.Listen = []string{"0.0.0.0:51820"}
				c.Upstream = "[2001:db8::10]:51820"
				c.SessionTimeout = 0
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			tt.mutate(&cfg)
			err := cfg.Validate()
			if tt.wantErr && err == nil {
				t.Fatal("expected validation error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("expected valid config: %v", err)
			}
		})
	}
}

func TestDefaultMetricsDisabled(t *testing.T) {
	cfg := Default()
	if cfg.MetricsEnabled {
		t.Fatal("metrics must be disabled by default")
	}
}
