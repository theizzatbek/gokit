package s3client

import (
	"context"
	"errors"
	"io"
	"iter"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// Client is the kit's S3 handle. Owns an `*s3.Client` + presigner
// + observability. Goroutine-safe.
type Client struct {
	api       *s3.Client
	presigner *s3.PresignClient
	bucket    string
	logger    *slog.Logger
	metrics   *metricsCollector
}

// Connect builds a Client. Returns *errs.Error with
// CodeMissingBucket / CodeInvalidConfig on misconfiguration; SDK
// errors flow through as `Cause` on the wrapped *errs.Error.
//
// The default credential chain is consulted when both
// AccessKeyID + SecretAccessKey are empty — useful in cloud
// deployments where IAM-role / instance-profile credentials are
// preferred over static keys.
func Connect(ctx context.Context, cfg Config, opts ...Option) (*Client, error) {
	if cfg.Bucket == "" {
		return nil, xerrs.Validation(CodeMissingBucket,
			"s3client: Config.Bucket is required")
	}
	awsCfg, err := buildAWSConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	api := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
		o.UsePathStyle = cfg.ForcePathStyle
	})
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}
	return &Client{
		api:       api,
		presigner: s3.NewPresignClient(api),
		bucket:    cfg.Bucket,
		logger:    o.logger,
		metrics:   o.metrics,
	}, nil
}

// buildAWSConfig constructs `aws.Config`. Static creds win when
// supplied; otherwise the SDK's default chain (env vars, shared
// config, instance profile) applies.
func buildAWSConfig(ctx context.Context, cfg Config) (aws.Config, error) {
	loadOpts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cfg.Region),
	}
	if cfg.AccessKeyID != "" && cfg.SecretAccessKey != "" {
		loadOpts = append(loadOpts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(
				cfg.AccessKeyID, cfg.SecretAccessKey, cfg.SessionToken)))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return aws.Config{}, xerrs.Wrap(err, xerrs.KindValidation, CodeInvalidConfig,
			"s3client: load AWS config")
	}
	return awsCfg, nil
}

// API returns the underlying *s3.Client for advanced operations
// the kit doesn't expose directly (CopyObject, multipart, bucket
// admin). Errors via this path are NOT mapped through the kit's
// *errs.Error contract — caller owns mapping.
func (c *Client) API() *s3.Client { return c.api }

// Bucket returns the default bucket name configured at Connect.
func (c *Client) Bucket() string { return c.bucket }

// Put uploads body to bucket/key. Returns the number of bytes
// written. Apply [PutOption]s for Content-Type, Cache-Control,
// metadata. Body must implement io.Reader; for non-seekable
// streams the SDK auto-buffers — keep payloads bounded or use
// the underlying multipart manager via API().
func (c *Client) Put(ctx context.Context, key string, body io.Reader, opts ...PutOption) error {
	pc := putConfig{}
	for _, opt := range opts {
		opt(&pc)
	}
	start := time.Now()
	input := &s3.PutObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
		Body:   body,
	}
	if pc.contentType != "" {
		input.ContentType = aws.String(pc.contentType)
	}
	if pc.cacheControl != "" {
		input.CacheControl = aws.String(pc.cacheControl)
	}
	if pc.contentEncoding != "" {
		input.ContentEncoding = aws.String(pc.contentEncoding)
	}
	if len(pc.metadata) > 0 {
		input.Metadata = pc.metadata
	}
	_, err := c.api.PutObject(ctx, input)
	elapsed := time.Since(start)
	c.metrics.observe("put", elapsed.Seconds())
	if err != nil {
		c.metrics.record("put", "error")
		c.warn("put failed", "key", key, "err", err.Error())
		return mapErr(err, CodePutFailed, "s3client: put "+key)
	}
	c.metrics.record("put", "success")
	c.debug("put ok", "key", key, "elapsed", elapsed)
	return nil
}

// Get opens the object for read. Caller MUST close the returned
// reader; failing to do so leaks an HTTP connection from the SDK
// pool.
func (c *Client) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	start := time.Now()
	out, err := c.api.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	c.metrics.observe("get", time.Since(start).Seconds())
	if err != nil {
		c.metrics.record("get", "error")
		c.warn("get failed", "key", key, "err", err.Error())
		return nil, mapErr(err, CodeGetFailed, "s3client: get "+key)
	}
	c.metrics.record("get", "success")
	c.debug("get ok", "key", key, "size", out.ContentLength)
	if out.ContentLength != nil {
		c.metrics.bytes("download", *out.ContentLength)
	}
	return out.Body, nil
}

// Delete removes the object. Deleting a missing key is NOT an
// error in S3 — Delete returns nil regardless. Use Exists if you
// care about the pre-existence check.
func (c *Client) Delete(ctx context.Context, key string) error {
	start := time.Now()
	_, err := c.api.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	c.metrics.observe("delete", time.Since(start).Seconds())
	if err != nil {
		c.metrics.record("delete", "error")
		c.warn("delete failed", "key", key, "err", err.Error())
		return mapErr(err, CodeDeleteFailed, "s3client: delete "+key)
	}
	c.metrics.record("delete", "success")
	c.debug("delete ok", "key", key)
	return nil
}

