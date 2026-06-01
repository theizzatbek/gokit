package s3client

// Stable error Code constants returned in *errs.Error.Code from
// every s3client operation. Treat as part of the public API —
// callers may switch on them, dashboards may alert on them.
const (
	// CodeMissingBucket — Config.Bucket empty at Connect.
	CodeMissingBucket = "s3_missing_bucket"

	// CodeInvalidConfig — Config rejected by the SDK
	// (parse error, missing required region, etc.).
	CodeInvalidConfig = "s3_invalid_config"

	// CodeNotFound — Key does not exist (NoSuchKey).
	CodeNotFound = "s3_not_found"

	// CodeAccessDenied — caller lacks permission (AccessDenied,
	// 403 from upstream).
	CodeAccessDenied = "s3_access_denied"

	// CodeBucketNotFound — bucket itself doesn't exist on the
	// upstream (NoSuchBucket).
	CodeBucketNotFound = "s3_bucket_not_found"

	// CodeUnavailable — generic upstream failure (network error,
	// 5xx from the S3 endpoint).
	CodeUnavailable = "s3_unavailable"

	// CodePutFailed — PutObject upload failed for non-mapped
	// reasons (chunked body read failure, etc.).
	CodePutFailed = "s3_put_failed"

	// CodeGetFailed — GetObject open failed.
	CodeGetFailed = "s3_get_failed"

	// CodeDeleteFailed — DeleteObject failed.
	CodeDeleteFailed = "s3_delete_failed"

	// CodePresignFailed — presigner.PresignGetObject /
	// PresignPutObject failed.
	CodePresignFailed = "s3_presign_failed"

	// CodeHeadFailed — HeadObject failed (used by Exists).
	CodeHeadFailed = "s3_head_failed"

	// CodeListFailed — ListObjectsV2 page fetch failed.
	CodeListFailed = "s3_list_failed"
)
