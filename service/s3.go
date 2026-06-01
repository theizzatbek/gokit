package service

import (
	"context"

	s3client "github.com/theizzatbek/gokit/clients/s3"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// CodeS3ConnectFailed — Service.New: s3client.Connect failed.
const CodeS3ConnectFailed = "service_s3_connect_failed"

// buildS3 constructs *s3client.Client when Config.S3.Bucket is
// set. Empty Bucket means the operator didn't opt in — leave
// svc.S3 nil so callers know to skip S3-bound work.
//
// Logger + metrics are auto-applied so observability lands on the
// shared service registry without extra wiring. User overrides
// come through WithS3Options.
func (s *Service[T, C]) buildS3(ctx context.Context) error {
	if s.cfg.S3.Bucket == "" {
		return nil
	}
	defaults := []s3client.Option{
		s3client.WithLogger(s.logger),
	}
	if s.metrics != nil {
		defaults = append(defaults, s3client.WithMetrics(s.metrics))
	}
	all := append(defaults, s.opts.s3Opts...)
	cli, err := s3client.Connect(ctx, s.cfg.S3, all...)
	if err != nil {
		return xerrs.Wrap(err, xerrs.KindUnavailable, CodeS3ConnectFailed,
			"service: s3 connect failed")
	}
	s.S3 = cli
	return nil
}
