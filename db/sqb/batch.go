package sqb

// InBatches calls fn on contiguous chunks of items, each at most size
// long. Returns the first non-nil error from fn (and stops); a nil
// error means every chunk processed cleanly.
//
// Use to keep large WHERE id IN (...) statements under Postgres's
// 65535-parameter ceiling — common when bulk-deleting / -updating
// based on a list of IDs gathered from a separate query:
//
//	err := sqb.InBatches(ids, 1000, func(chunk []uuid.UUID) error {
//	    _, err := sqb.Exec(ctx, db,
//	        sqb.Builder.Delete("items").Where(sq.Eq{"id": chunk}))
//	    return err
//	})
//
// Also useful for streaming bulk inserts when the row count is large
// enough that a single statement would exceed pgx's bind cap.
//
// Panics when size <= 0 — programmer error. Empty items is a no-op
// (returns nil without invoking fn).
func InBatches[T any](items []T, size int, fn func([]T) error) error {
	if size <= 0 {
		panic("sqb.InBatches: size must be > 0")
	}
	for i := 0; i < len(items); i += size {
		end := i + size
		if end > len(items) {
			end = len(items)
		}
		if err := fn(items[i:end]); err != nil {
			return err
		}
	}
	return nil
}
