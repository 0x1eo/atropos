# Atropos

Automated remediation service. When [Lachesis](https://github.com/0x1eo/lachesis) detects entropy above threshold, Atropos executes the "cut" - isolate, pause, or reset the offending node.

Named after the Greek Fate who cuts the thread of life. This one cuts network tunnels and reverts snapshots.

## Install

```bash
go mod tidy
go build
```

## Usage

```bash
# start with default policy
./atropos

# custom policy file
./atropos -policy /etc/atropos/policy.yaml

# with HMAC secret (recommended)
ATROPOS_HMAC_SECRET=your-secret ./atropos
```

Default port is `:8443`.

## Policy

Edit `atropos_policy.yaml`:

```yaml
nodes:
  athena:
    host: "athena.local"
    port: 22
    user: "root"
    strategies:
      - threshold: 0.85
        action: vbox_revert_snapshot
        snapshot_name: "LAST_ORDERED_STATE"
      - threshold: 0.70
        action: docker_stop_all
```

Higher thresholds = more aggressive response.

## Actions

| Action | What it does |
|--------|--------------|
| `docker_pause_all` | Pause all containers |
| `docker_stop_all` | Stop all containers |
| `docker_kill_all` | Kill all containers |
| `ssh_isolate_network` | Run command via SSH (e.g., kill WireGuard) |
| `vbox_revert_snapshot` | Revert VM to snapshot |
| `vbox_poweroff` | Power off VM |

## Webhook

Lachesis sends entropy alerts:

```bash
PAYLOAD='{"node":"athena","entropy":0.87}'
SIG=$(echo -n "$PAYLOAD" | openssl dgst -sha256 -hmac "your-secret" | cut -d' ' -f2)
curl -X POST http://localhost:8443/api/v1/cut \
  -H "Content-Type: application/json" \
  -H "X-Lachesis-Signature: sha256=$SIG" \
  -d "$PAYLOAD"
```

## License

MIT