// Exists probes the object with HeadObject. Returns (true, nil)
// when present, (false, nil) when absent, (false, err) on any
// other failure.
func (c *Client) Exists(ctx context.Context, key string) (bool, error) {
	_, err := c.api.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	c.metrics.record("head", outcomeFromErr(err))
	if err == nil {
		return true, nil
	}
	var nf *s3types.NotFound
	if errors.As(err, &nf) || isNotFoundCode(err) {
		return false, nil
	}
	return false, mapErr(err, CodeHeadFailed, "s3client: head "+key)
}

// PresignGet returns a presigned GET URL valid for ttl. Useful for
// browser-side downloads without leaking long-lived credentials.
func (c *Client) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	req, err := c.presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		c.metrics.record("presign_get", "error")
		return "", mapErr(err, CodePresignFailed, "s3client: presign get "+key)
	}
	c.metrics.record("presign_get", "success")
	return req.URL, nil
}

// PresignPut returns a presigned PUT URL valid for ttl. Useful for
// browser-side uploads — clients PUT directly to S3 without
// proxying through the service.
func (c *Client) PresignPut(ctx context.Context, key string, ttl time.Duration) (string, error) {
	req, err := c.presigner.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		c.metrics.record("presign_put", "error")
		return "", mapErr(err, CodePresignFailed, "s3client: presign put "+key)
	}
	c.metrics.record("presign_put", "success")
	return req.URL, nil
}

// Object is one entry returned by [Client.List].
type Object struct {
	Key          string
	Size         int64
	LastModified time.Time
	ETag         string
}

// List paginates objects under prefix. Returns a Go-1.23
// range-over-func iterator — listing stops as soon as the
// consumer breaks the loop:
//
//	for obj, err := range svc.S3.List(ctx, "avatars/") {
//	    if err != nil { return err }
//	    fmt.Println(obj.Key)
//	}
//
// The iterator yields one Object per page-entry; errors propagate
// through the second iter value so the caller's loop can stop on
// the first failure.
func (c *Client) List(ctx context.Context, prefix string) iter.Seq2[Object, error] {
	return func(yield func(Object, error) bool) {
		var continuationToken *string
		for {
			out, err := c.api.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
				Bucket:            aws.String(c.bucket),
				Prefix:            aws.String(prefix),
				ContinuationToken: continuationToken,
			})
			if err != nil {
				c.metrics.record("list", "error")
				yield(Object{}, mapErr(err, CodeListFailed, "s3client: list "+prefix))
				return
			}
			c.metrics.record("list", "success")
			for _, obj := range out.Contents {
				o := Object{
					Key: aws.ToString(obj.Key),
				}
				if obj.Size != nil {
					o.Size = *obj.Size
				}
				if obj.LastModified != nil {
					o.LastModified = *obj.LastModified
				}
				o.ETag = aws.ToString(obj.ETag)
				if !yield(o, nil) {
					return
				}
			}
			if out.IsTruncated == nil || !*out.IsTruncated {
				return
			}
			continuationToken = out.NextContinuationToken
		}
	}
}

func (c *Client) debug(msg string, attrs ...any) {
	if c.logger != nil {
		c.logger.Debug(msg, attrs...)
	}
}

func (c *Client) warn(msg string, attrs ...any) {
	if c.logger != nil {
		c.logger.Warn(msg, attrs...)
	}
}

// mapErr maps an SDK error to a *errs.Error with the kit's stable
// Code constants. Falls back to fallbackCode when the SDK error
// doesn't match a known shape.
func mapErr(err error, fallbackCode, msg string) error {
	if err == nil {
		return nil
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NoSuchKey", "NotFound":
			return xerrs.Wrap(err, xerrs.KindNotFound, CodeNotFound, msg)
		case "AccessDenied":
			return xerrs.Wrap(err, xerrs.KindPermission, CodeAccessDenied, msg)
		case "NoSuchBucket":
			return xerrs.Wrap(err, xerrs.KindNotFound, CodeBucketNotFound, msg)
		}
	}
	var nsk *s3types.NoSuchKey
	if errors.As(err, &nsk) {
		return xerrs.Wrap(err, xerrs.KindNotFound, CodeNotFound, msg)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return xerrs.Wrap(err, xerrs.KindTimeout, CodeUnavailable, msg)
	}
	return xerrs.Wrap(err, xerrs.KindUnavailable, fallbackCode, msg)
}

// isNotFoundCode matches the various 404-like API error codes S3
// uses ("NotFound", "NoSuchKey").
func isNotFoundCode(err error) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.ErrorCode() {
	case "NoSuchKey", "NotFound":
		return true
	}
	return false
}

// outcomeFromErr returns the metric outcome label for an SDK
// error — "success" / "not_found" / "error".
func outcomeFromErr(err error) string {
	if err == nil {
		return "success"
	}
	if isNotFoundCode(err) {
		return "not_found"
	}
	var nf *s3types.NotFound
	if errors.As(err, &nf) {
		return "not_found"
	}
	return "error"
}
