package links

import (
	"context"
	"errors"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/db/sqb"
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

// linkColumns is the canonical column order for every SELECT/RETURNING
// against the links table. Centralised here so the Scan helper below
// stays in lock-step with the builders.
var linkColumns = []string{
	"id", "user_id", "code", "original_url", "title", "description", "image_url",
	"visit_count", "last_visited_at", "created_at",
}

var linkReturning = "RETURNING " + strings.Join(linkColumns, ", ")

func scanLink(row pgx.Row, l *Link) error {
	return row.Scan(&l.ID, &l.UserID, &l.Code, &l.OriginalURL, &l.Title,
		&l.Description, &l.ImageURL, &l.VisitCount, &l.LastVisitedAt, &l.CreatedAt)
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
		row := sqb.QueryRow(ctx, s.db, sqb.Builder.
			Insert("links").
			Columns("user_id", "code", "original_url", "title", "description", "image_url").
			Values(userID, code, originalURL, title, desc, img).
			Suffix(linkReturning))
		err = scanLink(row, &l)
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
		if e, ok := errors.AsType[*xerrs.Error](err); ok && e.Kind == xerrs.KindAlreadyExists {
			continue // unique-violation on code — retry with a new one
		}
		return Link{}, err
	}
	return Link{}, xerrs.Internal("code_collision_exhausted",
		"urlshort: could not generate unique code after retries")
}

// GetByCode returns the link or NotFound.
func (s *Service) GetByCode(ctx context.Context, code string) (Link, error) {
	row := sqb.QueryRow(ctx, s.db, sqb.Builder.
		Select(linkColumns...).
		From("links").
		Where(sq.Eq{"code": code}))
	var l Link
	if err := scanLink(row, &l); err != nil {
		return Link{}, xerrs.NotFound("link_not_found", "urlshort: link not found")
	}
	return l, nil
}

// IncVisit bumps visit_count and last_visited_at, then publishes
// urlshort.link.visited.
func (s *Service) IncVisit(ctx context.Context, code, userAgent, ip string) (Link, error) {
	row := sqb.QueryRow(ctx, s.db, sqb.Builder.
		Update("links").
		Set("visit_count", sq.Expr("visit_count + 1")).
		Set("last_visited_at", sq.Expr("now()")).
		Where(sq.Eq{"code": code}).
		Suffix(linkReturning))
	var l Link
	if err := scanLink(row, &l); err != nil {
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
// Uses ReadQuery so the read can ride a replica when configured —
// listings tolerate the ~replica-lag window of staleness.
func (s *Service) ListByUser(ctx context.Context, userID string) ([]Link, error) {
	sqlStr, args, err := sqb.Builder.
		Select(linkColumns...).
		From("links").
		Where(sq.Eq{"user_id": userID}).
		OrderBy("created_at DESC").
		ToSql()
	if err != nil {
		return nil, err
	}
	rows, err := s.db.ReadQuery(ctx, sqlStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Link{}
	for rows.Next() {
		var l Link
		if err := scanLink(rows, &l); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Delete removes the link if the user owns it. Distinguishes
// "not found" from "wrong owner" with a separate lookup on miss.
func (s *Service) Delete(ctx context.Context, code, userID string) error {
	tag, err := sqb.Exec(ctx, s.db, sqb.Builder.
		Delete("links").
		Where(sq.Eq{"code": code, "user_id": userID}))
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
