// Package testdb provides testing helpers that spin up Postgres
// containers via testcontainers-go and return ready-to-use *db.DB
// handles. Two surfaces:
//
//   - [Spin] — single-node Postgres for the typical integration test.
//     Replaces ~30 lines of TestMain + initContainer + Connect
//     boilerplate that every kit subpackage with a Postgres-backed
//     store currently duplicates.
//
//   - [SpinCluster] — primary + N replicas with streaming
//     replication, returning a [Cluster] that exposes the Primary,
//     per-replica direct handles, AND a Multi *db.DB pre-wired with
//     [db.Config.ReadURLs] spanning every replica. Use to test code
//     that depends on the kit's multi-replica routing (PR #138) or to
//     reproduce production-like read-after-write timing issues.
//
//   - [BootCluster] — same wiring as SpinCluster but without the
//     [*testing.T] coupling: returns the [Cluster] + a teardown
//     closure the caller owns. Use from `TestMain` to share one
//     cluster across an entire test binary — ~15-30s boot paid once
//     instead of per test. Caller takes responsibility for
//     cross-test isolation (TRUNCATE between tests, watch WAL state).
//
// Spin / SpinCluster register cleanup with t.Cleanup so callers
// never have to remember Close()/Terminate() and skip themselves
// under `go test -short`. BootCluster runs unconditionally — TestMain
// callers should guard with `if testing.Short() { os.Exit(m.Run()) }`
// before calling it. A Docker daemon is required for any
// container-backed test.
//
// # Image policy
//
// Single-node uses the official `postgres:16-alpine` image (~80MB).
// Cluster uses `bitnamilegacy/postgresql:16` (~600MB) because
// Bitnami's image carries env-driven streaming-replication wiring out
// of the box — implementing the same dance against the official
// image would mean shell-scripting pg_basebackup + recovery.conf
// inside the test helper. The `bitnamilegacy` namespace is the
// community fallback for Bitnami's free public images after the
// upstream `bitnami/` namespace was removed in late 2025.
//
// Override either image via [WithImage] / [WithClusterImage] when
// your CI needs to mirror a specific version.
package testdb
