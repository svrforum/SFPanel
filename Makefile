.PHONY: build dev dev-api dev-web lint clean ci

# Version metadata is injected via ldflags so `make build` matches the artifact
# that goreleaser ships in CI. Without this the local binary always reported
# whatever string was hard-coded in cmd/sfpanel/main.go (was 0.11.1) regardless
# of the git tag, making "sfpanel version" useless for diagnosing installs.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

# 프론트엔드 빌드 후 Go 바이너리 빌드
# - npm ci (not install) keeps lockfile resolution deterministic, matching CI.
# - CGO_ENABLED=0 mirrors goreleaser; the modernc.org/sqlite driver is pure Go
#   so cgo is never required and turning it off makes the local binary linked
#   the same way as the released one.
build:
	cd web && npm ci && npm run build
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -trimpath -o sfpanel ./cmd/sfpanel

# 개발 모드 - API 서버
dev-api:
	go run ./cmd/sfpanel

# 개발 모드 - 프론트엔드 (핫 리로드)
dev-web:
	cd web && npm run dev

# 린트
lint:
	golangci-lint run ./...
	cd web && npm run lint

# 정리
clean:
	rm -f sfpanel
	rm -rf web/dist

# 테스트
test:
	go test ./internal/... -v -count=1

# 테스트 커버리지
test-coverage:
	go test ./internal/... -coverprofile=coverage.out
	go tool cover -func=coverage.out | tail -1

# CI - 로컬에서 전체 파이프라인 실행
ci: lint test build
