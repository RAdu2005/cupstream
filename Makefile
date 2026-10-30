# Tool versions are pinned in mise.toml; commands run through `mise x` when
# mise is installed, so the shell does not need mise activated.
MISE := $(shell command -v mise >/dev/null 2>&1 && echo "mise x --")
GO_TOOL := $(MISE) go tool -modfile=tools/go.mod
# The commit the binary reports in /healthz ("-dirty" with local changes).
VERSION ?= $(shell git describe --always --dirty --abbrev=40 --exclude='*' 2>/dev/null || echo dev)

.PHONY: help dev build test test-server test-web generate check-generated images clean

help:
	@echo "make dev               MediaMTX (Docker) + Go server + Vite on http://localhost:5173"
	@echo "make build             bin/cupstream with the website embedded"
	@echo "make test              Go and website tests"
	@echo "make generate          sqlc queries and TypeScript API types"
	@echo "make check-generated   fail if generated code is out of date"
	@echo "make images            build the cupstream and MediaMTX Docker images"

dev:
	$(MISE) scripts/dev.sh

build:
	cd web && $(MISE) npm ci && $(MISE) npm run build
	find server/internal/webui/dist -mindepth 1 ! -name .gitkeep -delete
	cp -r web/dist/. server/internal/webui/dist/
	cd server && CGO_ENABLED=0 $(MISE) go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o ../bin/cupstream ./cmd/cupstream

test: test-server test-web

test-server:
	cd server && $(MISE) go vet ./... && $(MISE) go test -race ./...

test-web:
	cd web && $(MISE) npm run typecheck && $(MISE) npm test

generate:
	cd server && $(MISE) sqlc generate && $(GO_TOOL) tygo generate

check-generated: generate
	cd server && $(MISE) sqlc vet
	git diff --exit-code -- server/internal/store web/src/api/types.gen.ts

images:
	docker compose build

clean:
	rm -rf bin web/dist
	find server/internal/webui/dist -mindepth 1 ! -name .gitkeep -delete
