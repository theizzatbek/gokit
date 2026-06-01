package users

import (
	"context"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/examples/urlshort/urlshort-api/internal/appctx"
	"github.com/theizzatbek/gokit/fibermap"
)

// Handler bundles the deps every users/auth endpoint needs — the user
// service and the kit's Auth[Claims]. Methods are one per endpoint;
// RegisterHandlers is a thin registrar.
//
// The kit no longer owns /auth/login etc. — this handler is where body
// parsing and credential verification live. Token issuance is delegated
// to authObj.IssueLogin / IssueRefresh / Logout.
type Handler struct {
	svc     *Service
	authObj *auth.Auth[Claims]
}

// NewHandler constructs a Handler.
func NewHandler(svc *Service, authObj *auth.Auth[Claims]) *Handler {
	return &Handler{svc: svc, authObj: authObj}
}

// RegisterHandlers wires every users-area fibermap handler:
//   - POST /auth/register  → users.register
//   - POST /auth/login     → users.login
//   - POST /auth/refresh   → users.refresh
//   - POST /auth/logout    → users.logout
//
// Also registers the ClaimsRefresher so RotateRefresh can re-read fresh
// custom claims (e.g. Email) from the users table on each rotation.
func RegisterHandlers(
	eng *fibermap.Engine[appctx.AppCtx],
	svc *Service,
	authObj *auth.Auth[Claims],
) {
	h := NewHandler(svc, authObj)
	authObj.SetClaimsRefresher(h.refreshClaims)
	fibermap.RegisterHandlerWithBody(eng, "users.register", h.Register)
	fibermap.RegisterHandlerWithBody(eng, "users.login", h.Login)
	fibermap.RegisterHandler(eng, "users.refresh", h.Refresh)
	fibermap.RegisterHandler(eng, "users.logout", h.Logout)
}

// Register handles POST /auth/register — creates a user account.
func (h *Handler) Register(c *fibermap.Context[appctx.AppCtx], body RegisterRequest) error {
	u, err := h.svc.Register(c.UserContext(), body.Email, body.Password)
	if err != nil {
		return err
	}
	return c.Status(201).JSON(RegisterResponse{UserID: u.ID})
}

// Login handles POST /auth/login — verifies password and issues tokens.
func (h *Handler) Login(c *fibermap.Context[appctx.AppCtx], body LoginRequest) error {
	u, err := h.svc.Authenticate(c.UserContext(), body.Login, body.Password)
	if err != nil {
		return err
	}
	return h.authObj.IssueLogin(c.Ctx, auth.LoginResult[Claims]{
		Subject: u.ID,
		Custom:  Claims{Email: u.Email},
	})
}

// Refresh handles POST /auth/refresh — rotates the refresh cookie.
func (h *Handler) Refresh(c *fibermap.Context[appctx.AppCtx]) error {
	return h.authObj.IssueRefresh(c.Ctx)
}

// Logout handles POST /auth/logout — revokes the family + clears cookie.
func (h *Handler) Logout(c *fibermap.Context[appctx.AppCtx]) error {
	return h.authObj.Logout(c.Ctx)
}

// refreshClaims is the ClaimsRefresher passed to authObj. Reads up-to-date
// user state on every refresh rotation so role/email changes propagate.
func (h *Handler) refreshClaims(ctx context.Context, subject string) (auth.LoginResult[Claims], error) {
	u, err := h.svc.ByID(ctx, subject)
	if err != nil {
		return auth.LoginResult[Claims]{}, err
	}
	return auth.LoginResult[Claims]{
		Subject: u.ID,
		Custom:  Claims{Email: u.Email},
	}, nil
}
