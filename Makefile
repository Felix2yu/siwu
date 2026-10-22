BIN  := bin/siwu
MAIN := ./cmd/siwu

.PHONY: build dev test lint lint-strict fmt docker clean stage smoke

build:
	./scripts/build.sh

dev: ## 后端 :8788，前端另开终端：cd web && pnpm dev
	go run $(MAIN) serve --addr :8788 --data ./data

test:
	go test ./internal/...
	cd web && pnpm run test

lint:
	@test -z "$$(gofmt -l ./cmd ./internal)" || { echo "gofmt 未通过："; gofmt -l ./cmd ./internal; exit 1; }
	go vet ./...
	cd web && pnpm run check

lint-strict: lint
	@if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run; else echo "  （未安装 golangci-lint，跳过）"; fi

fmt:
	gofmt -w ./cmd ./internal
	cd web && pnpm exec prettier --write .

docker: build
	docker build -t siwu:dev .

stage:
	./scripts/build.sh --stage-only

smoke: build
	./scripts/smoke.sh

clean:
	rm -rf bin/* web/dist/*
	rm -rf internal/web/dist/*
	touch internal/web/dist/.gitkeep
