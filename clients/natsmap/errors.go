package natsmap

// Build-time error codes returned in *errs.Error.Code from Engine.Build
// (collected via errors.Join when multiple validation failures co-occur).
const (
	CodeAlreadyBuilt           = "natsmap_already_built"
	CodeNoEntries              = "natsmap_no_entries"
	CodeDuplicateSubscriber    = "natsmap_duplicate_subscriber"
	CodeDuplicatePublisher     = "natsmap_duplicate_publisher"
	CodeMissingName            = "natsmap_missing_name"
	CodeMissingSubject         = "natsmap_missing_subject"
	CodeInvalidMaxInFlight     = "natsmap_invalid_max_in_flight"
	CodeInvalidMaxDeliver      = "natsmap_invalid_max_deliver"
	CodeInvalidAckWait         = "natsmap_invalid_ack_wait"
	CodeInvalidBackoff         = "natsmap_invalid_backoff"
	CodeInvalidStartFrom       = "natsmap_invalid_start_from"
	CodeHandlerNotRegistered   = "natsmap_handler_not_registered"
	CodeHandlerUnknown         = "natsmap_handler_unknown"
	CodePublisherNotRegistered = "natsmap_publisher_not_registered"
	CodePublisherUnknown       = "natsmap_publisher_unknown"
	CodeSubscribeFailed        = "natsmap_subscribe_failed"
	CodeReadFile               = "natsmap_read_file"
	CodeParseYAML              = "natsmap_parse_yaml"
	CodeEnvVarUnset            = "natsmap_env_var_unset"
	CodeEnvVarMalformed        = "natsmap_env_var_malformed"
)

// Runtime error codes returned from Publish / PublishWithHeaders.
const (
	CodeUnknownPublisher      = "natsmap_unknown_publisher"
	CodePublisherTypeMismatch = "natsmap_publisher_type_mismatch"
	CodePublishFailed         = "natsmap_publish_failed"
)
