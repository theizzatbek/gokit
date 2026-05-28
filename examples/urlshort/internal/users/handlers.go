package users

import (
	"context"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/examples/urlshort/internal/appctx"
	"github.com/theizzatbek/gokit/fibermap"
)

// RegisterHandlers wires every users-area fibermap handler:
//   - POST /auth/register  → users.register  (creates a user)
//   - POST /auth/login     → users.login     (verifies password, issues tokens)
//   - POST /auth/refresh   → users.refresh   (rotates refresh cookie)
//   - POST /auth/logout    → users.logout    (clears refresh cookie + family)
//
// The kit no longer owns /auth/login etc. — this handler is where body
// parsing and credential verification live. Token issuance is delegated to
// authObj.IssueLogin / IssueRefresh / Logout.
//
// SetClaimsRefresher is still registered so RotateRefresh can re-read fresh
// custom claims (e.g. Email) from the users table on each rotation.
func RegisterHandlers(
	eng *fibermap.Engine[appctx.AppCtx],
	svc *Service,
	authObj *auth.Auth[Claims],
) {
	authObj.SetClaimsRefresher(func(ctx context.Context, subject string) (auth.LoginResult[Claims], error) {
		u, err := svc.ByID(ctx, subject)
		if err != nil {
			return auth.LoginResult[Claims]{}, err
		}
		return auth.LoginResult[Claims]{
			Subject: u.ID,
			Custom:  Claims{Email: u.Email},
		}, nil
	})

	fibermap.RegisterHandlerWithBody(eng, "users.register",
		func(c *fibermap.Context[appctx.AppCtx], body RegisterRequest) error {
			u, err := svc.Register(c.UserContext(), body.Email, body.Password)
			if err != nil {
				return err
			}
			return c.Status(201).JSON(RegisterResponse{UserID: u.ID})
		})

	fibermap.RegisterHandlerWithBody(eng, "users.login",
		func(c *fibermap.Context[appctx.AppCtx], body LoginRequest) error {
			u, err := svc.Authenticate(c.UserContext(), body.Login, body.Password)
			if err != nil {
				return err
			}
			return authObj.IssueLogin(c.Ctx, auth.LoginResult[Claims]{
				Subject: u.ID,
				Custom:  Claims{Email: u.Email},
			})
		})

	fibermap.RegisterHandler(eng, "users.refresh",
		func(c *fibermap.Context[appctx.AppCtx]) error {
			return authObj.IssueRefresh(c.Ctx)
		})

	fibermap.RegisterHandler(eng, "users.logout",
		func(c *fibermap.Context[appctx.AppCtx]) error {
			return authObj.Logout(c.Ctx)
		})
}
