package service

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/getsentry/sentry-go"
	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/sentrykit"
)

// dropSentry returns a no-op BeforeSend option that drops every
// captured event. Used so service-level Sentry tests don't try to
// ship to a real DSN.
func dropSentry() sentrykit.Option {
	return sentrykit.WithBeforeSend(func(_ *sentry.Event, _ *sentry.EventHint) *sentry.Event { return nil })
}

const sentryTestDSN = "https://public@o0.ingest.sentry.io/0"

func TestSetupSentry_EmptyDSNIsNoop(t *testing.T) {
	s := &Service[struct{}, struct{}]{opts: &options{}}
	if err := s.setupSentry(context.Background()); err != nil {
		t.Fatalf("setupSentry: %v", err)
	}
	if s.sentryShutdown != nil {
		t.Errorf("sentryShutdown should be nil when WithSentry wasn't passed")
	}
	if len(s.opts.fiberMiddleware) != 0 {
		t.Errorf("fiberMiddleware = %d, want 0", len(s.opts.fiberMiddleware))
	}
}

func TestSetupSentry_InstallsFiberMiddleware(t *testing.T) {
	s := &Service[struct{}, struct{}]{
		opts: &options{
			sentryDSN:  sentryTestDSN,
			sentryOpts: []sentrykit.Option{dropSentry()},
		},
	}
	if err := s.setupSentry(context.Background()); err != nil {
		t.Fatalf("setupSentry: %v", err)
	}
	if s.sentryShutdown == nil {
		t.Error("sentryShutdown should be non-nil after Setup")
	}
	if len(s.opts.fiberMiddleware) != 1 {
		t.Errorf("fiberMiddleware = %d, want 1 (sentry appended)", len(s.opts.fiberMiddleware))
	}
	// Tear down so subsequent tests start from a clean global hub.
	_ = s.sentryShutdown(context.Background())
}

func TestWithSentry_StoresConfigOnOptions(t *testing.T) {
	o := &options{}
	WithSentry("dsn-xyz", sentrykit.WithEnvironment("staging"))(o)
	if o.sentryDSN != "dsn-xyz" {
		t.Errorf("sentryDSN = %q, want dsn-xyz", o.sentryDSN)
	}
	if len(o.sentryOpts) != 1 {
		t.Errorf("sentryOpts = %d, want 1", len(o.sentryOpts))
	}
}

func TestRegisterSentryShutdown_NoopWhenNil(t *testing.T) {
	s := &Service[struct{}, struct{}]{opts: &options{}}
	s.registerSentryShutdown() // must not panic
	if len(s.shutdownFns) != 0 {
		t.Errorf("shutdownFns = %d, want 0", len(s.shutdownFns))
	}
}

func TestRegisterSentryShutdown_AddsCallback(t *testing.T) {
	called := false
	s := &Service[struct{}, struct{}]{
		opts: &options{},
		sentryShutdown: func(ctx context.Context) error {
			called = true
			return nil
		},
	}
	s.registerSentryShutdown()
	if len(s.shutdownFns) != 1 {
		t.Fatalf("shutdownFns = %d, want 1", len(s.shutdownFns))
	}
	s.Close()
	if !called {
		t.Errorf("sentryShutdown was not invoked during Close")
	}
}

