package email

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/smtp"
	"strings"
	"time"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// SMTPConfig is the backend-specific block for [Config] when
// Backend == "smtp". Host + Port are required.
type SMTPConfig struct {
	Host     string `env:"HOST"`
	Port     int    `env:"PORT" envDefault:"587"`
	Username string `env:"USERNAME"`
	Password string `env:"PASSWORD"`

	// AuthMethod is the credential mechanism used. "plain" (default)
	// runs PLAIN over STARTTLS; "cram-md5" uses CRAM-MD5. Empty
	// Username disables auth entirely (no-auth relay).
	AuthMethod string `env:"AUTH_METHOD"`

	// LocalName is the HELO/EHLO domain. Empty defaults to
	// "localhost"; set to your sending host for IP-based reputation
	// systems that match HELO against rDNS.
	LocalName string `env:"LOCAL_NAME"`

	// StartTLS toggles STARTTLS upgrade. true (default) is correct
	// for port 587; set false ONLY for port 25 to local mail-relay
	// inside trusted networks.
	StartTLS bool `env:"START_TLS" envDefault:"true"`

	// Timeout caps the total round-trip per Send. Default 30s.
	Timeout time.Duration `env:"TIMEOUT" envDefault:"30s"`
}

type smtpSender struct {
	cfg  SMTPConfig
	base baseSender
	addr string
}

func newSMTPSender(cfg SMTPConfig, base baseSender) (Sender, error) {
	if cfg.Host == "" {
		return nil, xerrs.Validation(CodeInvalidConfig, "email/smtp: Host is required")
	}
	if cfg.Port == 0 {
		cfg.Port = 587
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.LocalName == "" {
		cfg.LocalName = "localhost"
	}
	return &smtpSender{
		cfg:  cfg,
		base: base,
		addr: fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
	}, nil
}

func (s *smtpSender) Send(ctx context.Context, msg Message) (err error) {
	if err = msg.Validate(); err != nil {
		return err
	}
	start := time.Now()
	defer func() { s.base.observe("smtp", start, msg, err) }()

	body, err := buildRFC5322(msg)
	if err != nil {
		return err
	}

	// Run the SMTP transaction in a goroutine so ctx cancellation
	// surfaces as the function's return even when the net/smtp
	// dial blocks. net/smtp doesn't expose a Dialer-level ctx so
	// the goroutine is the cleanest knob we have.
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.sendOnce(msg, body)
	}()
	select {
	case <-ctx.Done():
		return xerrs.Wrap(ctx.Err(), xerrs.KindTimeout, CodeSendFailed,
			"email/smtp: send cancelled by ctx")
	case e := <-errCh:
		if e != nil {
			return xerrs.Wrap(e, xerrs.KindUnavailable, CodeSendFailed,
				"email/smtp: send failed")
		}
		return nil
	}
}

func (s *smtpSender) sendOnce(msg Message, body []byte) error {
	c, err := smtp.Dial(s.addr)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer c.Close()

	if err := c.Hello(s.cfg.LocalName); err != nil {
		return fmt.Errorf("hello: %w", err)
	}
	if s.cfg.StartTLS {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(nil); err != nil {
				return fmt.Errorf("starttls: %w", err)
			}
		}
	}
	if s.cfg.Username != "" {
		auth := s.selectAuth()
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("auth: %w", err)
		}
	}
	if err := c.Mail(msg.From.Email); err != nil {
		return fmt.Errorf("mail-from: %w", err)
	}
	for _, addr := range msg.AllRecipients() {
		if err := c.Rcpt(addr); err != nil {
			return fmt.Errorf("rcpt %s: %w", addr, err)
		}
	}
	wc, err := c.Data()
	if err != nil {
		return fmt.Errorf("data: %w", err)
	}
	if _, err := wc.Write(body); err != nil {
		_ = wc.Close()
		return fmt.Errorf("write: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("close-data: %w", err)
	}
	return c.Quit()
}

func (s *smtpSender) selectAuth() smtp.Auth {
	switch strings.ToLower(s.cfg.AuthMethod) {
	case "cram-md5":
		return smtp.CRAMMD5Auth(s.cfg.Username, s.cfg.Password)
	default:
		return smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)
	}
}

