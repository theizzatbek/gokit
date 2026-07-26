package svckit

// Стабильные Code-константы ядра. Коды модов живут в пакетах модов —
// ядро их не знает и не перечисляет.
const (
	// CodeAuthNeedsDB — Config.Auth.PrivateKeyPEM задан, но DB нет.
	CodeAuthNeedsDB = "svckit_auth_needs_db"

	// CodeTLSConfigIncomplete — задан ровно один из TLS_CERT_FILE / TLS_KEY_FILE.
	CodeTLSConfigIncomplete = "svckit_tls_config_incomplete"

	// CodeModDuplicate — два мода вернули одинаковый Name(), либо Name() пуст.
	CodeModDuplicate = "svckit_mod_duplicate"

	// CodeModSetupFailed — мод вернул ошибку из фазы Setup.
	CodeModSetupFailed = "svckit_mod_setup_failed"

	// CodeModBuildFailed — мод вернул ошибку из фазы Build.
	CodeModBuildFailed = "svckit_mod_build_failed"

	// CodeModWireFailed — мод вернул ошибку из фазы Wire.
	CodeModWireFailed = "svckit_mod_wire_failed"

	// CodeDBConnectFailed — db.Connect не смог подключиться.
	CodeDBConnectFailed = "svckit_db_connect_failed"

	// CodeMigrateFailed — миграции не проехали.
	CodeMigrateFailed = "svckit_migrate_failed"

	// CodeAuthInvalidKey — auth.LoadKeysFromPEM / auth.New отвергли ключ.
	CodeAuthInvalidKey = "svckit_auth_invalid_key"

	// CodeHTTPCNewFailed — httpc.New вернул ошибку.
	CodeHTTPCNewFailed = "svckit_httpc_new_failed"

	// CodeRoutesYAMLNotFound — routes.yaml включён, но файла нет.
	CodeRoutesYAMLNotFound = "svckit_routes_yaml_not_found"

	// CodeExtraValidatorRegister — WithExtraValidators не смог зарегистрировать тег.
	CodeExtraValidatorRegister = "svckit_extra_validator_register"

	// CodePreflightFailed — хотя бы одна preflight-проверка упала.
	CodePreflightFailed = "svckit_preflight_failed"
)
