.PHONY: up down migrate run test

up:
	docker compose up -d
	@sleep 2
	$(MAKE) migrate

down:
	docker compose down -v

migrate:
	PGPASSWORD=$$DB_PASSWORD psql -h $$DB_HOST -p $$DB_PORT -U $$DB_USER -d $$DB_NAME -f migrations/0001_init.sql

run:
	go run .

test:
	go test -race -count=1 .
