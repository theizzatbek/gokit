package svckit

import (
	"context"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/auth/refreshpg"
	"github.com/theizzatbek/gokit/clients/httpc"
	"github.com/theizzatbek/gokit/db"
	xerrs "github.com/theizzatbek/gokit/errs"
	"github.com/theizzatbek/gokit/fibermap"
)

func (s *Service[T, C]) buildDB(ctx context.Context) error {
	if s.cfg.DB.User == "" {
		return nil
	}
	applyDBAppNameDefault(&s.cfg.DB.AppName, s.cfg.Service.NodeName)
	applyConnectRetryDefaults(s.opts.skipConnectRetry,
		&s.cfg.DB.ConnectMaxRetries,
		&s.cfg.DB.ConnectBackoffBase,
		&s.cfg.DB.ConnectBackoffMax)
	// Kit-default observability for the db pool: WithLogger always,
	// WithMetrics when the service has a registry AND the caller did
	// not opt out via WithoutAutoDBMetrics. User-supplied dbOpts run
	// LAST so explicit configuration wins on the same field; but note
	// that double-registering db.WithMetrics on the same registry
	// panics — see [WithoutAutoDBMetrics] doc for the conflict path.
	dbOpts := []db.Option{db.WithLogger(s.logger)}
	if !s.opts.skipAutoDBMetrics && s.metrics != nil {
		dbOpts = append(dbOpts, db.WithMetrics(s.metrics))
	}
	dbOpts = append(dbOpts, s.opts.dbOpts...)
	d, err := db.Connect(ctx, s.cfg.DB, dbOpts...)
	if err != nil {
		return xerrs.Wrap(err, xerrs.KindUnavailable, CodeDBConnectFailed, "svckit: db connect failed")
	}
	s.DB = d
	return nil
}

func (s *Service[T, C]) buildAuth() error {
	if s.cfg.Auth.PrivateKeyPEM == "" {
		return nil
	}
	keySet, err := auth.LoadKeysFromPEM(s.cfg.Auth.KID, map[string][]byte{
		s.cfg.Auth.KID: []byte(s.cfg.Auth.PrivateKeyPEM),
	})
	if err != nil {
		return xerrs.Wrap(err, xerrs.KindValidation, CodeAuthInvalidKey, "svckit: auth key invalid")
	}
	apiKeyPepper, err := decodeAPIKeyHashSecret(s.cfg.Auth.APIKeyHashSecret)
	if err != nil {
		return err
	}
	store := refreshpg.New(s.DB)
	authOpts := []auth.Option{
		auth.WithRefreshStore(store),
		auth.WithLogger(s.logger),
		auth.WithMetrics(s.metrics),
	}
	if len(apiKeyPepper) > 0 {
		authOpts = append(authOpts, auth.WithAPIKeyHashSecret(apiKeyPepper))
	}
	a, err := auth.New[C](auth.Config{
		Issuer:     s.cfg.Auth.Issuer,
		Keys:       keySet,
		AccessTTL:  s.cfg.Auth.AccessTTL,
		RefreshTTL: s.cfg.Auth.RefreshTTL,
	}, authOpts...)
	if err != nil {
		return xerrs.Wrap(err, xerrs.KindInternal, CodeAuthInvalidKey, "svckit: auth.New failed")
	}
	s.Auth = a
	s.refreshStore = store
	s.Hasher = auth.NewHasher(auth.DefaultParams())
	return nil
}

func (s *Service[T, C]) buildHTTPC() error {
	httpcOpts := append([]httpc.Option{httpc.WithLogger(s.logger), httpc.WithMetrics(s.metrics)}, s.opts.httpcOpts...)
	c, err := httpc.New(s.cfg.HTTPC, httpcOpts...)
	if err != nil {
		return xerrs.Wrap(err, xerrs.KindValidation, CodeHTTPCNewFailed, "svckit: httpc.New failed")
	}
	s.HTTPC = c
	return nil
}

// buildEngine assembles the engine and wires up a validator:
// caller-supplied wins verbatim, otherwise a kit-default is built and
// extras are registered on it.
func (s *Service[T, C]) buildEngine() error {
	s.Engine = fibermap.Default[T]()
	if s.opts.validator != nil {
		// Caller-supplied validator wins verbatim. WithExtraValidators
		// extras are intentionally ignored — the kit won't mutate a
		// validator instance the caller might share with other code
		// paths. See WithExtraValidators docs for the rationale.
		s.Engine.SetValidator(s.opts.validator)
		return nil
	}
	v := validator.New(validator.WithRequiredStructEnabled())
	for tag, fn := range s.opts.extraValidators {
		if err := v.RegisterValidation(tag, fn); err != nil {
			return xerrs.Wrapf(err, xerrs.KindValidation, CodeExtraValidatorRegister,
				"svckit: WithExtraValidators register %q", tag)
		}
	}
	s.Engine.SetValidator(v)
	return nil
}

