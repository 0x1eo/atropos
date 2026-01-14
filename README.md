# Atropos

Automated remediation service with comprehensive monitoring, analytics, and policy enforcement. When [Lachesis](https://github.com/0x1eo/lachesis) detects entropy above threshold, Atropos executes the "cut" - isolate, pause, or reset the offending node.

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

# custom history directory
./atropos -history-dir /var/lib/atropos/history

# with HMAC secret (recommended)
ATROPOS_HMAC_SECRET=your-secret ./atropos
```

Default port is `:8443`.

## Features

### Core Remediation
- Execute automated cuts based on entropy thresholds
- Strategy escalation on critical action failure
- Support for Docker, VirtualBox, and SSH-based cuts
- Dry-run simulation for policy testing

### History & Logging
- Persistent cut history with gzip compression
- Automatic history retention (configurable)
- Cut record includes: timestamp, entropy, action, latency, success, error details

### Trend Analysis & Statistics
- Success/failure rates by node and action
- Mean Time To Remediation (MTTR) calculation
- Problematic node identification
- Timeline view of cut events
- Most frequently used actions

### Web Dashboard
- Real-time statistics overview
- Node-level metrics and trends
- Cut history with filtering
- Built-in dry-run testing interface
- Responsive design with dark mode

### Enhanced Policy Features
- **Time Windows**: Only execute cuts during specified hours
- **Rate Limiting**: Limit cuts per time period
- **Conditional Actions**: Define fallback strategies on failure
- **Escalation**: Automatic escalation to higher thresholds on critical failure

### Export & Reporting
- CSV export of cut history
- JSON export for programmatic access
- HTML report generation with statistics and breakdowns

### Correlation with Clotho
- Import Clotho audit reports
- Correlate audit failures with remediation actions
- Track remediation effectiveness
- Identify controls that trigger most cuts
- View unresolved findings

### Notifications
- Email alerts on cut execution
- Webhook notifications with custom headers
- Support for multiple notification channels
- Retry logic with configurable attempts

## Policy

Edit `atropos_policy.yaml`:

```yaml
meta:
  version: "1.0"
  last_reviewed: "2026-01-14"

server:
  listen_addr: ":8443"
  hmac_secret: "change-me-in-prod"

nodes:
  athena:
    host: "athena.local"
    port: 22
    user: "root"
    description: "Primary application server"
    # Only cut during business hours
    time_windows:
      - start: "09:00"
        end: "17:00"
    # Rate limit: max 3 cuts per hour
    rate_limit:
      max_cuts: 3
      window_minutes: 60
    strategies:
      # High entropy: revert VM
      - threshold: 0.85
        action: vbox_revert_snapshot
        snapshot_name: "LAST_ORDERED_STATE"
        critical: true
        # On failure, try network isolation
        on_failure: "ssh_isolate_network"
      # Medium entropy: pause containers
      - threshold: 0.70
        action: docker_pause_all

  borg:
    host: "borg.local"
    port: 22
    user: "root"
    description: "Message broker / API gateway"
    strategies:
      - threshold: 0.90
        action: ssh_isolate_network
        command: "systemctl stop wireguard@wg0"
        critical: true
      - threshold: 0.75
        action: docker_stop_all
```

### Time Windows
Restrict cuts to specific time windows:

```yaml
nodes:
  production:
    time_windows:
      - start: "09:00"
        end: "17:00"  # Business hours only
      - start: "00:00"
        end: "04:00"  # Allow maintenance window
```

### Rate Limiting
Limit the frequency of cuts per node:

```yaml
nodes:
  critical:
    rate_limit:
      max_cuts: 5
      window_minutes: 60  # Max 5 cuts per hour
```

### Conditional Actions
Define fallback strategies when primary action fails:

```yaml
strategies:
  - threshold: 0.85
    action: vbox_revert_snapshot
    on_failure: "ssh_isolate_network"  # Fallback if VM revert fails
```

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

## API Endpoints

### Cut Management
- `POST /api/v1/cut` - Execute cut (requires HMAC signature)
- `POST /api/v1/cut/dryrun` - Simulate cut without execution

### History & Statistics
- `GET /api/v1/cuts/history?limit=100` - List all cuts
- `GET /api/v1/cuts/history/:node?limit=100` - List cuts for specific node
- `GET /api/v1/cuts/:id` - Get specific cut details
- `GET /api/v1/stats` - Global statistics
- `GET /api/v1/stats/:node` - Node-level statistics

### Trends
- `GET /api/v1/trends?days=30` - Global trends (default: 30 days)
- `GET /api/v1/trends/:node` - Node-specific trends

### Correlation
- `POST /api/v1/correlation/import` - Import Clotho audit report
- `GET /api/v1/correlation/:node?hours=24` - Get correlations

### Exports
- `GET /api/v1/export/history.csv?limit=1000` - Export CSV
- `GET /api/v1/export/history.json?limit=1000` - Export JSON
- `GET /api/v1/export/report.html?limit=1000` - Generate HTML report

### Dashboard
- `GET /` or `/dashboard` - Web dashboard
- `GET /static/index.html` - Direct dashboard access

## Notification Configuration

Notifications can be configured via environment variables or config file:

### Email Notifications
```yaml
# atropos_notification.yaml
enabled: true
email:
  smtp_host: "smtp.example.com"
  smtp_port: 587
  smtp_user: "alerts@example.com"
  smtp_password: "password"
  from: "atropos@example.com"
  to:
    - "admin@example.com"
    - "ops@example.com"
```

### Webhook Notifications
```yaml
enabled: true
webhook:
  url: "https://hooks.example.com/atropos"
  headers:
    Authorization: "Bearer token123"
    X-Custom-Header: "value"
  retries: 3
```

Set environment variable:
```bash
ATROPOS_NOTIFICATIONS_CONFIG=/path/to/config.yaml ./atropos
```

## Clotho Correlation

Import Clotho audit reports to correlate failures with remediation:

```bash
# Import audit report
curl -X POST http://localhost:8443/api/v1/correlation/import \
  -H "Content-Type: application/json" \
  -d @clotho_audit_report.json

# Get correlations
curl http://localhost:8443/api/v1/correlation/athena?hours=24
```

Response includes:
- Effectiveness percentage
- Number of resolved findings
- Unresolved findings
- Controls triggering most cuts
- Time deltas between failures and remediation

## Dashboard

Access the web dashboard at `http://localhost:8443/`:

Features:
- Real-time statistics (total cuts, success rate, failed cuts, problematic nodes)
- Node breakdown with success rates and common actions
- Recent cut history
- Trend analysis visualization
- Dry-run testing interface
- Auto-refresh every 30 seconds

## Actions

| Action | What it does |
|--------|--------------|
| `docker_pause_all` | Pause all containers |
| `docker_stop_all` | Stop all containers |
| `docker_kill_all` | Kill all containers |
| `ssh_isolate_network` | Run command via SSH (e.g., kill WireGuard) |
| `vbox_revert_snapshot` | Revert VM to snapshot |
| `vbox_poweroff` | Power off VM |

## License

MIT
