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

type StatsResponse struct {
	Code          string     `json:"code"`
	VisitCount    int64      `json:"visit_count"`
	LastVisitedAt *time.Time `json:"last_visited_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}
