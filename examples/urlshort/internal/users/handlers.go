package users

import (
	"context"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/examples/urlshort/internal/appctx"
	"github.com/theizzatbek/gokit/fibermap"
	"github.com/theizzatbek/gokit/fibermap/bind"
)

// RegisterHandlers wires:
//   - POST /auth/register as a fibermap handler ("users.register").
//   - The CredentialsVerifier consumed by authObj.LoginHandler when the
//     route POST /auth/login is mounted elsewhere.
//   - The ClaimsRefresher consumed by authObj.RefreshHandler.
//
// LoginHandler / RefreshHandler / LogoutHandler are mounted as raw
// Fiber routes in main.go since they don't take a typed AppCtx.
func RegisterHandlers(
	eng *fibermap.Engine[appctx.AppCtx],
	svc *Service,
	authObj *auth.Auth[Claims],
) {
	authObj.SetCredentialsVerifier(func(ctx context.Context, req auth.LoginRequest) (auth.LoginResult[Claims], error) {
		u, err := svc.Authenticate(ctx, req.Login, req.Password)
		if err != nil {
			return auth.LoginResult[Claims]{}, err
		}
		return auth.LoginResult[Claims]{
			Subject: u.ID,
			Custom:  Claims{Email: u.Email},
		}, nil
	})
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

	fibermap.RegisterHandler(eng, "users.register",
		func(c *fibermap.Context[appctx.AppCtx]) error {
			body, err := bind.Body[RegisterRequest](c.Ctx, nil)
			if err != nil {
				return err
			}
			u, err := svc.Register(c.UserContext(), body.Email, body.Password)
			if err != nil {
				return err
			}
			return c.Status(201).JSON(RegisterResponse{UserID: u.ID})
		})
}
