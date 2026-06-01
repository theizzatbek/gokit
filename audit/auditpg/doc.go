// Package auditpg is the Postgres-backed audit.Store. Use it when
// the audit trail needs to survive process restarts and be queryable
// by admin tools.
//
//	store := auditpg.New(svc.DB)
//	_ = auditpg.ApplySchema(ctx, svc.DB)  // or via migration tool
//
//	logger, _ := audit.New(store, audit.Config{ServiceName: "tasks"},
//	    audit.WithHashChain())
//
// Hash-chain semantics: when [audit.WithHashChain] is enabled,
// Append serializes through a db/lock advisory lock (name
// "audit:" + service-name from Config). Two processes writing the
// same chain CANNOT fork it — the second writer blocks until the
// first commits.
//
// Schema lives in schema.sql; ApplySchema is idempotent. Most
// deployments include the DDL in their migration tool — the kit
// ships both.
package auditpg