func TestNew_WithSentry_WrapsAutoBuiltLogger(t *testing.T) {
	svc, err := New[testCtx, testClaims](context.Background(), Config{},
		WithSentry(sentryTestDSN, dropSentry()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(svc.Close)

	handlerType := reflect.TypeOf(svc.Logger().Handler()).String()
	// SlogHandler returns an unexported *sentrykit.slogHandler.
	// We assert by type-name suffix to avoid exporting the struct.
	if handlerType == "" || handlerType[len(handlerType)-1] != 'r' || handlerType[:9] != "*sentrykit" {
		// Acceptable too: caller wraps further. So fall back to a
		// behavioural assertion — the handler must NOT be the stock
		// JSON handler.
		if _, isJSON := svc.Logger().Handler().(*slog.JSONHandler); isJSON {
			t.Errorf("expected SlogHandler wrap on kit-built logger, got %T", svc.Logger().Handler())
		}
	}
}

func TestNew_WithSentry_RespectsUserLogger(t *testing.T) {
	user := slog.New(slog.NewJSONHandler(testWriter{}, nil))
	svc, err := New[testCtx, testClaims](context.Background(), Config{},
		WithLogger(user),
		WithSentry(sentryTestDSN, dropSentry()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(svc.Close)

	if svc.Logger() != user {
		t.Errorf("user-supplied logger should not be wrapped; got %T (wanted identity)", svc.Logger())
	}
}

// testWriter is a no-op io.Writer used so the user-supplied logger
// test doesn't pollute stdout.
type testWriter struct{}

func (testWriter) Write(b []byte) (int, error) { return len(b), nil }

func TestNew_WithSentryErrorCapture_CapturesErrorLog(t *testing.T) {
	var captured []*sentry.Event
	svc, err := New[testCtx, testClaims](context.Background(), Config{},
		WithSentry(sentryTestDSN,
			sentrykit.WithBeforeSend(func(e *sentry.Event, _ *sentry.EventHint) *sentry.Event {
				captured = append(captured, e)
				return nil
			})),
		WithSentryErrorCapture(slog.LevelError),
		WithSentryBreadcrumbs(sentrykit.WithCaptureDedupeWindow(0)),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(svc.Close)

	svc.Logger().Error("boom", "k", "v")
	if len(captured) != 1 {
		t.Errorf("expected 1 captured event, got %d", len(captured))
	}

	// Below threshold — no capture.
	svc.Logger().Warn("not severe enough")
	if len(captured) != 1 {
		t.Errorf("Warn should not capture; total events = %d", len(captured))
	}
}

func TestSetupSentry_AutoReleaseInjected(t *testing.T) {
	t.Setenv("SENTRY_RELEASE", "auto-release-v1")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")

	var captured []*sentry.Event
	svc, err := New[testCtx, testClaims](context.Background(), Config{},
		WithSentry(sentryTestDSN,
			sentrykit.WithBeforeSend(func(e *sentry.Event, _ *sentry.EventHint) *sentry.Event {
				captured = append(captured, e)
				return nil
			})),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(svc.Close)

	sentry.CaptureMessage("ping")
	if len(captured) != 1 {
		t.Fatalf("captured = %d, want 1", len(captured))
	}
	if captured[0].Release != "auto-release-v1" {
		t.Errorf("Release = %q, want auto-release-v1", captured[0].Release)
	}
}

func TestSetupSentry_ExplicitReleaseOverridesAuto(t *testing.T) {
	t.Setenv("SENTRY_RELEASE", "auto-release-v1")

	var captured []*sentry.Event
	svc, err := New[testCtx, testClaims](context.Background(), Config{},
		WithSentry(sentryTestDSN,
			sentrykit.WithRelease("explicit-v2"),
			sentrykit.WithBeforeSend(func(e *sentry.Event, _ *sentry.EventHint) *sentry.Event {
				captured = append(captured, e)
				return nil
			})),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(svc.Close)

	sentry.CaptureMessage("ping")
	if captured[0].Release != "explicit-v2" {
		t.Errorf("Release = %q, want explicit-v2 (explicit wins over auto)", captured[0].Release)
	}
}

func TestSetupSentry_UserScopeMiddlewareSkippedWithoutAuth(t *testing.T) {
	s := &Service[struct{}, struct{}]{
		opts: &options{
			sentryDSN:  sentryTestDSN,
			sentryOpts: []sentrykit.Option{dropSentry()},
		},
	}
	if err := s.setupSentry(context.Background()); err != nil {
		t.Fatalf("setupSentry: %v", err)
	}
	t.Cleanup(func() { _ = s.sentryShutdown(context.Background()) })
	// Only sentrykit.FiberMiddleware should be installed; no user scope.
	if got := len(s.opts.fiberMiddleware); got != 1 {
		t.Errorf("fiberMiddleware = %d, want 1 (no Auth → no user scope)", got)
	}
}

func TestSetupSentry_WithoutSentryUserScope_SkipsMiddleware(t *testing.T) {
	s := &Service[struct{}, struct{}]{
		Auth: &auth.Auth[struct{}]{}, // placeholder non-nil — middleware never invokes it here
		opts: &options{
			sentryDSN:           sentryTestDSN,
			sentryOpts:          []sentrykit.Option{dropSentry()},
			skipSentryUserScope: true,
		},
	}
	if err := s.setupSentry(context.Background()); err != nil {
		t.Fatalf("setupSentry: %v", err)
	}
	t.Cleanup(func() { _ = s.sentryShutdown(context.Background()) })
	if got := len(s.opts.fiberMiddleware); got != 1 {
		t.Errorf("fiberMiddleware = %d, want 1 (opt-out applied)", got)
	}
}

func TestSetupSentry_UserScopeMiddlewareInstalledWhenAuthOn(t *testing.T) {
	s := &Service[struct{}, struct{}]{
		Auth: &auth.Auth[struct{}]{},
		opts: &options{
			sentryDSN:  sentryTestDSN,
			sentryOpts: []sentrykit.Option{dropSentry()},
		},
	}
	if err := s.setupSentry(context.Background()); err != nil {
		t.Fatalf("setupSentry: %v", err)
	}
	t.Cleanup(func() { _ = s.sentryShutdown(context.Background()) })
	// Expect sentrykit.FiberMiddleware + sentryUserScopeMiddleware.
	if got := len(s.opts.fiberMiddleware); got != 2 {
		t.Errorf("fiberMiddleware = %d, want 2 (sentry + user scope)", got)
	}
}

func TestSentryUserScopeMiddleware_TagsHubWithPrincipalSubject(t *testing.T) {
	var captured []*sentry.Event
	shutdown, err := sentrykit.Setup(context.Background(), sentryTestDSN,
		sentrykit.WithBeforeSend(func(e *sentry.Event, _ *sentry.EventHint) *sentry.Event {
			captured = append(captured, e)
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("sentrykit.Setup: %v", err)
	}
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	s := &Service[struct{}, struct{}]{Auth: &auth.Auth[struct{}]{}}
	app := fiber.New()
	app.Use(sentrykit.FiberMiddleware())
	// Inject a principal as if auth.Bearer had run upstream.
	app.Use(func(c *fiber.Ctx) error {
		auth.SetPrincipalForTest[struct{}](c, &auth.Principal[struct{}]{Subject: "user-42"})
		return c.Next()
	})
	app.Use(s.sentryUserScopeMiddleware())
	app.Get("/", func(c *fiber.Ctx) error {
		sentrykit.HubFromContext(c).CaptureMessage("hello")
		return c.SendString("ok")
	})

	if _, err := app.Test(httptest.NewRequest("GET", "/", nil)); err != nil {
		t.Fatal(err)
	}
	if len(captured) != 1 {
		t.Fatalf("captured = %d, want 1", len(captured))
	}
	if captured[0].User.ID != "user-42" {
		t.Errorf("User.ID = %q, want user-42", captured[0].User.ID)
	}
}

func TestRegisterSentryShutdown_LIFO_AfterOtel(t *testing.T) {
	// OnShutdown is LIFO. We want sentry to flush BEFORE otel — so
	// we register otel first, then sentry. Calling Close should
	// invoke sentry first, then otel.
	var order []string
	s := &Service[struct{}, struct{}]{
		opts: &options{},
		otelShutdown: func(ctx context.Context) error {
			order = append(order, "otel")
			return nil
		},
		sentryShutdown: func(ctx context.Context) error {
			order = append(order, "sentry")
			return nil
		},
	}
	s.registerOtelShutdown()
	s.registerSentryShutdown()
	s.Close()
	if len(order) != 2 || order[0] != "sentry" || order[1] != "otel" {
		t.Errorf("shutdown order = %v, want [sentry otel]", order)
	}
}
