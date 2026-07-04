# ProxyStation

ProxyStation is a web-based proxy management panel built with Go and Vue.
It provides a browser UI for managing nodes, subscriptions, routing rules,
local proxy inbounds, and runtime kernel downloads for Xray, sing-box, and V2Ray.

## Features

- Web UI for node, group, outbound, and subscription management
- Built-in proxy control for SOCKS5 and HTTP local inbounds
- Kernel download and update support for `xray`, `sing-box`, and `v2ray`
- Routing rule and custom inbound management
- Runtime logs with live streaming in the UI
- Docker-based deployment with persistent runtime data

## Project Structure

- `gui/`: Vue 3 frontend built with Vite
- `service/`: Go backend, engine management, config storage, and HTTP API
- `Dockerfile`: multi-stage build for frontend and backend
- `docker-compose.yml`: deployment definition using local `.env` variables
- `.env.example`: sample environment configuration for Docker Compose

## Local Development

### Frontend

```powershell
cd gui
npm install
npm run dev
```

### Backend

```powershell
cd service
go run . --addr=127.0.0.1:2026 --data=./data --gui=../gui/dist
```

If you want the backend to serve the built frontend:

```powershell
cd gui
npm run build

cd ..\service
go run . --addr=127.0.0.1:2026 --data=./data --gui=../gui/dist
```

## Docker

Create a local `.env` file from the example before starting Docker Compose:

```powershell
cp .env.example .env
```

Edit `.env` and set your local values, especially `PROXYSTATION_WEB_USERNAME`,
`PROXYSTATION_WEB_PASSWORD`, and `PROXYSTATION_DATA_DIR`. The real `.env` file is
ignored by Git and must not be committed.

Build and run with Docker Compose:

```powershell
docker compose up -d --build
```

The default Compose deployment uses host networking (`network_mode: host`), so
ProxyStation listens directly on the host ports configured by the application:

- `2026`: Web UI
- `20260`: local SOCKS5 proxy
- `20261`: local HTTP proxy
- `20262`: local SOCKS5 UDP proxy

If you prefer Docker bridge networking, comment out `network_mode: host` in
`docker-compose.yml` and uncomment the `ports` section. The optional published
host ports for bridge mode are documented in `.env.example`.

Persistent runtime data is mounted from `PROXYSTATION_DATA_DIR` to `/app/data` in
the container.

## Authentication

Docker Compose reads authentication settings from your local `.env` file:

- `PROXYSTATION_WEB_USERNAME`
- `PROXYSTATION_WEB_PASSWORD`

These values are applied at startup and stored in hashed form where applicable.

## Privacy Notes

- Do not commit `.env`, `service/data/`, databases, logs, downloaded kernels, or local build artifacts.
- This repository already ignores common runtime and secret-bearing local files via `.gitignore`.

## License

This project is licensed under the Apache License 2.0. See `LICENSE` for details.
