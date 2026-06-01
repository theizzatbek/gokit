.PHONY: up down test \
        run-api run-publisher run-counter run-enricher \
        build-api build-publisher build-counter build-enricher

# Local infra (Postgres + NATS + Redis) for all four services.
up:
	docker compose up -d
	@sleep 2

down:
	docker compose down -v

# Run each service in its own terminal.
#
# Boot order recommendation:
#   1. run-api        — applies migrations, mounts HTTP at :3000
#   2. run-publisher  — drains outbox + exposes POST /publish at :3001
#   3. run-counter    — batched consumer of urlshort.link.visited
#   4. run-enricher   — consumer of urlshort.link.created
#
# api can start before publisher — it stages outbox rows and
# fire-and-forgets POSTs to publisher. The publisher catches up
# whenever it comes online.

run-api:
	cd urlshort-api && CONFIGS_DIR=configs go run .

run-publisher:
	cd urlshort-publisher && CONFIGS_DIR=configs ADDR=:3001 go run .

run-counter:
	cd urlshort-counter && CONFIGS_DIR=configs go run .

run-enricher:
	cd urlshort-enricher && CONFIGS_DIR=configs go run .

build-api:
	cd urlshort-api && go build -o ../bin/urlshort-api .

build-publisher:
	cd urlshort-publisher && go build -o ../bin/urlshort-publisher .

build-counter:
	cd urlshort-counter && go build -o ../bin/urlshort-counter .

build-enricher:
	cd urlshort-enricher && go build -o ../bin/urlshort-enricher .

test:
	go test -race -count=1 ./...
