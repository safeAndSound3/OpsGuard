# OpsGuard

OpsGuard is a monitoring and collection management platform built with a React + TypeScript frontend and a Go API. The local entry point is `http://localhost:3000`; Vite proxies `/api` and `/health` requests to the Go API.

## Architecture

- `frontend/`: React + Vite interface
- `backend/`: Go API, collection rules, and MySQL metric export
- Remote database: the `opsguard` database on Alibaba Cloud RDS

The project no longer uses Docker Compose or a local MySQL server. The application connects to the remote RDS with the restricted `opsguard_app` account. Business data, data sources, alerts, and metric samples share the same project database.

## Local Development

Use Node.js 22+ and Go 1.22+. Database settings are loaded from `backend/.env`, which is intentionally excluded from Git. Use `backend/.env.example` as the configuration template.

Start the API:

```powershell
cd backend
go run ./cmd/server
```

Start the web application:

```powershell
cd frontend
npm ci
npm run dev
```

Open [http://localhost:3000](http://localhost:3000).

## Build

```powershell
cd frontend
npm run typecheck
npm run build

cd ..\backend
go build -trimpath -ldflags="-s -w" -o bin\opsguard.exe ./cmd/server
```

The frontend build splits the React runtime, disables sourcemaps, and skips compressed-size reporting. The backend build removes debug paths and symbol data to reduce the binary size.

## Development Rules

- Browser code uses only relative `/api` calls; it never exposes the RDS address or credentials.
- Update `backend/.env.example` when adding a server configuration. Keep real values only in `backend/.env`.
- Backend schema initialization creates application tables in the remote `opsguard` database as needed.
