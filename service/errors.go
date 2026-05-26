package service

// Error codes returned in *errs.Error.Code from service.New.
// Subsystem-specific failures propagate unchanged from the subpackages.
const (
	CodeAuthNeedsDB         = "service_auth_needs_db"
	CodeAuthInvalidKey      = "service_auth_invalid_key"
	CodeDBConnectFailed     = "service_db_connect_failed"
	CodeAPIMapLoadFailed    = "service_apimap_load_failed"
	CodeNATSConnectFailed   = "service_nats_connect_failed"
	CodeHTTPCNewFailed      = "service_httpc_new_failed"
	CodeOpenAPIMountFailed  = "service_openapi_mount_failed"
	CodeNATSMapNeedsNATS    = "service_natsmap_needs_nats"
	CodeNATSMapLoadFailed   = "service_natsmap_load_failed"
	CodeAPIMapYAMLNotFound  = "service_apimap_yaml_not_found"
	CodeNATSMapYAMLNotFound = "service_natsmap_yaml_not_found"
	CodeRoutesYAMLNotFound  = "service_routes_yaml_not_found"
	CodeOpenAPIYAMLParse    = "service_openapi_yaml_parse"
)
