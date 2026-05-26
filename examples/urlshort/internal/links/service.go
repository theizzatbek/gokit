package links

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/theizzatbek/gokit/db"
	xerrs "github.com/theizzatbek/gokit/errs"

	"github.com/theizzatbek/gokit/examples/urlshort/internal/events"
)

// EnrichFn is the metadata fetcher injected by main.go. The service
// does not depend on the enrich package directly to keep the dep tree
// flat (handlers wire enrich.FetchMetadata in here).
type EnrichFn func(ctx context.Context, url string) (title, description, imageURL string)

type Service struct {
	db     *db.DB
	enrich EnrichFn
	pub    *events.Publisher
}

func NewService(d *db.DB, enrich EnrichFn, pub *events.Publisher) *Service {
	return &Service{db: d, enrich: enrich, pub: pub}
}

// Create enriches metadata best-effort, generates a unique code (with
// retries on collision), inserts, and publishes urlshort.link.created.
func (s *Service) Create(ctx context.Context, userID, originalURL string) (Link, error) {
	title, desc, img := s.enrich(ctx, originalURL)

	for i := 0; i < codeRetryBudget; i++ {
		code, err := generateCode()
		if err != nil {
			return Link{}, xerrs.Wrap(err, xerrs.KindInternal,
				"urlshort_code_rand_failed", "urlshort: random failed")
		}
		var l Link
		row := s.db.QueryRow(ctx, `
			INSERT INTO links(user_id, code, original_url, title, description, image_url)
			VALUES($1,$2,$3,$4,$5,$6)
			RETURNING id, user_id, code, original_url, title, description, image_url,
			          visit_count, last_visited_at, created_at`,
			userID, code, originalURL, title, desc, img)
		err = row.Scan(&l.ID, &l.UserID, &l.Code, &l.OriginalURL, &l.Title,
			&l.Description, &l.ImageURL, &l.VisitCount, &l.LastVisitedAt, &l.CreatedAt)
		if err == nil {
			s.pub.LinkCreated(ctx, events.LinkCreated{
				LinkID:    l.ID,
				UserID:    l.UserID,
				Code:      l.Code,
				URL:       l.OriginalURL,
				Title:     l.Title,
				CreatedAt: l.CreatedAt,
			})
			return l, nil
		}
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			continue // unique-violation on code — retry with a new one
		}
		return Link{}, err
	}
	return Link{}, xerrs.Internal("code_collision_exhausted",
		"urlshort: could not generate unique code after retries")
}

// GetByCode returns the link or NotFound.
func (s *Service) GetByCode(ctx context.Context, code string) (Link, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, user_id, code, original_url, title, description, image_url,
		       visit_count, last_visited_at, created_at
		FROM links WHERE code = $1`, code)
	var l Link
	if err := row.Scan(&l.ID, &l.UserID, &l.Code, &l.OriginalURL, &l.Title,
		&l.Description, &l.ImageURL, &l.VisitCount, &l.LastVisitedAt, &l.CreatedAt); err != nil {
		return Link{}, xerrs.NotFound("link_not_found", "urlshort: link not found")
	}
	return l, nil
}

// IncVisit bumps visit_count and last_visited_at, then publishes
// urlshort.link.visited.
func (s *Service) IncVisit(ctx context.Context, code, userAgent, ip string) (Link, error) {
	row := s.db.QueryRow(ctx, `
		UPDATE links
		SET visit_count = visit_count + 1, last_visited_at = now()
		WHERE code = $1
		RETURNING id, user_id, code, original_url, title, description, image_url,
		          visit_count, last_visited_at, created_at`, code)
	var l Link
	if err := row.Scan(&l.ID, &l.UserID, &l.Code, &l.OriginalURL, &l.Title,
		&l.Description, &l.ImageURL, &l.VisitCount, &l.LastVisitedAt, &l.CreatedAt); err != nil {
		return Link{}, xerrs.NotFound("link_not_found", "urlshort: link not found")
	}
	s.pub.LinkVisited(ctx, events.LinkVisited{
		Code:      code,
		VisitedAt: time.Now(),
		UserAgent: userAgent,
		IP:        ip,
	})
	return l, nil
}

// ListByUser returns the user's links ordered by created_at desc.
func (s *Service) ListByUser(ctx context.Context, userID string) ([]Link, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, code, original_url, title, description, image_url,
		       visit_count, last_visited_at, created_at
		FROM links WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Link{}
	for rows.Next() {
		var l Link
		if err := rows.Scan(&l.ID, &l.UserID, &l.Code, &l.OriginalURL, &l.Title,
			&l.Description, &l.ImageURL, &l.VisitCount, &l.LastVisitedAt, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Delete removes the link if the user owns it. Distinguishes
// "not found" from "wrong owner" with a separate lookup on miss.
func (s *Service) Delete(ctx context.Context, code, userID string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM links WHERE code = $1 AND user_id = $2`, code, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		l, getErr := s.GetByCode(ctx, code)
		if getErr != nil {
			return getErr
		}
		if l.UserID != userID {
			return xerrs.Permission("link_not_owned", "urlshort: link belongs to a different user")
		}
		return xerrs.NotFound("link_not_found", "urlshort: link not found")
	}
	return nil
}
