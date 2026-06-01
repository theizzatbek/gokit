package links

import (
	"context"
	"errors"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"github.com/theizzatbek/gokit/clients/cache"
	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/db/outbox"
	"github.com/theizzatbek/gokit/db/sqb"
	xerrs "github.com/theizzatbek/gokit/errs"

	"github.com/theizzatbek/gokit/examples/urlshort/shared/events"
)

// CachedLink is the trimmed-down projection stored in Redis under
// urlshort:link:<code>. visit_count + last_visited_at deliberately
// omitted — they mutate on every click and would force an
// invalidation per redirect, defeating the cache's purpose.
type CachedLink struct {
	ID          string `json:"id"`
	UserID      string `json:"user_id"`
	Code        string `json:"code"`
	OriginalURL string `json:"original_url"`
}

// VisitPublisher emits one LinkVisited event per successful redirect.
// Service depends on the interface (not the publisher transport
// directly) so the example stays unit-testable: tests pass a no-op,
// main.go wires the HTTP-backed urlshort-publisher gateway client.
//
// Enrichment (title / description / image_url) is OUT of scope for
// the api — it happens asynchronously in urlshort-enricher after the
// LinkCreated outbox event is published. The api always inserts the
// row with empty metadata; the enricher UPDATEs once Microlink
// returns. Stale rows (enricher down) keep working — only the
// metadata is missing, the redirect still resolves.
type VisitPublisher interface {
	LinkVisited(ctx context.Context, e events.LinkVisited)
}

type Service struct {
	db    *db.DB
	pub   VisitPublisher
	cache *cache.Redis[CachedLink] // nil = no cache (graceful pass-through)
}

