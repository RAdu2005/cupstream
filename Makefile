.PHONY: help dev-frontend dev-backend build deploy clean

help:
	@echo "Targets:"
	@echo "  make dev-frontend   Run Vite dev server (HMR)"
	@echo "  make dev-backend    Run Go backend in dev mode (no TLS, no embed)"
	@echo "  make build          Build the production single-binary (npm + go with embed)"
	@echo "  make deploy         Build and rsync to VPS (set VPS=user@host)"
	@echo "  make clean          Remove build artifacts"

dev-frontend:
	npm run dev

dev-backend:
	cd auth-svc && TLS=no PORT=9999 go run -tags dev .

build:
	npm run build
	cd auth-svc && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -trimpath -o auth-svc .

deploy: build
	@if [ -z "$$VPS" ]; then echo "Set VPS=user@host"; exit 1; fi
	rsync -avz auth-svc/auth-svc $$VPS:/tmp/auth-svc.new
	ssh $$VPS 'sudo install -m 0755 /tmp/auth-svc.new /usr/local/bin/auth-svc && sudo systemctl restart auth-svc && rm /tmp/auth-svc.new'

clean:
	rm -rf dist auth-svc/dist auth-svc/auth-svc
