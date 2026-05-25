package natsclient

// Error codes returned in *errs.Error.Code. These are part of the public API:
// downstream services may switch on them and they show up in slog output.
const (
	CodeConnectFailed        = "connect_failed"
	CodeAuthAmbiguous        = "auth_ambiguous"
	CodeJetStreamUnavailable = "jetstream_unavailable"
	CodeStreamNotFound       = "stream_not_found"
	CodeStreamOpFailed       = "stream_op_failed"
	CodeStreamConfigInvalid  = "stream_config_invalid"
	CodeConsumerOpFailed     = "consumer_op_failed"
	CodePublishFailed        = "publish_failed"
	CodeDecodeFailed         = "decode_failed"
	CodeMissingURL           = "missing_url"
	CodeInvalidNKey          = "invalid_nkey"
	CodeEncodeFailed         = "encode_failed"
)