// buildRFC5322 assembles a complete RFC 5322 message ready to feed
// the SMTP DATA command. Handles plain, multipart/alternative
// (text+html), and multipart/mixed (alternative + attachments).
func buildRFC5322(msg Message) ([]byte, error) {
	var buf bytes.Buffer
	w := &buf

	// Headers
	fmt.Fprintf(w, "From: %s\r\n", msg.From.String())
	if to := formatHeaderList(msg.To); to != "" {
		fmt.Fprintf(w, "To: %s\r\n", to)
	}
	if cc := formatHeaderList(msg.CC); cc != "" {
		fmt.Fprintf(w, "Cc: %s\r\n", cc)
	}
	if rt := formatHeaderList(msg.ReplyTo); rt != "" {
		fmt.Fprintf(w, "Reply-To: %s\r\n", rt)
	}
	fmt.Fprintf(w, "Subject: %s\r\n", msg.Subject)
	fmt.Fprintf(w, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	fmt.Fprintf(w, "MIME-Version: 1.0\r\n")
	for k, v := range msg.Headers {
		fmt.Fprintf(w, "%s: %s\r\n", k, v)
	}
	if msg.Tag != "" {
		fmt.Fprintf(w, "X-Tag: %s\r\n", msg.Tag)
	}

	hasAttach := len(msg.Attachments) > 0
	hasBoth := msg.HTMLBody != "" && msg.TextBody != ""

	switch {
	case !hasAttach && !hasBoth:
		// Single body part.
		ctype := "text/plain; charset=UTF-8"
		body := msg.TextBody
		if msg.HTMLBody != "" {
			ctype = "text/html; charset=UTF-8"
			body = msg.HTMLBody
		}
		fmt.Fprintf(w, "Content-Type: %s\r\n", ctype)
		fmt.Fprintf(w, "Content-Transfer-Encoding: 7bit\r\n\r\n")
		fmt.Fprint(w, body)
	case !hasAttach && hasBoth:
		// multipart/alternative wrapping text + html.
		boundary := nextBoundary()
		fmt.Fprintf(w, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", boundary)
		writeAltParts(w, boundary, msg.TextBody, msg.HTMLBody)
		fmt.Fprintf(w, "--%s--\r\n", boundary)
	case hasAttach:
		// multipart/mixed wrapping (optional alternative) + each
		// attachment as base64.
		mixed := nextBoundary()
		fmt.Fprintf(w, "Content-Type: multipart/mixed; boundary=%q\r\n\r\n", mixed)
		if hasBoth {
			alt := nextBoundary()
			fmt.Fprintf(w, "--%s\r\n", mixed)
			fmt.Fprintf(w, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", alt)
			writeAltParts(w, alt, msg.TextBody, msg.HTMLBody)
			fmt.Fprintf(w, "--%s--\r\n\r\n", alt)
		} else {
			fmt.Fprintf(w, "--%s\r\n", mixed)
			ctype := "text/plain; charset=UTF-8"
			body := msg.TextBody
			if msg.HTMLBody != "" {
				ctype = "text/html; charset=UTF-8"
				body = msg.HTMLBody
			}
			fmt.Fprintf(w, "Content-Type: %s\r\n\r\n", ctype)
			fmt.Fprint(w, body)
			fmt.Fprintf(w, "\r\n")
		}
		for _, a := range msg.Attachments {
			if err := writeAttachment(w, mixed, a); err != nil {
				return nil, err
			}
		}
		fmt.Fprintf(w, "--%s--\r\n", mixed)
	}
	return buf.Bytes(), nil
}

func writeAltParts(w io.Writer, boundary, textBody, htmlBody string) {
	fmt.Fprintf(w, "--%s\r\n", boundary)
	fmt.Fprintf(w, "Content-Type: text/plain; charset=UTF-8\r\n")
	fmt.Fprintf(w, "Content-Transfer-Encoding: 7bit\r\n\r\n%s\r\n", textBody)
	fmt.Fprintf(w, "--%s\r\n", boundary)
	fmt.Fprintf(w, "Content-Type: text/html; charset=UTF-8\r\n")
	fmt.Fprintf(w, "Content-Transfer-Encoding: 7bit\r\n\r\n%s\r\n", htmlBody)
}

func writeAttachment(w io.Writer, boundary string, a Attachment) error {
	if a.Data == nil {
		return xerrs.Validationf(CodeInvalidMessage,
			"email: attachment %q has nil Data", a.Filename)
	}
	raw, err := io.ReadAll(a.Data)
	if err != nil {
		return fmt.Errorf("read attachment %q: %w", a.Filename, err)
	}
	ctype := a.ContentType
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	fmt.Fprintf(w, "--%s\r\n", boundary)
	fmt.Fprintf(w, "Content-Type: %s\r\n", ctype)
	fmt.Fprintf(w, "Content-Transfer-Encoding: base64\r\n")
	fmt.Fprintf(w, "Content-Disposition: attachment; filename=%q\r\n\r\n", a.Filename)
	enc := base64.StdEncoding.EncodeToString(raw)
	// Wrap to 76-char lines per RFC 2045.
	for i := 0; i < len(enc); i += 76 {
		end := i + 76
		if end > len(enc) {
			end = len(enc)
		}
		fmt.Fprintf(w, "%s\r\n", enc[i:end])
	}
	return nil
}
