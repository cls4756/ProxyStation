# ===== Stage 1: Build frontend =====
FROM node:20-alpine AS frontend-builder
WORKDIR /app/gui
COPY gui/package*.json ./
RUN npm ci --silent
COPY gui/ ./
RUN npm run build

# ===== Stage 2: Build backend =====
FROM golang:1.21-alpine AS backend-builder
WORKDIR /app/service
COPY service/go.mod service/go.sum ./
RUN go env -w GOPROXY=https://goproxy.cn,direct
RUN go mod download
COPY service/ ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o proxystation .

# ===== Stage 3: Runtime =====
FROM debian:bookworm-slim
WORKDIR /app

ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates tzdata \
  && rm -rf /var/lib/apt/lists/*

ENV PROXYSTATION_WEB_USERNAME=
ENV PROXYSTATION_WEB_PASSWORD=

COPY --from=backend-builder /app/service/proxystation ./
COPY --from=frontend-builder /app/gui/dist ./gui/dist

VOLUME ["/app/data"]

EXPOSE 2026
EXPOSE 20260
EXPOSE 20261

ENTRYPOINT ["./proxystation", "--addr=0.0.0.0:2026", "--data=/app/data", "--gui=/app/gui/dist"]
