# OpsGuard

[中文](README.md) | English

OpsGuard is a locally deployable operations inspection and alert-handling platform. It does not replace Prometheus, Hadoop, Grafana, or other production systems. Instead, it connects to existing services and provides a unified view for data sources, alert rules, notifications, and essential operational metrics.

## Features

- Data sources: Prometheus, MySQL, SSH, and Hadoop / YARN.
- Alerting: synchronized Prometheus rules plus custom MySQL data, port, file, and SSH script checks.
- Notifications: separate active, alert, and recovery events, with unread state, duration, mark-all-read, and muted persistent alerts.
- Hadoop: running applications from ResourceManager; completed MapReduce applications from JobHistory; server-side filters, pagination, container logs, and deep links.
- Dashboards: collected MySQL and SSH metrics, plus MySQL slow-query and high-latency SQL details.
- Settings: global refresh interval and password management.

## Architecture

```text
Browser
  │
  ├─ React + TypeScript + Vite (frontend, :3000)
  │       └─ /api proxy
  │
  └─ Go HTTP API (backend, :8030)
          ├─ MySQL: platform configuration, notifications, metric samples
          ├─ Prometheus: metrics, rules, alerts
          ├─ Hadoop: ResourceManager / NodeManager / JobHistory
          └─ SSH: node metrics and custom check scripts
```

Platform data is isolated from monitored systems. Each Prometheus, Hadoop, SSH, and MySQL data source is queried within its own data-source ID boundary.

## Prerequisites

- Node.js 22+
- Go 1.22+
- MySQL 8+ for OpsGuard platform data

Prometheus, Hadoop, SSH, and external MySQL are optional integrations. Production credentials are never exposed to browser code.

## Configuration

Copy `backend/config/opsguard.conf.example` to `backend/config/opsguard.conf` and configure the platform MySQL connection:

```ini
HOST=127.0.0.1
PORT=8030
ENV=development

MYSQL_HOST=127.0.0.1
MYSQL_PORT=3306
MYSQL_DATABASE=opsguard
MYSQL_USER=opsguard_app
MYSQL_PASSWORD=change-me

# Used only when creating the admin account; remove after initialization
OPSGUARD_ADMIN_PASSWORD=change-this-initial-password
# Set to true when serving through HTTPS
SESSION_COOKIE_SECURE=false
SESSION_TTL=12h

# Verify SSH host keys in production
SSH_HOST_KEY_POLICY=strict
SSH_KNOWN_HOSTS_FILE=C:/opsguard/ssh_known_hosts
```

The backend initializes its tables on startup. An existing `admin` account is never overwritten by the bootstrap password; legacy plaintext user passwords are migrated to bcrypt hashes. Data-source credentials are encrypted with `OPSGUARD_ENCRYPTION_KEY`. `backend/config/opsguard.conf` is ignored by Git and must not contain committed credentials. Container and service deployments can point `OPSGUARD_CONFIG` at any absolute configuration-file path; environment variables take precedence over file values. For a cross-origin frontend, configure complete allowed origins with `CORS_ALLOWED_ORIGINS`; same-origin deployments need no CORS setting.

## Local Development

Start the API:

```powershell
cd backend
go run ./cmd/server
```

Start the frontend:

```powershell
cd frontend
pnpm install
pnpm dev
```

Open [http://127.0.0.1:3000](http://127.0.0.1:3000). Vite proxies `/api` and `/health` to `http://127.0.0.1:8030`.

## Build and Verify

```powershell
cd backend
go test ./internal/service ./internal/router
go build -o .\bin\opsguard.exe .\cmd\server

cd ..\frontend
pnpm build
```

## Hadoop Integration

Use the ResourceManager web endpoint when adding a Hadoop source, for example `http://hadoop-master:8088`. NodeManager and JobHistory addresses are optional and are used for logs and historical applications:

- NodeManager reads container logs that remain on a node.
- JobHistory reads aggregated logs and supplements completed MapReduce applications.

The Hadoop page uses server-side filtering and pages of 20 applications. ResourceManager supplies running applications. If JobHistory is unavailable, running applications remain available but completed history may be incomplete.

## Development Rules

- Browser code uses relative `/api` endpoints only and never exposes database addresses or credentials.
- Secrets, tokens, and real environment addresses stay in local environment configuration.
- Build output, logs, caches, and local tooling are excluded from the repository.
