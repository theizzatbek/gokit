package audit

// Stable error Code constants produced by this package.
const (
	// CodeNilStore — Logger was built without a Store. Returned
	// from New rather than panicking so caller can decide.
	CodeNilStore = "audit_nil_store"

	// CodeInvalidEvent — Event missing required fields (Action,
	// Outcome). Returned at Log time.
	CodeInvalidEvent = "audit_invalid_event"

	// CodeAppendFailed — Store.Append returned an error.
	CodeAppendFailed = "audit_append_failed"

	// CodeQueryFailed — Store.Query returned an error.
	CodeQueryFailed = "audit_query_failed"

	// CodePurgeFailed — Store-level retention sweep failed.
	CodePurgeFailed = "audit_purge_failed"

	// CodeChainBroken — Verify detected a hash mismatch — either
	// the hash was edited, an event was dropped, or the chain
	// state was lost on a backend that doesn't persist it.
	CodeChainBroken = "audit_chain_broken"
)
