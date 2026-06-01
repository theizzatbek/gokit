# cmd/kit

`kit` is the gokit operator CLI — schema migrations, Ed25519 key
generation, API-key minting, transactional-outbox inspection. One
binary that wraps the kit's library entry points for ops use.

## Install

```bash
go install github.com/theizzatbek/gokit/cmd/kit@latest
```

## Commands

### `kit version`

```bash
kit version
```

Prints the binary version + VCS revision from `runtime/debug.ReadBuildInfo`.

### `kit migrate`

Wraps [`db/migrate`](../../db/migrate/README.md). Useful as a
pre-deployment init step in K8s when migrations are slow enough
that running them at app start would balloon pod-startup time.

```bash
# Apply pending migrations from ./migrations/.
kit migrate up --dir migrations/ --dsn postgres://...

# Roll back the 2 most recent.
kit migrate down --steps 2 --dir migrations/

# Inspect applied / pending status.
kit migrate status --dir migrations/

# Print just the current version (or empty for "none applied").
kit migrate version
```

DSN may also come from the `DATABASE_URL` env. Migrations follow the
`NNNN_name.sql` + optional `NNNN_name.down.sql` convention.

### `kit auth keygen`

```bash
kit auth keygen --kid k1 > keys.pem
```

Prints PKCS8 Ed25519 private + SPKI public PEMs to stdout. Pipe to
a file and split into the env vars your service expects (the kit
convention: `AUTH_PRIVATE_KEY` for the private half).

### `kit auth apikey new`

Mints a fresh API key tied to a subject + scopes + role,
HMAC-SHA256s with the kit secret, INSERTs through
[`auth/apikeypg`](../../auth/apikeypg/README.md), prints the plain
key ONCE.

```bash
export API_KEY_HASH_SECRET=$(openssl rand -hex 32)

kit auth apikey new \
    --subject svc-orders \
    --scopes orders:read,orders:write \
    --role service \
    --expires-in 90d \
    --description "issued by admin@example.com on 2026-06-01" \
    --dsn postgres://...

# Output:
# # --- API key (printed ONCE — copy it now) ---
# kit_g7v2y...
#
# # id:          ec79f4a0-...
# # subject:     svc-orders
# # scopes:      orders:read,orders:write
# # role:        service
# # expires_at:  2026-09-01T09:11:24Z
```

The `kit_` prefix lets callers grep the key out of logs cleanly.
The plain key is never persisted server-side; only its HMAC lives
in the `auth_api_keys` table. Lose the plain key and the operator
has to mint a new one.

### `kit outbox status`

```bash
kit outbox status --dsn postgres://...

# Output:
# pending:        12
# oldest_pending: 2026-06-01T09:05:11Z (1m37s ago)
# with_retries:   3
# max_attempts:   8
#
# recent failures:
#   attempts=8 type=orders.created err=nats: jetstream timeout
#   attempts=3 type=orders.updated err=nats: ack timeout
#   ...
```

First port of call when `/readyz` reports the outbox check failing.
Shows queue depth, the age of the oldest pending row, the top-5
most-attempted failed rows with their error messages.

## DSN format

All DB-bound commands accept `--dsn postgres://user:pw@host:port/db?sslmode=disable`
or read `DATABASE_URL` env. Both flag styles work — env wins when
both are unset (i.e. the flag is empty).

## Why no cobra / urfave/cli

Subcommand dispatch lives in plain stdlib `flag`. Adding a CLI
framework would pull in a dependency tree that's larger than the
CLI itself. The trade-off: no auto-generated tab-completion or
fancy help, but startup is instant and the binary stays under 10
MiB.

## See also

- [`db/migrate`](../../db/migrate/README.md) — the library wrapped by `kit migrate`.
- [`auth/apikeypg`](../../auth/apikeypg/README.md) — the KeyStore `kit auth apikey new` inserts into.
- [`db/outbox`](../../db/outbox/README.md) — the table `kit outbox status` reads.
