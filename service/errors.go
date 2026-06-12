package service

// Error codes returned in *errs.Error.Code from service.New.
// Subsystem-specific failures propagate unchanged from the subpackages.
const (
	CodeAuthNeedsDB                 = "service_auth_needs_db"
	CodeAuthInvalidKey              = "service_auth_invalid_key"
	CodeAuthInvalidAPIKeyHashSecret = "service_auth_invalid_apikey_hash_secret"
	CodeExtraValidatorRegister      = "service_extra_validator_register"
	CodeDBConnectFailed     = "service_db_connect_failed"
	CodeAPIMapLoadFailed    = "service_apimap_load_failed"
	CodeNATSConnectFailed   = "service_nats_connect_failed"
	CodeRedisConnectFailed  = "service_redis_connect_failed"
	CodeHTTPCNewFailed      = "service_httpc_new_failed"
	CodeOpenAPIMountFailed  = "service_openapi_mount_failed"
	CodeNATSMapNeedsNATS    = "service_natsmap_needs_nats"
	CodeNATSMapLoadFailed   = "service_natsmap_load_failed"
	CodeAPIMapYAMLNotFound  = "service_apimap_yaml_not_found"
	CodeNATSMapYAMLNotFound = "service_natsmap_yaml_not_found"
	CodeRoutesYAMLNotFound  = "service_routes_yaml_not_found"
	CodeOpenAPIYAMLParse    = "service_openapi_yaml_parse"

	// CodeOutboxNeedsDB — WithOutbox requested but Config.DB is empty.
	CodeOutboxNeedsDB = "service_outbox_needs_db"

	// CodeOutboxNeedsNATSMap — WithOutbox requested without a dispatcher
	// override AND without NATSMap (the default PublishFn target).
	CodeOutboxNeedsNATSMap = "service_outbox_needs_natsmap"

	// CodeOutboxStartFailed — outbox.Worker.Start returned an error
	// at boot time.
	CodeOutboxStartFailed = "service_outbox_start_failed"

	// CodeOutboxSchemaFailed — WithOutboxAutoSchema run failed.
	CodeOutboxSchemaFailed = "service_outbox_schema_failed"
)
