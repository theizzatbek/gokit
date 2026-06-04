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
// Both helpers register cleanup with t.Cleanup so callers never have
// to remember Close()/Terminate(). Both skip under `go test -short`
// (a Docker daemon is required for any container-backed test).
//
// # Image policy
//
// Single-node uses the official `postgres:16-alpine` image (~80MB).
// Cluster uses `bitnami/postgresql:16` (~600MB) because Bitnami's
// image carries env-driven streaming-replication wiring out of the
// box — implementing the same dance against the official image
// would mean shell-scripting pg_basebackup + recovery.conf inside
// the test helper.
//
// Override either image via [WithImage] / [WithClusterImage] when
// your CI needs to mirror a specific version.
package testdb
