// Package service bundles every gokit subpackage into a single runtime.
// service.New(ctx, cfg) wires *db.DB, *auth.Auth[C], *natsclient.Client,
// *http.Client, *apimap.Client, and *fibermap.Engine[T] with auto-detect
// optionality — subsystems whose config is empty stay nil and skip
// construction.
//
// Typical wiring:
//
//	cfg := service.Config{ /* env-parsed */ }
//	svc, err := service.New[AppCtx, MyClaims](ctx, cfg,
//	    service.WithOpenAPI(openapi.Info{Title: "myservice", Version: "0.1.0"}),
//	)
//	if err != nil { return err }
//	defer svc.Close()
//
//	// User wires handlers + services using svc.Engine / svc.Auth / svc.DB / ...
//	svc.SetContextBuilder(...)
//	svc.SetCredentialsVerifier(...)
//	fibermap.RegisterHandler(svc.Engine, "ping", pingHandler)
//
//	return svc.Run()
//
// See docs/superpowers/specs/2026-05-25-service-helper-design.md for the full design.
package service
