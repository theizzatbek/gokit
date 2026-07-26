// Package s3mod is the S3 mod for svckit.
//
// Wiring it in:
//
//	type AppConfig struct {
//	    svckit.Config
//	    S3 s3mod.Config `envPrefix:"S3_"`
//	}
//
//	s3m := s3mod.New(cfg.S3)
//	svc, err := svckit.New[App, Claims](ctx, cfg.Config, s3m.Option())
//	...
//	s3m.Client().Put(ctx, key, body)
//
// An empty Bucket means the operator did not enable S3: the mod
// stays disabled, Optional() returns ok=false, and Client() panics
// with a hint. Merely importing this package adds ~7.9 MB to the
// binary (aws-sdk-go-v2) — that's the whole point of mods: don't
// import it, don't pay for it.
package s3mod
