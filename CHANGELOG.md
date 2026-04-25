# Changelog

All notable user-facing changes to `udp46` are documented here.

This project follows semantic versioning. Update this file before creating or moving a release tag.

## [Unreleased]

- No changes yet.

## [0.1.0] - 2026-04-26

Initial release.

### Added

- Implemented the `udp46` foreground daemon and CLI.
- Added one or more IPv4 UDP listeners that relay to one IPv6 upstream.
- Added per-client in-memory sessions keyed by listener, IPv4 client address, and IPv4 client port.
- Added one dedicated connected IPv6 upstream UDP socket per session.
- Added idle session expiry, graceful shutdown, and per-session cleanup.
- Added structured JSON logging with `log/slog`.
- Added optional metrics/debug HTTP server with `/metrics` and `/debug/sessions`.
- Added low-cardinality Prometheus metrics by default when metrics are enabled.
- Added opt-in high-cardinality per-session Prometheus labels via `--metrics.session-labels`.
- Added build metadata through `udp46 --version` and `udp46_build_info`.
- Added integration tests for relay flow, reply demux, session reuse, distinct upstream ports, idle expiry, metrics, debug sessions, and shutdown.
- Added GitHub Actions CI for formatting, tests, race tests, vet, and build.
- Added tag-driven release workflow for cross-platform `.tar.gz` archives, Linux `.deb` packages, and checksums.
- Added MIT license.
- Added operator documentation for WireGuard, operations, release flow, systemd, nftables, metrics, and troubleshooting.
- Added project-local `AGENTS.md` for future maintenance context.

### Fixed

- Fixed relay tests to use default config values when constructing partial test configs.
- Fixed session publication races during shutdown and `--max-sessions` enforcement.
- Fixed idle expiry races that could close a freshly active session.
- Fixed metrics histogram snapshot races during concurrent scrapes.
- Fixed metrics server startup to fail non-zero when the HTTP listener cannot bind.
- Fixed asynchronous metrics server failures to return a deterministic non-zero exit status.