// mountAuthMiddleware wires Auth's bearer-style middleware factories onto the
// engine so routes.yaml entries like `middleware: - bearer: []` resolve. The
// kit does not mount /auth/login, /refresh, /logout — those endpoints are
// the caller's responsibility (typically a fibermap handler that parses the
// chosen credential format and calls Auth.IssueLogin / IssueRefresh /
// Logout).
//
// Deliberately does NOT call auth/fibermount.MountMiddlewareFactories.
// That package also hosts ratelimit_redis.go (MountRateLimitRedisFactory),
// which imports clients/ratelimit → clients/redis; Go resolves
// dependencies per package, so importing auth/fibermount AT ALL drags
// both into svckit's core regardless of which function is actually
// called. The seven factories below are registered directly under the
// exact same names fibermount uses, against the exact same *Factory
// methods on auth.Auth[C] — bit-for-bit the same wiring, just without
// the transitive import. See authFactoryAdapt below.
func (s *Service[T, C]) mountAuthMiddleware() error {
	if s.Auth == nil {
		return nil
	}
	a := s.Auth
	fibermap.RegisterMiddlewareFactory(s.Engine, "bearer", authFactoryAdapt[T](a.BearerFactory))
	fibermap.RegisterMiddlewareFactory(s.Engine, "require_scope", authFactoryAdapt[T](a.RequireScopeFactory))
	fibermap.RegisterMiddlewareFactory(s.Engine, "require_role", authFactoryAdapt[T](a.RequireRoleFactory))
	fibermap.RegisterMiddlewareFactory(s.Engine, "require_any_scope", authFactoryAdapt[T](a.RequireAnyScopeFactory))
	fibermap.RegisterMiddlewareFactory(s.Engine, "require_any_role", authFactoryAdapt[T](a.RequireAnyRoleFactory))
	fibermap.RegisterMiddlewareFactory(s.Engine, "rate_limit", authFactoryAdapt[T](a.RateLimitFactory))
	fibermap.RegisterMiddlewareFactory(s.Engine, "idempotency", authFactoryAdapt[T](a.IdempotencyFactory))
	return nil
}

// authFactoryAdapt bridges auth's factory signature
// (func([]any) (fiber.Handler, error)) to fibermap's
// (func([]string) (MiddlewareFunc[T], error)). A copy of
// auth/fibermount's private `adapt` helper — duplicated here so
// mountAuthMiddleware does not need to import auth/fibermount (see its
// doc comment for why).
func authFactoryAdapt[T any](authFactory func([]any) (fiber.Handler, error)) fibermap.MiddlewareFactoryFunc[T] {
	return func(args []string) (fibermap.MiddlewareFunc[T], error) {
		anyArgs := make([]any, len(args))
		for i, a := range args {
			anyArgs[i] = a
		}
		h, err := authFactory(anyArgs)
		if err != nil {
			return nil, err
		}
		return func(c *fibermap.Context[T]) error { return h(c.Ctx) }, nil
	}
}

// applyConnectRetryDefaults centralises the rule:
//   - skip=true → no injection (cfg stays as user wrote it).
//   - maxRetries == -1 → user explicit "no retry"; normalize to 0.
//   - maxRetries == 0 → inject 5 (K8s-friendly default).
//   - base == 0 → inject 1s.
//   - max == 0 → inject 16s.
//
// Pure function — easy to unit test without constructing a Service.
func applyConnectRetryDefaults(skip bool, maxRetries *int, base, max *time.Duration) {
	if skip {
		return
	}
	if *maxRetries == -1 {
		*maxRetries = 0
		return
	}
	if *maxRetries == 0 {
		*maxRetries = 5
	}
	if *base == 0 {
		*base = time.Second
	}
	if *max == 0 {
		*max = 16 * time.Second
	}
}

// applyDBAppNameDefault copies nodeName into appName when appName is empty.
// Used by buildDB so each instance is visible in pg_stat_activity as the
// pod hostname (or whatever the user set in Service.NodeName).
func applyDBAppNameDefault(appName *string, nodeName string) {
	if *appName == "" && nodeName != "" {
		*appName = nodeName
	}
}
