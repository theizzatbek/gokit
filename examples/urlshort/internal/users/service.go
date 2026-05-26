package users

import (
	"context"
	"errors"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/db"
	xerrs "github.com/theizzatbek/gokit/errs"
)

type Service struct {
	db     *db.DB
	hasher *auth.Hasher
}

func NewService(d *db.DB, h *auth.Hasher) *Service {
	return &Service{db: d, hasher: h}
}

// Register hashes the password and inserts the user. Email collisions
// map to *errs.Error{Kind: AlreadyExists}.
func (s *Service) Register(ctx context.Context, email, password string) (User, error) {
	hash, err := s.hasher.Hash(password)
	if err != nil {
		return User{}, xerrs.Wrap(err, xerrs.KindInternal,
			"urlshort_hash_failed", "urlshort: password hash failed")
	}
	var u User
	row := s.db.QueryRow(ctx,
		`INSERT INTO users(email, password_hash) VALUES($1,$2) RETURNING id, email, created_at`,
		email, hash)
	if err := row.Scan(&u.ID, &u.Email, &u.CreatedAt); err != nil {
		if e, ok := errors.AsType[*xerrs.Error](err); ok && e.Kind == xerrs.KindAlreadyExists {
			return User{}, xerrs.AlreadyExists("user_exists", "urlshort: email already registered")
		}
		return User{}, err
	}
	return u, nil
}

// Authenticate returns the user if password matches; otherwise an
// Unauthorized error with the same message for both "user missing"
// and "wrong password" (avoids user enumeration).
func (s *Service) Authenticate(ctx context.Context, email, password string) (User, error) {
	row := s.db.QueryRow(ctx,
		`SELECT id, email, password_hash, created_at FROM users WHERE email = $1`,
		email)
	var (
		u    User
		hash string
	)
	if err := row.Scan(&u.ID, &u.Email, &hash, &u.CreatedAt); err != nil {
		return User{}, xerrs.Unauthorized("invalid_credentials", "urlshort: invalid email or password")
	}
	if err := s.hasher.Verify(hash, password); err != nil {
		return User{}, xerrs.Unauthorized("invalid_credentials", "urlshort: invalid email or password")
	}
	return u, nil
}

// ByID looks up a user by ID. Used by the ClaimsRefresher to rebuild
// custom claims on /auth/refresh.
func (s *Service) ByID(ctx context.Context, id string) (User, error) {
	row := s.db.QueryRow(ctx,
		`SELECT id, email, created_at FROM users WHERE id = $1`, id)
	var u User
	if err := row.Scan(&u.ID, &u.Email, &u.CreatedAt); err != nil {
		return User{}, xerrs.NotFound("user_not_found", "urlshort: user not found")
	}
	return u, nil
}
