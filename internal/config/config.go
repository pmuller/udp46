package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultSessionTimeout = 180 * time.Second
	DefaultReadBufferSize = 64 * 1024
	DefaultWriteTimeout   = 5 * time.Second
	DefaultLogLevel       = "info"
	DefaultMetricsListen  = "127.0.0.1:9108"
	DefaultMaxSessions    = 0
)

type Config struct {
	Listen               []string
	Upstream             string
	SessionTimeout       time.Duration
	ReadBufferSize       int
	WriteTimeout         time.Duration
	LogLevel             string
	MetricsEnabled       bool
	MetricsListen        string
	MetricsSessionLabels bool
	MaxSessions          int
}

func Default() Config {
	return Config{
		SessionTimeout: DefaultSessionTimeout,
		ReadBufferSize: DefaultReadBufferSize,
		WriteTimeout:   DefaultWriteTimeout,
		LogLevel:       DefaultLogLevel,
		MetricsListen:  DefaultMetricsListen,
		MaxSessions:    DefaultMaxSessions,
	}
}

func (c Config) Validate() error {
	if len(c.Listen) == 0 {
		return errors.New("at least one IPv4 listen address is required")
	}
	for _, addr := range c.Listen {
		if err := validateIPv4Listen(addr); err != nil {
			return err
		}
	}
	if err := validateHostPort(c.Upstream, "upstream"); err != nil {
		return err
	}
	if c.SessionTimeout <= 0 {
		return fmt.Errorf("session timeout must be positive: %s", c.SessionTimeout)
	}
	if c.ReadBufferSize <= 0 {
		return fmt.Errorf("read buffer size must be positive: %d", c.ReadBufferSize)
	}
	if c.WriteTimeout <= 0 {
		return fmt.Errorf("write timeout must be positive: %s", c.WriteTimeout)
	}
	switch strings.ToLower(c.LogLevel) {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("invalid log level %q", c.LogLevel)
	}
	if c.MetricsEnabled {
		if err := validateHostPort(c.MetricsListen, "metrics listen"); err != nil {
			return err
		}
	}
	if c.MaxSessions < 0 {
		return fmt.Errorf("max sessions must not be negative: %d", c.MaxSessions)
	}
	return nil
}

func validateIPv4Listen(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid IPv4 listen address %q: %w", addr, err)
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return fmt.Errorf("invalid IPv4 listen port %q: %w", port, err)
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("listen address %q must use a literal IPv4 address: %w", addr, err)
	}
	if !ip.Is4() {
		return fmt.Errorf("listen address %q must be IPv4", addr)
	}
	return nil
}

func validateHostPort(addr string, name string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid %s address %q: %w", name, addr, err)
	}
	if host == "" {
		return fmt.Errorf("%s host is required", name)
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return fmt.Errorf("invalid %s port %q: %w", name, port, err)
	}
	return nil
}
