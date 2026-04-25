package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/pmuller/udp46/internal/build"
	"github.com/pmuller/udp46/internal/config"
	"github.com/pmuller/udp46/internal/metrics"
	"github.com/pmuller/udp46/internal/relay"
)

type repeatedStrings []string

func (v *repeatedStrings) String() string {
	return strings.Join(*v, ",")
}

func (v *repeatedStrings) Set(value string) error {
	*v = append(*v, value)
	return nil
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	cfg := config.Default()
	var version bool
	var listens repeatedStrings

	fs := flag.NewFlagSet("udp46", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Var(&listens, "listen", "IPv4 UDP listen address. Repeat for multiple listeners.")
	fs.StringVar(&cfg.Upstream, "upstream", "", "IPv6 UDP upstream address.")
	fs.DurationVar(&cfg.SessionTimeout, "session-timeout", cfg.SessionTimeout, "Idle session timeout.")
	fs.IntVar(&cfg.ReadBufferSize, "read-buffer-size", cfg.ReadBufferSize, "UDP read buffer size in bytes.")
	fs.DurationVar(&cfg.WriteTimeout, "write-timeout", cfg.WriteTimeout, "UDP write timeout.")
	fs.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "Log level: debug, info, warn, error.")
	fs.BoolVar(&cfg.MetricsEnabled, "metrics.enabled", cfg.MetricsEnabled, "Enable metrics and debug HTTP server.")
	fs.StringVar(&cfg.MetricsListen, "metrics.listen", cfg.MetricsListen, "Metrics and debug HTTP listen address.")
	fs.BoolVar(&cfg.MetricsSessionLabels, "metrics.session-labels", cfg.MetricsSessionLabels, "Expose high-cardinality per-session Prometheus labels.")
	fs.IntVar(&cfg.MaxSessions, "max-sessions", cfg.MaxSessions, "Maximum active sessions. Zero means unlimited.")
	fs.BoolVar(&version, "version", false, "Print version and exit.")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if version {
		fmt.Println(build.String())
		return 0
	}
	cfg.Listen = []string(listens)
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		return 2
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slogLevel(cfg.LogLevel)}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	registry := metrics.New(cfg.MetricsSessionLabels, nil)
	server, err := relay.New(cfg, logger, registry)
	if err != nil {
		logger.Error("server initialization failed", "error", err)
		return 1
	}
	registry.SetSnapshotFunc(server.Snapshots)

	var metricsServer *http.Server
	var metricsListener net.Listener
	metricsErr := make(chan error, 1)
	if cfg.MetricsEnabled {
		mux := http.NewServeMux()
		mux.Handle("/metrics", registry.Handler())
		mux.Handle("/debug/sessions", metrics.DebugSessionsHandler(server.Snapshots))
		metricsListener, err = net.Listen("tcp", cfg.MetricsListen)
		if err != nil {
			logger.Error("metrics server bind failed", "listen", cfg.MetricsListen, "error", err)
			return 1
		}
		metricsServer = &http.Server{
			Addr:              cfg.MetricsListen,
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
		}
	}

	logger.Info("starting udp46",
		"listen", cfg.Listen,
		"upstream", cfg.Upstream,
		"resolved_upstream", server.UpstreamAddr(),
		"session_timeout", cfg.SessionTimeout.String(),
		"read_buffer_size", cfg.ReadBufferSize,
		"write_timeout", cfg.WriteTimeout.String(),
		"metrics_enabled", cfg.MetricsEnabled,
		"metrics_listen", cfg.MetricsListen,
		"metrics_session_labels", cfg.MetricsSessionLabels,
		"max_sessions", cfg.MaxSessions,
	)
	if err := server.Start(ctx); err != nil {
		logger.Error("server start failed", "error", err)
		if metricsListener != nil {
			_ = metricsListener.Close()
		}
		return 1
	}
	if metricsServer != nil {
		go func() {
			logger.Info("metrics server started", "listen", metricsListener.Addr().String())
			if err := metricsServer.Serve(metricsListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				metricsErr <- err
			}
		}()
	}

	exitCode := 0
	select {
	case <-ctx.Done():
	case err := <-metricsErr:
		logger.Error("metrics server failed", "error", err)
		exitCode = 1
	}
	logger.Info("shutdown requested")
	server.Close()
	if metricsServer != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := metricsServer.Shutdown(shutdownCtx); err != nil {
			logger.Warn("metrics server shutdown failed", "error", err)
		}
	}
	server.Wait()
	logger.Info("shutdown complete")
	return exitCode
}

func slogLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
