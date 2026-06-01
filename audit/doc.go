// Package audit is an append-only audit-log primitive. Compliance
// frameworks (SOC2, HIPAA, PCI-DSS, financial regulations) require
// a tamper-evident record of every privileged action — who did
// what, to what, with what outcome, when, from where. This package
// gives services a typed, structured way to emit those events and a
// pluggable Store backend to persist them.
//
//	logger := audit.New(store, audit.Config{ServiceName: "tasks"},
//	    audit.WithHashChain())
//
//	// Typed convenience constructors:
//	_ = logger.Login(ctx, audit.Actor{Subject: "u-42", IP: c.IP()}, audit.Success)
//	_ = logger.Updated(ctx, actor, target, map[string]any{"plan": "pro"})
//	_ = logger.Denied(ctx, actor, target, "post.delete", "not_owner")
//
//	// Free-form:
//	_, _ = logger.Log(ctx, audit.Event{
//	    Action: "billing.invoice_downloaded",
//	    Actor:  actor, Target: target, Outcome: audit.Success,
//	    Metadata: map[string]any{"invoice_id": "inv-42"},
//	})
//
// Hash-chain (opt-in via [WithHashChain]) links each event to the
// previous one via SHA-256 — auditors can verify the chain end-to-end
// to prove no records were silently deleted or edited.
//
// Backends:
//
//   - audit/auditpg: Postgres-backed Store. Hash-chain Append
//     serializes through a db/lock advisory lock so concurrent
//     writers don't fork the chain.
//   - In-memory MemoryStore for tests + dev.
//
// Query interface lets admin tools filter by actor / action /
// target / time-range. Retention is the operator's responsibility:
// PurgeBefore drops events older than a deadline and is wired into
// a db/jobs cron in production deployments.
package audit
