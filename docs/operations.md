# Operations

## Logs

`udp46` logs structured JSON to stdout. Under systemd:

```sh
journalctl -u udp46 -f
```

Useful events:

- startup configuration summary;
- upstream resolution result;
- listener start and stop;
- session creation;
- session close with reason and counters;
- socket read and write errors;
- graceful shutdown.

Packet payloads are never logged.

## Inspect Sessions

Enable metrics/debug HTTP:

```sh
udp46 \
  --listen 0.0.0.0:51820 \
  --upstream '[2001:db8:100::10]:51820' \
  --metrics.enabled \
  --metrics.listen 127.0.0.1:9108
```

Inspect active sessions:

```sh
curl -fsS --get 127.0.0.1:9108/debug/sessions
```

Expose metrics:

```sh
curl -fsS --get 127.0.0.1:9108/metrics
```

Keep the metrics listener on loopback or a private management network. The debug endpoint includes client addresses and ports.

## Key Metrics

- `udp46_sessions_active`: current active mappings.
- `udp46_sessions_created_total`: total mappings created.
- `udp46_sessions_expired_total`: mappings expired by idle timeout.
- `udp46_sessions_closed_total{reason}`: session close reasons.
- `udp46_datagrams_total{listener,direction}`: relayed datagrams.
- `udp46_bytes_total{listener,direction}`: relayed bytes.
- `udp46_drops_total{listener,direction,reason}`: dropped datagrams.
- `udp46_errors_total{listener,operation}`: runtime socket errors.
- `udp46_upstream_socket_open_errors_total{listener}`: failures opening per-session IPv6 sockets.
- `udp46_upstream_write_errors_total{listener}`: writes to the IPv6 upstream failed.
- `udp46_client_write_errors_total{listener}`: writes back to IPv4 clients failed.
- `udp46_session_duration_seconds`: session lifetime at close.
- `udp46_session_idle_seconds`: idle age at close.

`--metrics.session-labels` adds per-session metrics. Those labels include client address and port, so use them only for small deployments or temporary debugging.

## Troubleshooting No Handshake

Check the client target:

```sh
wg show
```

Verify the VPS is listening:

```sh
ss -ulpn | grep ':51820'
```

Check packets arriving on the VPS IPv4 listener:

```sh
tcpdump -ni any udp port 51820
```

Check packets leaving the VPS toward the IPv6 upstream:

```sh
tcpdump -ni any ip6 and udp port 51820
```

Check the home firewall allows the VPS IPv6 address to the WireGuard port:

```sh
nft list ruleset
```

Check active relay sessions:

```sh
curl -fsS --get 127.0.0.1:9108/debug/sessions
```

If `/debug/sessions` is empty while clients are sending traffic, packets are not reaching the IPv4 listener or source filtering is dropping them before `udp46`.

If sessions exist but the home server has no handshake, inspect IPv6 routing and firewall rules between the VPS and home endpoint.

## Firewall Checklist

- VPS accepts IPv4 UDP on the listen ports.
- VPS permits outbound IPv6 UDP to the upstream.
- Home firewall accepts IPv6 UDP from the VPS to the service port.
- Metrics/debug HTTP is restricted to loopback or management networks.
- Low ports use `CAP_NET_BIND_SERVICE` instead of running as root.
