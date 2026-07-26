package svckit

// Stable Code constants for the core. Mod-specific codes live in the
// mod packages — the core does not know about them and does not list
// them here.
const (
	// CodeAuthNeedsDB — Config.Auth.PrivateKeyPEM is set but DB is not.
	CodeAuthNeedsDB = "svckit_auth_needs_db"

	// CodeTLSConfigIncomplete — exactly one of TLS_CERT_FILE / TLS_KEY_FILE is set.
	CodeTLSConfigIncomplete = "svckit_tls_config_incomplete"

	// CodeModDuplicate — two mods returned the same Name(), or Name() is empty.
	CodeModDuplicate = "svckit_mod_duplicate"

	// CodeModSetupFailed — a mod returned an error from its Setup phase.
	CodeModSetupFailed = "svckit_mod_setup_failed"

	// CodeModBuildFailed — a mod returned an error from its Build phase.
	CodeModBuildFailed = "svckit_mod_build_failed"

	// CodeModWireFailed — a mod returned an error from its Wire phase.
	CodeModWireFailed = "svckit_mod_wire_failed"

	// CodeDBConnectFailed — db.Connect could not connect.
	CodeDBConnectFailed = "svckit_db_connect_failed"

	// CodeAuthInvalidKey — auth.LoadKeysFromPEM / auth.New rejected the key.
	CodeAuthInvalidKey = "svckit_auth_invalid_key"

	// CodeAuthInvalidAPIKeyHashSecret — Config.Auth.APIKeyHashSecret failed
	// to decode as base64, or decoded to fewer than the required bytes.
	// Set by decodeAPIKeyHashSecret, ported from service/apikey_secret.go
	// in Task 5; declared here now so the copied file compiles unmodified.
	CodeAuthInvalidAPIKeyHashSecret = "svckit_auth_invalid_apikey_hash_secret"

	// CodeHTTPCNewFailed — httpc.New returned an error.
	CodeHTTPCNewFailed = "svckit_httpc_new_failed"

	// CodeRoutesYAMLNotFound — routes.yaml is enabled but the file is missing.
	CodeRoutesYAMLNotFound = "svckit_routes_yaml_not_found"

	// CodeOpenAPIMountFailed — openapi.Generator.Mount returned an error.
	CodeOpenAPIMountFailed = "svckit_openapi_mount_failed"

	// CodeOpenAPIYAMLParse — routes.yaml's top-level `openapi:` block
	// failed to read or parse. Set by parseOpenAPIBlock, ported from
	// service/openapi_yaml.go in Task 5; declared here now so the
	// copied file compiles unmodified.
	CodeOpenAPIYAMLParse = "svckit_openapi_yaml_parse"

	// CodeExtraValidatorRegister — WithExtraValidators failed to register a tag.
	CodeExtraValidatorRegister = "svckit_extra_validator_register"

	// CodePreflightFailed — at least one preflight check failed.
	CodePreflightFailed = "svckit_preflight_failed"
)
