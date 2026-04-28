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
- `docker-compose.yml`: example deployment definition

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

Build and run with Docker Compose:

```powershell
docker compose up -d --build
```

Default exposed ports:

- `2026`: Web UI
- `20260`: local SOCKS5 proxy
- `20261`: local HTTP proxy

Persistent runtime data is mounted to `./data` in the compose example.

## Authentication

The container supports these environment variables:

- `PROXYSTATION_WEB_USERNAME`
- `PROXYSTATION_WEB_PASSWORD`

These values are applied at startup and stored in hashed form where applicable.

## Privacy Notes

- Do not commit `service/data/`, databases, logs, downloaded kernels, or local build artifacts.
- This repository already ignores common runtime and secret-bearing local files via `.gitignore`.

## License

This project is licensed under GNU GPL v3.0. See `LICENSE` for details.
