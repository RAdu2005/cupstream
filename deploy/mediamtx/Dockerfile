# Builds MediaMTX from CupStream's fork: an upstream release plus our patches.
# See https://github.com/RAdu2005/mediamtx/blob/cupstream/v1.21.2/CUPSTREAM.md

FROM golang:1.27-alpine3.24 AS build
ARG MEDIAMTX_REPO=https://github.com/RAdu2005/mediamtx.git
ARG MEDIAMTX_REF=v1.21.2-cupstream.4
RUN apk add --no-cache git
WORKDIR /src
RUN git clone --depth 1 --branch "$MEDIAMTX_REF" "$MEDIAMTX_REPO" .
RUN go generate ./... && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /mediamtx .

FROM alpine:3.24
RUN apk add --no-cache ca-certificates
COPY --from=build /mediamtx /usr/local/bin/mediamtx
COPY mediamtx.yml /etc/mediamtx/mediamtx.yml
ENTRYPOINT ["/usr/local/bin/mediamtx"]
CMD ["/etc/mediamtx/mediamtx.yml"]
