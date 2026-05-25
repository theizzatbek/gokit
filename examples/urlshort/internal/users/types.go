// Package users owns the user account model — registration, password
// hashing, and the credentials verifier consumed by gokit/auth's
// LoginHandler.
package users

import "time"

type User struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
}

type RegisterRequest struct {
	Email    string `json:"email"    validate:"required,email"`
	Password string `json:"password" validate:"required,min=12,max=200"`
}

type RegisterResponse struct {
	UserID string `json:"user_id"`
}

// Claims is the JWT custom-payload type for urlshort. Embedded by
// auth.LoginResult[C] as the Custom field. Kept tiny — the heavy
// lookups are by subject.
type Claims struct {
	Email string `json:"email"`
}
