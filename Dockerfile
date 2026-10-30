# CupStream server: the Go binary with the website embedded.

FROM node:24-alpine3.24 AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.27-alpine3.24 AS server
WORKDIR /server
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
COPY --from=web /web/dist/ internal/webui/dist/
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /cupstream ./cmd/cupstream

FROM alpine:3.24
RUN apk add --no-cache ca-certificates && adduser -D -H -u 10001 cupstream \
    && mkdir /data && chown cupstream /data
COPY --from=server /cupstream /usr/local/bin/cupstream
USER cupstream
ENV CUPSTREAM_DB=/data/cupstream.db
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/cupstream"]
