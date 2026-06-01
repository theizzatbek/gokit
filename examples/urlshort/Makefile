.PHONY: up down test \
        run-api run-counter run-enricher \
        build-api build-counter build-enricher

# Local infra (Postgres + NATS + Redis) for all three services.
up:
	docker compose up -d
	@sleep 2

down:
	docker compose down -v

# Run each service in its own terminal. urlshort-api boots first
# since it owns the migrations.
run-api:
	cd urlshort-api && CONFIGS_DIR=configs go run .

run-counter:
	cd urlshort-counter && CONFIGS_DIR=configs go run .

run-enricher:
	cd urlshort-enricher && CONFIGS_DIR=configs go run .

build-api:
	cd urlshort-api && go build -o ../bin/urlshort-api .

build-counter:
	cd urlshort-counter && go build -o ../bin/urlshort-counter .

build-enricher:
	cd urlshort-enricher && go build -o ../bin/urlshort-enricher .

test:
	go test -race -count=1 ./...
