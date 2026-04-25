# WireGuard Over IPv6-Only Home Endpoint

This guide describes the common deployment where a home WireGuard server is reachable over IPv6, while some mobile clients are stuck on IPv4-only networks.

## Topology

```text
IPv4-only mobile client
  -> vps.example.net:51820 over IPv4
  -> udp46 on the VPS
  -> [2001:db8:100::10]:51820 at home over IPv6
```

The WireGuard server stays on the home firewall. The VPS runs only `udp46` and does not need WireGuard private keys.

`udp46` only bridges the outer UDP transport. WireGuard cryptography, peer
identity, tunnel routing, and access policy remain on the WireGuard endpoints.

## VPS Command

```sh
udp46 \
  --listen 0.0.0.0:51820 \
  --upstream '[2001:db8:100::10]:51820' \
  --session-timeout 180s
```

## Client Configuration

Point the client at the VPS name:

```ini
[Peer]
PublicKey = <server-public-key>
Endpoint = vps.example.net:51820
AllowedIPs = 0.0.0.0/0, ::/0
PersistentKeepalive = 25
```

`AllowedIPs` controls routes inside the WireGuard tunnel. It does not control
how the outer UDP packet reaches the WireGuard server. `udp46` only changes that
outer transport path.

`PersistentKeepalive = 25` is important for mobile clients and for recovery after a relay restart.

## Server Configuration

No special WireGuard relay configuration is required on the home server. It receives authenticated WireGuard packets from the VPS IPv6 address and a relay-chosen UDP source port.

The home firewall must allow UDP traffic from the VPS IPv6 address to the WireGuard port.

## Restart Behavior

`udp46` stores sessions only in memory. Restarting it drops the mappings.

Active clients recover on their next outbound WireGuard packet. Idle clients recover on the next persistent keepalive. After the packet reaches the home server, WireGuard authenticates it and updates the peer endpoint.

## DNS Behavior

The upstream hostname is resolved at startup. If `[2001:db8:100::10]` is represented by a dynamic DNS name, restart `udp46` after the DNS target changes.
