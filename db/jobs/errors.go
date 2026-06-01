package jobs

// Stable error Code constants returned by the package.
const (
	// CodeNilDB — NewWorker / Schedule received a nil *db.DB.
	CodeNilDB = "jobs_nil_db"

	// CodeInvalidJobType — Schedule was called with an empty
	// jobType, or RegisterHandler with an empty name.
	CodeInvalidJobType = "jobs_invalid_job_type"

	// CodeHandlerNotRegistered — Worker dequeued a row whose type
	// has no registered handler. Row moves to state `failed` and
	// the worker logs at Warn so ops sees the missing wiring.
	CodeHandlerNotRegistered = "jobs_handler_not_registered"

	// CodePayloadEncode — Schedule failed to JSON-encode payload.
	CodePayloadEncode = "jobs_payload_encode_failed"

	// CodePayloadDecode — Worker failed to JSON-decode a row's
	// payload into the handler's T.
	CodePayloadDecode = "jobs_payload_decode_failed"

	// CodeInsertFailed — Schedule INSERT returned a DB error.
	CodeInsertFailed = "jobs_insert_failed"

	// CodeWorkerStarted — Start called twice on the same Worker.
	CodeWorkerStarted = "jobs_worker_started"

	// CodeSchemaApplyFailed — ApplySchema returned a DB error.
	CodeSchemaApplyFailed = "jobs_schema_apply_failed"
)
