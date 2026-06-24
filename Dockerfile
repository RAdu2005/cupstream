# syntax=docker/dockerfile:1

# ------------------------------------------------------------------
# Stage 1: Build frontend
# ------------------------------------------------------------------
FROM node:20-bookworm-slim AS frontend-builder
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
RUN npm run build

# ------------------------------------------------------------------
# Stage 2: Build Go backend
# ------------------------------------------------------------------
FROM golang:1.22-bookworm AS backend-builder
WORKDIR /app
COPY auth-svc/go.mod auth-svc/go.sum ./
RUN go mod download
COPY auth-svc/ ./
COPY --from=frontend-builder /app/auth-svc/dist ./dist
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w" -trimpath -o auth-svc .

# ------------------------------------------------------------------
# Stage 3: Runtime
# ------------------------------------------------------------------
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=backend-builder /app/auth-svc /usr/local/bin/auth-svc
COPY scripts/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh /usr/local/bin/auth-svc

EXPOSE 80 443
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["/usr/local/bin/auth-svc"]
