.PHONY: build dev dev-api dev-web lint clean

# 프론트엔드 빌드 후 Go 바이너리 빌드
build:
	cd web && npm install && npm run build
	go build -o sfpanel ./cmd/sfpanel

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
