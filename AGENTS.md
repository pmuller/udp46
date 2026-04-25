# AGENTS.md

Instructions for future agents working on this repository.

## Project Snapshot

`udp46` is a payload-blind IPv4-to-IPv6 UDP relay written in Go. Its core invariant is one dedicated connected IPv6 upstream UDP socket per unique `(listener, IPv4 client address, IPv4 client port)` session. Do not replace this with a shared upstream socket.

The daemon must not parse, inspect, log, authenticate, decrypt, or otherwise understand UDP payloads. It is intended for protocols such as WireGuard, QUIC, and game UDP traffic where the relay only preserves datagram flow.

## Current Design Choices

- Standard library only. Do not add dependencies unless there is a clear payoff.
- CLI config only. No config file support yet.
- Metrics/debug HTTP is disabled by default.
- `/metrics` and `/debug/sessions` are enabled together by `--metrics.enabled`.
- Per-session Prometheus labels are opt-in via `--metrics.session-labels`.
- Sessions are in memory only. Restart drops mappings.
- Release workflow builds `.tar.gz` archives for all targets and `.deb` packages for Linux. Do not add more package formats unless explicitly requested.
- License is MIT. Keep `LICENSE` in release artifacts and package docs.

## Commands

The default Go cache may be read-only in this environment. Use writable caches under `/tmp`:

```sh
env GOCACHE=/tmp/udp46-go-cache GOMODCACHE=/tmp/udp46-go-mod go test ./...
env GOCACHE=/tmp/udp46-go-cache GOMODCACHE=/tmp/udp46-go-mod go test -race ./...
env GOCACHE=/tmp/udp46-go-cache GOMODCACHE=/tmp/udp46-go-mod go vet ./...
env GOCACHE=/tmp/udp46-go-cache GOMODCACHE=/tmp/udp46-go-mod go build -o /tmp/udp46 ./cmd/udp46
test -z "$(gofmt -l cmd internal)"
ruby -e 'require "yaml"; ARGV.each { |f| YAML.load_file(f); puts f }' .github/workflows/ci.yml .github/workflows/release.yml lefthook.yml
```

Run `gofmt -w cmd internal` after Go edits.

## Changelog Discipline

Keep `CHANGELOG.md` current. Any user-visible behavior change, CLI flag change, metric/debug output change, packaging/release change, documentation-significant operational change, or bug fix must update the `[Unreleased]` section in the same commit.

Before creating a release tag:

- move relevant `[Unreleased]` entries into a versioned section such as `## [0.2.0] - YYYY-MM-DD`;
- leave a fresh `[Unreleased]` section with `- No changes yet.`;
- make sure `docs/release.md` still describes the actual workflow;
- commit the changelog update before tagging.

## Networking And Concurrency Pitfalls

These have already produced review findings; do not regress them:

- Recheck `Server.closed` while holding `Server.mu` before publishing a newly dialed session.
- Recheck `--max-sessions` while holding `Server.mu` immediately before publishing a session.
- Idle expiry must revalidate session activity under the session lock before setting `closed=true`.
- Client activity is refreshed before writing upstream so expiry does not close a session while a fresh client packet is in flight.
- Metrics histogram snapshots must deep-copy the `counts` slice before unlocking the registry.
- Metrics HTTP bind errors are startup failures. Bind synchronously before starting the relay.
- Asynchronous metrics server failures must deterministically return exit code `1`.

## Test Expectations

Local UDP tests must not require internet access or root. IPv6 loopback tests may skip if `::1` is unavailable.

Coverage should stay focused on:

- config validation;
- session-key behavior across listeners and clients;
- IPv4 client to IPv6 upstream relay and reply demux;
- idle expiry and refresh behavior;
- metrics and debug endpoint behavior;
- graceful shutdown.

Always run race tests after touching relay, metrics, or shutdown code.

## Documentation Rules

Use only documentation-safe examples:

- IPv4: `192.0.2.0/24`, `198.51.100.0/24`, `203.0.113.0/24`
- IPv6: `2001:db8::/32`
- hostnames: `example.net`, `example.com`

Do not add real operator IPs, hostnames, DNS zones, or internal infrastructure names to examples, tests, fixtures, docs, logs, or screenshots.

Keep docs aligned with implementation:

- release docs should mention only artifacts the workflow actually builds;
- metrics disabled by default;
- dynamic upstream DNS requires restart;
- session state is memory-only;
- WireGuard clients should use `PersistentKeepalive = 25`.

## Release Notes

Version metadata is embedded with:

```sh
-X github.com/pmuller/udp46/internal/build.Version=...
-X github.com/pmuller/udp46/internal/build.Commit=...
-X github.com/pmuller/udp46/internal/build.Date=...
```

`udp46 --version` and `udp46_build_info` must expose the same metadata.
