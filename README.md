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

### Transit-Aware Node Cleanup

The scheduled cleanup rule “中转失败才清理” first runs a fast connection check.
If it fails and the node's existing IP score is strictly greater than the
configured threshold (85 by default, configurable from 0 to 100), cleanup saves
`trans-proxy` as the node's front proxy and retries the fast check. Nodes with no
score or a score at or below the threshold do not receive this retry.
Only non-subscription groups can be selected as cleanup sources. Scheduled
cleanup also skips subscription groups referenced by older saved rules.

A successful fast check is followed by a real connection check using the node's
current front proxy. Failure of either final check removes the node from its
source group, optionally moving it into the configured recycle group. The saved
front proxy remains on the node even if the retry fails. The `trans-proxy`
outbound must exist and must not use the node itself.

### Destructive Actions

The UI asks for confirmation before deleting subscriptions, groups, nodes,
outbounds, routing rules, or custom inbounds and built-in proxy nodes. Cancelling
does not submit a request or change the selection. Subscription and group
deletion leave independent node copies in other groups untouched.

Routing-rule imports replace all existing rules rather than appending, so the
confirmation recommends exporting a backup first. Deleting automatic copy or
cleanup rules takes effect only after saving settings. Account and listener
removal inside an editor changes its draft only; clearing logs affects only the
current display.

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

### Group Duplicate Maintenance

Copies, moves, automatic copies and subscription group rebuilds compare the
complete stored connection configuration, ignoring display names and probe/IP
metadata. Credentials, TLS/transport options and front proxies remain part of
the identity. Deduplication applies within each group only: identical connection
configurations are allowed in different groups, but each group owns an
independent node record. Copies always clone; the shared-reference copy mode
has been removed. Duplicate moves are skipped and remain in the source group.

Preview existing group duplicates without changing the database:

```bash
cd service
CGO_ENABLED=0 go run ./cmd/dedupe-groups --db /path/to/proxystation.db
```

Apply the cleanup with a new consistent SQLite backup, including WAL data:

```bash
CGO_ENABLED=0 go run ./cmd/dedupe-groups --db /path/to/proxystation.db \
  --apply --backup /path/to/backups/proxystation-before-dedupe.db
```

The tool removes group references only, preserves node records and indexes,
prefers active nodes, and atomically retargets equivalent active references when
necessary. Backups use owner-only permissions and are never overwritten.
For older databases affected by subscription-index shifts, include
`--repair-subscription-refs` in both preview and apply commands. This restores
subscription-generated group references from their stable subscription IDs
before comparing content, without guessing from node names.
After offline maintenance, refresh the running service's group cache or restart
it. Deploy the updated application to prevent older versions from reintroducing
duplicates.

To migrate historical cross-group shared nodes into independent clones, add
`--isolate-group-nodes` to the preview and apply commands. Include
`--repair-subscription-refs` as well if the database has stale subscription
indexes. The migration first removes duplicates within each group, then clones
shared node records and subscription references in manual groups. It preserves
the subscription's own native nodes and retargets affected group outbounds to
equivalent clones. Original node records remain intact. Stop the old service
during the final backup/migration and deploy the new version before restarting,
so old background tasks cannot recreate shared nodes.
Manual-node deletion atomically adjusts remaining group and outbound indexes.
Inbound-generated nodes are synchronized by source using a dedicated index,
without rebuilding the whole manual-node collection or altering its clones.

## Storage

Configuration and node data are stored in `proxystation.db`, a SQLite database
running in WAL mode. Scalar settings live in a key/value table, while collections
(nodes, subscriptions, groups, inbound subscriptions) are stored one row per
element. Editing a single node therefore updates a single row instead of
rewriting the whole collection.

Upgrading from an older build that used bbolt is automatic: on first start the
legacy database is converted to SQLite and the original file is kept next to it
as `proxystation.db.bolt.bak`. No manual export/import is required.

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