func NewService(d *db.DB, pub VisitPublisher, c *cache.Redis[CachedLink]) *Service {
	return &Service{db: d, pub: pub, cache: c}
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
// retries on collision), inserts, and enqueues urlshort.link.created
// into the transactional outbox.
//
// The INSERT + outbox.Enqueue run inside ONE db.Tx so the event is
// persisted atomically with the link row — no crash window between
// commit and publish. The kit-managed outbox.Worker drains the
// outbox table asynchronously and publishes through natsmap (the
// worker lives in urlshort-publisher, not here).
//
// Idempotent on (user_id, original_url): the second time a user posts
// the same URL, this returns the existing link without inserting a
// duplicate and without re-enqueuing LinkCreated. Backed by the
// UNIQUE (user_id, original_url) index from migration 0002 and a
// pre-check SELECT — the INSERT path runs only on a true miss.
//
// The pre-check + insert pattern (vs. INSERT … ON CONFLICT DO UPDATE
// RETURNING) is deliberate here because the code field is generated
// fresh per attempt; ON CONFLICT … DO NOTHING would silently
// suppress the row and force a follow-up SELECT anyway. Two queries
// is the same network cost with cleaner semantics.
func (s *Service) Create(ctx context.Context, userID, originalURL string) (Link, error) {
	// Idempotency pre-check. A concurrent second poster can still
	// race past this; the INSERT below catches it via the new UNIQUE
	// (user_id, original_url) index.
	if l, err := s.findByUserAndURL(ctx, userID, originalURL); err == nil {
		return l, nil
	}

	for i := 0; i < codeRetryBudget; i++ {
		code, err := generateCode()
		if err != nil {
			return Link{}, xerrs.Wrap(err, xerrs.KindInternal,
				"urlshort_code_rand_failed", "urlshort: random failed")
		}

		var inserted Link
		txErr := s.db.Tx(ctx, func(tx *db.Tx) error {
			// title/description/image_url stay empty here — the
			// enricher service backfills them out-of-band after
			// consuming LinkCreated. Lets the redirect endpoint
			// work the instant the row is committed even if the
			// enricher is paused.
			l, qerr := sqb.QueryOne[Link](ctx, tx, sqb.Builder.
				Insert("links").
				Columns("user_id", "code", "original_url").
				Values(userID, code, originalURL).
				Suffix(linkReturning), scanLink)
			if qerr != nil {
				return qerr
			}
			if oerr := outbox.EnqueueTyped(ctx, tx, events.SubjectLinkCreated,
				events.LinkCreated{
					LinkID:    l.ID,
					UserID:    l.UserID,
					Code:      l.Code,
					URL:       l.OriginalURL,
					Title:     l.Title,
					CreatedAt: l.CreatedAt,
				},
				outbox.WithAggregate("link", l.Code),
			); oerr != nil {
				return oerr
			}
			inserted = l
			return nil
		})
		if txErr == nil {
			return inserted, nil
		}
		if e, ok := errors.AsType[*xerrs.Error](txErr); ok && e.Kind == xerrs.KindAlreadyExists {
			// AlreadyExists covers TWO unique constraints:
			//   links_code_key (UNIQUE code) — code generator collided.
			//     Retry with a fresh code.
			//   links_user_url_uniq (UNIQUE user_id, original_url) —
			//     a concurrent request inserted the same URL between
			//     our pre-check and our INSERT. Re-fetch.
			if existing, getErr := s.findByUserAndURL(ctx, userID, originalURL); getErr == nil {
				return existing, nil
			}
			continue // assume it was the code collision; retry
		}
		return Link{}, txErr
	}
	return Link{}, xerrs.Internal("code_collision_exhausted",
		"urlshort: could not generate unique code after retries")
}

func (s *Service) findByUserAndURL(ctx context.Context, userID, originalURL string) (Link, error) {
	return sqb.QueryOne[Link](ctx, s.db, sqb.Builder.
		Select(linkColumns...).
		From("links").
		Where(sq.Eq{"user_id": userID, "original_url": originalURL}), scanLink)
}

// GetByCode returns the link or NotFound.
func (s *Service) GetByCode(ctx context.Context, code string) (Link, error) {
	l, err := sqb.QueryOne[Link](ctx, s.db, sqb.Builder.
		Select(linkColumns...).
		From("links").
		Where(sq.Eq{"code": code}), scanLink)
	if err != nil {
		return Link{}, xerrs.NotFound("link_not_found", "urlshort: link not found")
	}
	return l, nil
}

// Resolve is the read-side of the redirect path. Optimised for
// latency:
//
//  1. Redis cache lookup. Positive hit → return + publish.
//  2. Negative cache hit (known-bad code) → return NotFound without
//     touching Postgres. Absorbs scanner traffic at zero DB cost.
//  3. DB fallback. Populate cache (positive or negative based on
//     outcome) and return.
//  4. Publish urlshort.link.visited — the link_visit_counter
//     subscriber persists the increment asynchronously.
//
// Cached projection (see CachedLink) only carries the fields the
// redirect needs: ID, UserID, Code, OriginalURL. visit_count +
// last_visited_at intentionally absent — they mutate on every click
// and would defeat the cache's purpose.
//
// visit_count + last_visited_at are eventually consistent. Stats
// reads taken < ~10ms after a click may miss the most recent visit
// while the subscriber drains its batch buffer. The counts are
// durable: JetStream persists each event before the publish call
// returns, and the subscriber's batched UPDATE either commits all or
// retries on failure.
func (s *Service) Resolve(ctx context.Context, code, userAgent, ip string) (Link, error) {
	if hit := s.cache.Get(ctx, code); hit.Value != nil {
		s.publishVisit(ctx, code, userAgent, ip)
		return Link{
			ID:          hit.Value.ID,
			UserID:      hit.Value.UserID,
			Code:        hit.Value.Code,
			OriginalURL: hit.Value.OriginalURL,
		}, nil
	} else if hit.NotFound {
		return Link{}, xerrs.NotFound("link_not_found", "urlshort: link not found")
	}

	l, err := s.GetByCode(ctx, code)
	if err != nil {
		// Cache the 404 so the next scanner hit on this code
		// short-circuits before Postgres.
		s.cache.SetNotFound(ctx, code)
		return Link{}, err
	}
	s.cache.Set(ctx, code, CachedLink{
		ID: l.ID, UserID: l.UserID, Code: l.Code, OriginalURL: l.OriginalURL,
	})
	s.publishVisit(ctx, code, userAgent, ip)
	return l, nil
}

func (s *Service) publishVisit(ctx context.Context, code, userAgent, ip string) {
	s.pub.LinkVisited(ctx, events.LinkVisited{
		Code:      code,
		VisitedAt: time.Now(),
		UserAgent: userAgent,
		IP:        ip,
	})
}

// ListByUser returns the user's links ordered by created_at desc with
// pagination + optional case-insensitive search on title / original_url
// applied via params. Uses ReadQuery so the read can ride a replica
// when configured — listings tolerate the ~replica-lag window of
// staleness. sqb.ScanAll dissolves the iter loop while keeping the
// ReadQuery path explicit (the higher-level sqb.QueryAll routes to the
// primary pool).
func (s *Service) ListByUser(ctx context.Context, userID string, params ListParams) ([]Link, error) {
	b := sqb.Builder.
		Select(linkColumns...).
		From("links").
		Where(sq.Eq{"user_id": userID}).
		OrderBy("created_at DESC")
	if params.Q != "" {
		// Postgres ILIKE for case-insensitive substring search. Both
		// columns are user-visible so it makes sense to search both.
		needle := "%" + params.Q + "%"
		b = b.Where(sq.Or{
			sq.ILike{"title": needle},
			sq.ILike{"original_url": needle},
		})
	}
	sqlStr, args, err := params.Apply(b).ToSql()
	if err != nil {
		return nil, err
	}
	rows, err := s.db.ReadQuery(ctx, sqlStr, args...)
	if err != nil {
		return nil, err
	}
	return sqb.ScanAll[Link](rows, scanLink)
}

// Update applies the partial UpdateRequest to the link identified by code,
// owner-gated by userID. Nil fields on req leave the corresponding column
// unchanged. Returns the updated Link.
//
// "not found" vs "wrong owner" disambiguates via a follow-up lookup on
// miss, same pattern as Delete.
func (s *Service) Update(ctx context.Context, code, userID string, req UpdateRequest) (Link, error) {
	b := sqb.Builder.Update("links").
		Where(sq.Eq{"code": code, "user_id": userID}).
		Suffix(linkReturning)
	if req.Title != nil {
		b = b.Set("title", *req.Title)
	}
	if req.Description != nil {
		b = b.Set("description", *req.Description)
	}
	// All-nil request → nothing to set; just return current state.
	if req.Title == nil && req.Description == nil {
		l, err := s.GetByCode(ctx, code)
		if err != nil {
			return Link{}, err
		}
		if l.UserID != userID {
			return Link{}, xerrs.Permission("link_not_owned", "urlshort: link belongs to a different user")
		}
		return l, nil
	}

	l, err := sqb.QueryOne[Link](ctx, s.db, b, scanLink)
	if err != nil {
		// UPDATE ... RETURNING produced no row → either no such code OR
		// code exists but belongs to a different user. Distinguish for
		// caller via a public-read lookup.
		got, getErr := s.GetByCode(ctx, code)
		if getErr != nil {
			return Link{}, getErr
		}
		if got.UserID != userID {
			return Link{}, xerrs.Permission("link_not_owned", "urlshort: link belongs to a different user")
		}
		return Link{}, xerrs.NotFound("link_not_found", "urlshort: link not found")
	}
	// Invalidate AFTER the write so the next Resolve refetches the
	// updated title / description / image_url from Postgres.
	s.cache.Invalidate(ctx, code)
	return l, nil
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
	// Drop any cached projection so the next Resolve returns NotFound
	// (or, with the negative cache populated, short-circuits straight
	// to a 404 without DB).
	s.cache.Invalidate(ctx, code)
	return nil
}
