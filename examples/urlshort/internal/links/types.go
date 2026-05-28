package links

import "time"

type Link struct {
	ID            string     `json:"id"`
	UserID        string     `json:"user_id"`
	Code          string     `json:"code"`
	OriginalURL   string     `json:"original_url"`
	Title         string     `json:"title"`
	Description   string     `json:"description"`
	ImageURL      string     `json:"image_url"`
	VisitCount    int64      `json:"visit_count"`
	LastVisitedAt *time.Time `json:"last_visited_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

type CreateRequest struct {
	URL string `json:"url" validate:"required,url"`
}

type CreateResponse struct {
	Code        string `json:"code"`
	ShortURL    string `json:"short_url"`
	Title       string `json:"title"`
	Description string `json:"description"`
	ImageURL    string `json:"image_url"`
}

// UpdateRequest is the partial-update body for PATCH /links/:code.
// Fields use pointers so callers can patch a subset — a nil pointer
// leaves the column unchanged, an explicit empty string clears it.
type UpdateRequest struct {
	Title       *string `json:"title,omitempty"       validate:"omitempty,max=200"`
	Description *string `json:"description,omitempty" validate:"omitempty,max=2000"`
}

// UpdateInput is the combined-source input for the link-update endpoint —
// path param (the link's :code) + JSON body (the partial fields).
// fibermap.RegisterHandlerWithInput reflects on this struct once at
// registration and binds + validates both fields per request.
type UpdateInput struct {
	Body   UpdateRequest
	Params struct {
		Code string `params:"code" json:"code" validate:"required,len=6,alphanum"`
	}
}

type StatsResponse struct {
	Code          string     `json:"code"`
	VisitCount    int64      `json:"visit_count"`
	LastVisitedAt *time.Time `json:"last_visited_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}
