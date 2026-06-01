package s3client

// Config carries the connection parameters for [Connect]. Use env
// prefix `S3_` when composing with caarlos0/env (the kit's
// convention).
type Config struct {
	// Endpoint is the S3 endpoint URL. Empty → AWS S3 default.
	// Set to "http://minio:9000" / "https://r2.cloudflarestorage.com"
	// for S3-compatible providers.
	Endpoint string `env:"ENDPOINT"`

	// Region is the AWS region. Required for AWS S3; for
	// S3-compatible providers usually any valid token works
	// (e.g. "us-east-1" or "auto").
	Region string `env:"REGION" envDefault:"us-east-1"`

	// AccessKeyID + SecretAccessKey are the static credentials.
	// Leave both empty to fall back to the SDK's default chain
	// (env vars, instance profile, web identity).
	AccessKeyID     string `env:"ACCESS_KEY_ID"`
	SecretAccessKey string `env:"SECRET_ACCESS_KEY"`

	// SessionToken is optional — set for temporary credentials
	// (STS AssumeRole, web identity).
	SessionToken string `env:"SESSION_TOKEN"`

	// Bucket is the default bucket every op uses unless the caller
	// passes a per-call `WithBucket(name)` override. Required at
	// Connect time so misconfiguration surfaces immediately.
	Bucket string `env:"BUCKET"`

	// ForcePathStyle uses `https://endpoint/bucket/key` URLs
	// instead of `https://bucket.endpoint/key`. Required for
	// MinIO; AWS S3 supports both.
	ForcePathStyle bool `env:"FORCE_PATH_STYLE"`

	// UseSSL is a hint for the SDK when Endpoint is set without
	// a scheme. AWS endpoints always use TLS; this only matters
	// for custom Endpoint values like `minio:9000`.
	UseSSL bool `env:"USE_SSL" envDefault:"true"`
}
