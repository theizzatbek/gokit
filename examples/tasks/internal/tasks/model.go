package tasks

import "time"

// Task is the domain model. JSON tags double as the wire shape.
type Task struct {
	ID        string    `json:"id"`
	OwnerID   string    `json:"owner_id"`
	Title     string    `json:"title"`
	Done      bool      `json:"done"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ListResponse is the GET /tasks response envelope. A typed struct
// instead of fiber.Map so OpenAPI reflection produces a proper
// schema (`{tasks: array<Task>}`) instead of the opaque
// `map[string]any`.
type ListResponse struct {
	Tasks []Task `json:"tasks"`
}

// ErrorResponse is the universal `{"error": "..."}` body returned
// from every 4xx/5xx response. Typed so the spec advertises the
// `error` field name + string type instead of an opaque object.
type ErrorResponse struct {
	Error string `json:"error"`
}
