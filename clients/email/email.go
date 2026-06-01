package email

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/mail"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// Address is one mailbox. Name is optional (used in From-header
// formatting); Email is required.
type Address struct {
	Name  string
	Email string
}

// String returns the RFC 5322 mailbox formatting: `Name <email>` when
// Name is set, or bare email otherwise. Used by SMTP/SES headers.
func (a Address) String() string {
	if a.Name == "" {
		return a.Email
	}
	addr := mail.Address{Name: a.Name, Address: a.Email}
	return addr.String()
}

// Attachment is one file attached to a Message. Data is read once at
// Send time (the io.Reader is fully consumed) — Reader implementations
// that don't support replay can't be reused across sends.
type Attachment struct {
	Filename    string
	ContentType string // application/pdf, image/png, …
	Data        io.Reader
}

// Message is the wire-shape Sender.Send consumes. The minimal valid
// shape is From + (≥1 of To/CC/BCC) + Subject + (HTMLBody OR
// TextBody). Validate enforces the contract.
type Message struct {
	From        Address
	To          []Address
	CC          []Address
	BCC         []Address
	ReplyTo     []Address
	Subject     string
	HTMLBody    string
	TextBody    string
	Headers     map[string]string
	Attachments []Attachment

	// Tag is an opt-in tracking label propagated to backends that
	// support it (Postmark X-PM-Message-Stream / SES Tags). Maps
	// to nothing on plain SMTP.
	Tag string
}

// Validate reports the message-level contract:
//   - From.Email non-empty
//   - at least one recipient across To/CC/BCC
//   - Subject non-empty
//   - HTMLBody OR TextBody non-empty
//
// Returns *errs.Error{Code: [CodeInvalidMessage]} on failure.
func (m Message) Validate() error {
	if strings.TrimSpace(m.From.Email) == "" {
		return xerrs.Validation(CodeInvalidMessage, "email: From.Email is required")
	}
	if len(m.To)+len(m.CC)+len(m.BCC) == 0 {
		return xerrs.Validation(CodeInvalidMessage, "email: at least one recipient (To/CC/BCC) is required")
	}
	if strings.TrimSpace(m.Subject) == "" {
		return xerrs.Validation(CodeInvalidMessage, "email: Subject is required")
	}
	if m.HTMLBody == "" && m.TextBody == "" {
		return xerrs.Validation(CodeInvalidMessage, "email: HTMLBody or TextBody is required")
	}
	return nil
}

// AllRecipients returns the flat To+CC+BCC slice. Used by SMTP
// backends that need the envelope-RCPT-TO list.
func (m Message) AllRecipients() []string {
	out := make([]string, 0, len(m.To)+len(m.CC)+len(m.BCC))
	for _, a := range m.To {
		out = append(out, a.Email)
	}
	for _, a := range m.CC {
		out = append(out, a.Email)
	}
	for _, a := range m.BCC {
		out = append(out, a.Email)
	}
	return out
}

// Sender is the abstract contract every backend satisfies.
// Goroutine-safe — implementations protect their own state.
type Sender interface {
	Send(ctx context.Context, msg Message) error
}

// Config picks the backend + carries backend-specific knobs. Exactly
// one of SMTP / SES / Postmark must be set when Backend is the
// matching value.
type Config struct {
	Backend string `env:"BACKEND"` // smtp|ses|postmark|stub

	SMTP     SMTPConfig
	SES      SESConfig
	Postmark PostmarkConfig
}

// New constructs a Sender bound to the selected backend. Returns
// *errs.Error{Code: [CodeInvalidConfig]} for an unknown Backend.
//
// Logger + metrics are best-effort: nil values silently skip
// observability.
func New(cfg Config, opts ...Option) (Sender, error) {
	o := options{}
	for _, opt := range opts {
		opt(&o)
	}
	base := baseSender{logger: o.logger, metric: o.metric}
	switch strings.ToLower(strings.TrimSpace(cfg.Backend)) {
	case "smtp":
		return newSMTPSender(cfg.SMTP, base)
	case "ses":
		return newSESSender(cfg.SES, base)
	case "postmark":
		return newPostmarkSender(cfg.Postmark, base)
	case "stub":
		return NewStub(), nil
	case "":
		return nil, xerrs.Validation(CodeInvalidConfig,
			"email: Backend is required (smtp|ses|postmark|stub)")
	default:
		return nil, xerrs.Validationf(CodeInvalidConfig,
			"email: unknown Backend %q", cfg.Backend)
	}
}

// baseSender is the shared observability scaffolding embedded into
// every backend so per-Send logging + metrics behave identically
// regardless of transport.
type baseSender struct {
	logger *slog.Logger
	metric *metrics
}

func (b baseSender) observe(backend string, start time.Time, msg Message, err error) {
	dur := time.Since(start)
	b.metric.observe(backend, dur)
	if err != nil {
		b.metric.record(backend, "error")
		if b.logger != nil {
			b.logger.Warn("email: send failed",
				"backend", backend, "subject", msg.Subject,
				"recipients", len(msg.AllRecipients()),
				"err", err.Error())
		}
		return
	}
	b.metric.record(backend, "ok")
	if b.logger != nil {
		b.logger.Debug("email: sent",
			"backend", backend, "subject", msg.Subject,
			"recipients", len(msg.AllRecipients()),
			"elapsed", dur)
	}
}

// Stub is an in-memory Sender for tests / dev / staging where real
// delivery is undesirable. Captures every successful Send into Sent
// so test code can assert. Goroutine-safe.
type Stub struct {
	mu   sync.Mutex
	sent []Message
}

// NewStub returns a fresh Stub.
func NewStub() *Stub { return &Stub{} }

// Send validates msg and appends to Sent. Returns the validation
// error from Message.Validate.
func (s *Stub) Send(_ context.Context, msg Message) error {
	if err := msg.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, msg)
	return nil
}

// Sent returns a snapshot copy of every Message successfully Send'd.
func (s *Stub) Sent() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Message, len(s.sent))
	copy(out, s.sent)
	return out
}

// Reset clears the captured-Sent list.
func (s *Stub) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = nil
}

// formatHeaderList collapses []Address to a comma-separated RFC
// 5322 list. Used by SMTP/SES backends. Empty slice → empty string.
func formatHeaderList(addrs []Address) string {
	parts := make([]string, len(addrs))
	for i, a := range addrs {
		parts[i] = a.String()
	}
	return strings.Join(parts, ", ")
}

// boundary is a stable-ish multipart boundary; suffixed with a
// monotonic counter so two consecutive messages don't collide.
var boundaryCounter uint64

func nextBoundary() string {
	boundaryCounter++
	return fmt.Sprintf("=_kit_email_%d_%d", time.Now().UnixNano(), boundaryCounter)
}

// observability registers - cheap accessor + no-op when receiver is
// nil (every Option call is independent so the embedded *metrics
// pointer may be unset).
type metrics struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

func newMetrics(reg prometheus.Registerer) *metrics {
	m := &metrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "email_send_total",
			Help: "Email send attempts by backend and outcome.",
		}, []string{"backend", "outcome"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "email_send_duration_seconds",
			Help:    "Latency of Send round-trips.",
			Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}, []string{"backend"}),
	}
	reg.MustRegister(m.requests, m.duration)
	return m
}

func (m *metrics) record(backend, outcome string) {
	if m == nil {
		return
	}
	m.requests.WithLabelValues(backend, outcome).Inc()
}

func (m *metrics) observe(backend string, d time.Duration) {
	if m == nil {
		return
	}
	m.duration.WithLabelValues(backend).Observe(d.Seconds())
}
