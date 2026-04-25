# udp46

`udp46` is a small stateful UDP relay for exposing IPv6-only UDP services to IPv4-only clients. It listens on one or more IPv4 UDP sockets and forwards datagrams to one IPv6 upstream without parsing or terminating the application protocol.

The primary use case is an IPv4-only mobile client reaching a home WireGuard endpoint that is reachable only over IPv6:

```text
IPv4-only client
  -> dual-stack VPS IPv4 UDP listener
  -> IPv6-only home WireGuard endpoint
```

The relay is payload-blind. It does not hold WireGuard keys, decrypt traffic, authenticate peers, inspect QUIC, or understand any UDP protocol.

`udp46` is designed for the common dual-stack VPS case where lower-layer
IPv4/IPv6 translation is not practical: you have one normal public IPv4 address
and one normal public IPv6 address, but no routed IPv6 translation prefix.
Unlike SIIT/NAT46, `udp46` does not need a routed `/96` or proxy NDP. Unlike
many generic UDP proxies, it creates one upstream IPv6 socket per client session
so the upstream sees distinct source ports.

`udp46` is licensed under the MIT License. See [LICENSE](LICENSE).

## How It Works

For every unique tuple of client IPv4 address, client UDP port, and listener identity, `udp46` creates an in-memory session with a dedicated connected IPv6 UDP socket to the upstream.

```text
192.0.2.10:62000 -> udp46 0.0.0.0:51820
udp46 creates [2001:db8::5]:41001 -> [2001:db8:100::10]:51820

198.51.100.20:62000 -> udp46 0.0.0.0:51820
udp46 creates [2001:db8::5]:41002 -> [2001:db8:100::10]:51820
```

The upstream sees distinct source ports, which lets protocols such as WireGuard maintain separate peer endpoints.

This is transport plumbing only. Application-level routing, policy, and
authentication remain the responsibility of the upstream service.

## Install And Run

Build locally:

```sh
go build -o udp46 ./cmd/udp46
```

Run in the foreground:

```sh
./udp46 \
  --listen 0.0.0.0:51820 \
  --listen 0.0.0.0:443 \
  --upstream '[2001:db8:100::10]:51820' \
  --session-timeout 180s
```

With metrics and debug HTTP enabled:

```sh
./udp46 \
  --listen 0.0.0.0:51820 \
  --upstream '[2001:db8:100::10]:51820' \
  --metrics.enabled \
  --metrics.listen 127.0.0.1:9108
```

Configuration is currently CLI-only. Upstream DNS names are resolved at startup; dynamic DNS changes require a restart.

## Flags

- `--listen`: IPv4 UDP listen address. Repeat for multiple listeners.
- `--upstream`: IPv6 UDP upstream address or hostname and port.
- `--session-timeout`: idle session timeout. Default: `180s`.
- `--read-buffer-size`: UDP read buffer size. Default: `65536`.
- `--write-timeout`: UDP write timeout. Default: `5s`.
- `--log-level`: `debug`, `info`, `warn`, or `error`. Default: `info`.
- `--metrics.enabled`: enable `/metrics` and `/debug/sessions`. Default: disabled.
- `--metrics.listen`: metrics HTTP address. Default: `127.0.0.1:9108`.
- `--metrics.session-labels`: expose high-cardinality per-session Prometheus labels.
- `--max-sessions`: maximum active sessions. `0` means unlimited.
- `--version`: print embedded version metadata.

## WireGuard Example

The mobile client points at the VPS:

```ini
[Peer]
PublicKey = <server-public-key>
Endpoint = vps.example.net:51820
PersistentKeepalive = 25
AllowedIPs = 0.0.0.0/0, ::/0
```

The home WireGuard server remains on the IPv6-only home firewall, for example `[2001:db8:100::10]:51820`.

`AllowedIPs` controls routing inside the WireGuard tunnel. `udp46` only affects
the outer UDP transport between the IPv4-only client and the IPv6-only endpoint.

See [docs/wireguard.md](docs/wireguard.md).

## Session Persistence

Sessions are in-memory only. Restarting `udp46` drops all mappings.

For WireGuard, this causes a short interruption. Active clients recover on the next outbound packet. Idle clients recover on the next `PersistentKeepalive = 25` packet, after which WireGuard authenticates the packet and updates the peer endpoint.

Persistent session storage is a non-goal for v1.

## Metrics And Debug

Metrics are disabled by default. When enabled, `/metrics` exposes low-cardinality Prometheus metrics for listeners, sessions, datagrams, bytes, drops, errors, and session lifetime histograms.

`/debug/sessions` exposes JSON session details:

- listener address
- client IPv4 address and UDP port
- upstream local IPv6 address and UDP port
- upstream remote IPv6 address and UDP port
- created and last-activity timestamps
- packet and byte counters in both directions

Per-session Prometheus labels are disabled by default because they are high-cardinality. Enable `--metrics.session-labels` only for small deployments or temporary debugging.

## Security Model

`udp46` is payload-blind and does not log packet payloads. It is still an internet-facing UDP relay, so host firewalling matters.

Recommended production controls:

- bind only required IPv4 UDP ports;
- restrict sources with nftables when possible;
- expose metrics/debug only on loopback or a private management network;
- run as a dedicated unprivileged user;
- use systemd hardening.

Example nftables allowlist:

```nft
table inet filter {
  chain input {
    type filter hook input priority 0;
    policy drop;

    iif lo accept
    ct state established,related accept
    ip saddr { 192.0.2.0/24, 198.51.100.0/24 } udp dport 51820 accept
  }
}
```

Example systemd unit:

```ini
[Unit]
Description=udp46 IPv4-to-IPv6 UDP relay
After=network-online.target
Wants=network-online.target

[Service]
User=udp46
Group=udp46
ExecStart=/usr/local/bin/udp46 --listen 0.0.0.0:51820 --upstream [2001:db8:100::10]:51820
Restart=always
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
RestrictAddressFamilies=AF_INET AF_INET6
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE

[Install]
WantedBy=multi-user.target
```

## Non-Goals

`udp46` is not a VPN, NAT46/SIIT implementation, firewall, TLS or WireGuard terminator, TCP proxy, anonymity tool, or protocol parser. It does not implement persistent session state.

## Comparison

Jool and SIIT solve IP translation at a lower layer and are the right tools when
you have the routing primitives they need. Stateless SIIT/NAT46 typically needs
a routed IPv6 translation prefix, such as a `/96`, so translated source
addresses can route back to the translator. `udp46` is intentionally narrower:
it relays UDP datagrams between IPv4 clients and one IPv6 upstream while
preserving one upstream source port per client session.

Generic UDP proxies can forward packets, but many use one shared upstream socket. That breaks protocols such as WireGuard because multiple clients appear to the upstream as the same source endpoint. `udp46` creates a dedicated upstream socket per client tuple.

`socat` is useful for quick connectivity experiments, but it is not the target
operational model. `udp46` makes the session table explicit, exports per-client
state and counters, applies idle expiry and capacity limits deliberately, and
logs structured metadata without packet payloads. Those controls are what make
the relay debuggable as long-running infrastructure rather than a one-off
packet plumbing command.

## Development

Run checks:

```sh
go test ./...
go test -race ./...
go vet ./...
gofmt -w cmd internal
```

CI runs tests, race tests, vet, formatting checks, and a binary build.
