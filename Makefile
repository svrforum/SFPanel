.PHONY: build dev dev-api dev-web lint lint-errcheck clean ci appstore-catalog test test-coverage stub-dist

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
lint: lint-errcheck
	golangci-lint run ./...
	cd web && npm run lint

# 스코프드 errcheck — 보안·상태 변경에 민감한 패키지에서만 누락 에러를 잡는다.
# 전역 errcheck는 best-effort `_ =` 관용구가 많아 노이즈가 크므로 .golangci.yml
# 에선 꺼두고, 여기서만 별도 설정(.golangci-errcheck.yml)으로 돌린다.
lint-errcheck:
	golangci-lint run -c .golangci-errcheck.yml ./internal/cluster/... ./internal/api/... ./internal/feature/auth/... ./internal/db/...

# 정리
clean:
	rm -f sfpanel
	rm -rf web/dist

# 테스트 — cmd/ runs too (./internal/... alone silently skipped it). Explicit
# lists rather than ./... because web/node_modules ships a stray Go package.
# cmd/sfpanel imports the root embed package, which won't compile until
# web/dist exists — stub a minimal index.html when it's absent (a real
# `make build` leaves the genuine assets untouched).
stub-dist:
	@[ -f web/dist/index.html ] || (mkdir -p web/dist && echo '<!doctype html><title>test-stub</title>' > web/dist/index.html)

test: stub-dist
	go test ./cmd/... ./internal/... -v -count=1

# 앱스토어 카탈로그 번들 생성
appstore-catalog:
	go run ./cmd/appstore-catalog

# 테스트 커버리지
test-coverage: stub-dist
	go test ./cmd/... ./internal/... -coverprofile=coverage.out
	go tool cover -func=coverage.out | tail -1

# CI - 로컬에서 전체 파이프라인 실행
ci: lint test build
