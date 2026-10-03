.PHONY: build test test-integration lint fmt run migrate-up migrate-down migrate-status seed db-up db-down docker-build spec-lint

build:
	go build ./...

test:
	go test ./...

# Requires TEST_DATABASE_URL (see .env.example). Resets that database.
test-integration:
	go test -count=1 ./...

lint:
	go vet ./...
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)

fmt:
	gofmt -w .

run:
	go run ./cmd/api

migrate-up:
	go run ./cmd/migrate up

migrate-down:
	go run ./cmd/migrate down

migrate-status:
	go run ./cmd/migrate status

# Loads sample treks (development only; refuses APP_ENV=production).
seed:
	go run ./cmd/seed

db-up:
	docker compose -f deploy/docker-compose.yml up -d postgres

db-down:
	docker compose -f deploy/docker-compose.yml down

docker-build:
	docker build -f deploy/Dockerfile -t travel-swipe-api .

spec-lint:
	npx --yes @redocly/cli@latest lint api/openapi.yaml
