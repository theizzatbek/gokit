package email

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// PostmarkConfig is the backend-specific block for [Config] when
// Backend == "postmark". ServerToken is required.
type PostmarkConfig struct {
	// ServerToken is the per-server token. Found under Postmark
	// → Server → API Tokens.
	ServerToken string `env:"SERVER_TOKEN"`

	// MessageStream picks the stream (default "outbound"). Postmark
	// requires a matching stream for broadcasts vs transactional.
	MessageStream string `env:"MESSAGE_STREAM" envDefault:"outbound"`

	// Endpoint overrides the default https://api.postmarkapp.com.
	// Used by tests + region-pinned EU deployments
	// (https://api.eu.postmarkapp.com).
	Endpoint string `env:"ENDPOINT"`

	// Timeout caps the round-trip per Send. Default 15s.
	Timeout time.Duration `env:"TIMEOUT" envDefault:"15s"`

	// HTTPClient is an optional override. Set to wire kit's
	// clients/httpc retries / metrics. nil falls back to a
	// fresh net/http.DefaultClient with Timeout.
	HTTPClient *http.Client
}

type postmarkSender struct {
	cfg  PostmarkConfig
	base baseSender
	http *http.Client
	url  string
}

func newPostmarkSender(cfg PostmarkConfig, base baseSender) (Sender, error) {
	if cfg.ServerToken == "" {
		return nil, xerrs.Validation(CodeInvalidConfig,
			"email/postmark: ServerToken is required")
	}
	if cfg.MessageStream == "" {
		cfg.MessageStream = "outbound"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 15 * time.Second
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = "https://api.postmarkapp.com"
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: cfg.Timeout}
	}
	return &postmarkSender{
		cfg:  cfg,
		base: base,
		http: httpClient,
		url:  cfg.Endpoint + "/email",
	}, nil
}

type postmarkPayload struct {
	From          string               `json:"From"`
	To            string               `json:"To,omitempty"`
	Cc            string               `json:"Cc,omitempty"`
	Bcc           string               `json:"Bcc,omitempty"`
	Subject       string               `json:"Subject"`
	HtmlBody      string               `json:"HtmlBody,omitempty"`
	TextBody      string               `json:"TextBody,omitempty"`
	ReplyTo       string               `json:"ReplyTo,omitempty"`
	Tag           string               `json:"Tag,omitempty"`
	MessageStream string               `json:"MessageStream"`
	Headers       []postmarkHeader     `json:"Headers,omitempty"`
	Attachments   []postmarkAttachment `json:"Attachments,omitempty"`
}

type postmarkHeader struct {
	Name  string `json:"Name"`
	Value string `json:"Value"`
}

type postmarkAttachment struct {
	Name        string `json:"Name"`
	Content     string `json:"Content"`
	ContentType string `json:"ContentType"`
}

type postmarkError struct {
	ErrorCode int    `json:"ErrorCode"`
	Message   string `json:"Message"`
}

func (s *postmarkSender) Send(ctx context.Context, msg Message) (err error) {
	if err = msg.Validate(); err != nil {
		return err
	}
	start := time.Now()
	defer func() { s.base.observe("postmark", start, msg, err) }()

	payload, err := buildPostmarkPayload(msg, s.cfg.MessageStream)
	if err != nil {
		return err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return xerrs.Wrap(err, xerrs.KindInternal, CodeSendFailed,
			"email/postmark: marshal payload")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		return xerrs.Wrap(err, xerrs.KindInternal, CodeSendFailed,
			"email/postmark: build request")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Postmark-Server-Token", s.cfg.ServerToken)

	resp, err := s.http.Do(req)
	if err != nil {
		return xerrs.Wrap(err, xerrs.KindUnavailable, CodeSendFailed,
			"email/postmark: send failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		pe := postmarkError{}
		_ = json.Unmarshal(raw, &pe)
		return xerrs.Wrapf(fmt.Errorf("postmark %d: %s", pe.ErrorCode, pe.Message),
			kindFromStatus(resp.StatusCode), CodeSendFailed,
			"email/postmark: api returned %d", resp.StatusCode)
	}
	return nil
}

func buildPostmarkPayload(msg Message, stream string) (postmarkPayload, error) {
	p := postmarkPayload{
		From:          msg.From.String(),
		To:            formatHeaderList(msg.To),
		Cc:            formatHeaderList(msg.CC),
		Bcc:           formatHeaderList(msg.BCC),
		Subject:       msg.Subject,
		HtmlBody:      msg.HTMLBody,
		TextBody:      msg.TextBody,
		ReplyTo:       formatHeaderList(msg.ReplyTo),
		Tag:           msg.Tag,
		MessageStream: stream,
	}
	for k, v := range msg.Headers {
		p.Headers = append(p.Headers, postmarkHeader{Name: k, Value: v})
	}
	for _, a := range msg.Attachments {
		if a.Data == nil {
			return p, xerrs.Validationf(CodeInvalidMessage,
				"email: attachment %q has nil Data", a.Filename)
		}
		raw, err := io.ReadAll(a.Data)
		if err != nil {
			return p, xerrs.Wrapf(err, xerrs.KindInternal, CodeSendFailed,
				"email/postmark: read attachment %q", a.Filename)
		}
		ctype := a.ContentType
		if ctype == "" {
			ctype = "application/octet-stream"
		}
		p.Attachments = append(p.Attachments, postmarkAttachment{
			Name:        a.Filename,
			Content:     base64.StdEncoding.EncodeToString(raw),
			ContentType: ctype,
		})
	}
	return p, nil
}

func kindFromStatus(s int) xerrs.Kind {
	switch {
	case s == http.StatusUnauthorized || s == http.StatusForbidden:
		return xerrs.KindPermission
	case s == http.StatusTooManyRequests:
		return xerrs.KindRateLimited
	case s >= 400 && s < 500:
		return xerrs.KindValidation
	default:
		return xerrs.KindUnavailable
	}
}
