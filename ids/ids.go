package ids

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/theizzatbek/gokit/errs"
)

// Stable error codes returned in [*errs.Error.Code] from the ids
// package. Downstream alerting / log-routing can match on these
// constants stably.
const (
	// CodeBadPrefix — input doesn't start with the prefix the caller
	// supplied. Also returned when the input is shorter than the
	// prefix or empty. Wire-safe to surface in HTTP 400 responses.
	CodeBadPrefix = "id_bad_prefix"

	// CodeBadSuffix — the 26-char tail after the prefix isn't a
	// valid Crockford-Base32 ULID. Covers wrong length, illegal
	// character, ULID-overflow. Wire-safe to surface in HTTP 400.
	CodeBadSuffix = "id_bad_suffix"
)

// suffixLen is the canonical ULID-string length per the spec
// (Crockford-Base32-encoded 16 bytes).
const suffixLen = 26

// Sentinel errors for errors.Is checks. Most callers should branch
// on *errs.Error.Code instead — the codes are stable across the
// kit's semver contract; sentinel identity is not.
var (
	// ErrBadPrefix is wrapped by the *errs.Error returned from Parse
	// when the input doesn't match the supplied prefix.
	ErrBadPrefix = errors.New("ids: bad prefix")

	// ErrBadSuffix is wrapped by the *errs.Error returned from Parse
	// when the suffix isn't a valid ULID.
	ErrBadSuffix = errors.New("ids: bad suffix")
)

// entropy is a process-shared monotonic ULID entropy source guarded
// by mu. The ulid.MonotonicEntropy contract requires serialisation
// across calls — concurrent New() calls in the same millisecond
// would otherwise race the internal increment counter and risk
// non-monotonic output (the package would panic instead but the
// kit prefers a small lock over surfacing the panic).
var (
	mu      sync.Mutex
	entropy io.Reader = ulid.Monotonic(rand.Reader, 0)
)

// New returns "<prefix><26-char Crockford-Base32 ULID>".
//
// The 16-byte binary layout is the ULID spec's:
//
//	6 bytes ms-precision Unix timestamp || 10 bytes randomness
//
// IDs are time-sortable (lexicographic order matches creation
// order at ms resolution) and monotonic within a single process —
// two New() calls in the same millisecond produce IDs that compare
// as strictly increasing per the ULID monotonic-entropy contract.
//
// New is safe for concurrent use; the entropy source is guarded
// by a package-level lock so two goroutines colliding in the same
// millisecond see deterministic monotonic increments rather than
// the underlying ulid package's panic-on-overflow behaviour.
func New(prefix string) string {
	mu.Lock()
	id := ulid.MustNew(ulid.Timestamp(time.Now()), entropy)
	mu.Unlock()
	return prefix + id.String()
}

// Parse validates that s starts with prefix and that the 26-char
// tail decodes as a Crockford-Base32 ULID, returning the raw 16
// bytes for storage as a Postgres uuid column (or any other
// 16-byte-keyed store).
//
// Error mapping:
//
//	*errs.Error{Kind: Validation, Code: CodeBadPrefix}
//	    when s doesn't start with prefix (or is shorter than prefix).
//
//	*errs.Error{Kind: Validation, Code: CodeBadSuffix}
//	    when the tail isn't a valid ULID (wrong length, illegal
//	    character, overflow).
//
// Callers that want errors.Is can match against [ErrBadPrefix] /
// [ErrBadSuffix]; most code should match on e.Code instead — the
// codes are part of the kit's semver-stable contract.
func Parse(prefix, s string) ([16]byte, error) {
	if !strings.HasPrefix(s, prefix) {
		return [16]byte{}, errs.Validation(CodeBadPrefix,
			fmt.Sprintf("ids: input %q is missing prefix %q", s, prefix))
	}
	tail := s[len(prefix):]
	if len(tail) != suffixLen {
		return [16]byte{}, errs.Validation(CodeBadSuffix,
			fmt.Sprintf("ids: suffix length %d after prefix %q, want %d", len(tail), prefix, suffixLen))
	}
	parsed, err := ulid.Parse(tail)
	if err != nil {
		return [16]byte{}, errs.Wrap(err, errs.KindValidation, CodeBadSuffix,
			fmt.Sprintf("ids: suffix %q is not a valid ULID", tail))
	}
	return parsed, nil
}

// Format is the inverse of Parse for callers holding raw 16-byte ID
// material (typically a row scanned from a pgx `uuid` column). The
// output is the canonical wire form: `prefix + 26-char uppercase
// Crockford-Base32`.
//
// Format does not validate raw — any 16 bytes encode cleanly to a
// 26-char ULID string. Callers that scanned from an authoritative
// source (DB, Parse output) can trust the result.
func Format(prefix string, raw [16]byte) string {
	return prefix + ulid.ULID(raw).String()
}
