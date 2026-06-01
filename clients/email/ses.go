package email

import (
	"bytes"
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	sestypes "github.com/aws/aws-sdk-go-v2/service/sesv2/types"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// SESConfig is the backend-specific block for [Config] when
// Backend == "ses". All fields are optional — empty AccessKeyID +
// SecretAccessKey falls back to the SDK default credential chain
// (env, instance-profile, IRSA).
type SESConfig struct {
	Region          string `env:"REGION" envDefault:"us-east-1"`
	AccessKeyID     string `env:"ACCESS_KEY_ID"`
	SecretAccessKey string `env:"SECRET_ACCESS_KEY"`
	SessionToken    string `env:"SESSION_TOKEN"`

	// ConfigurationSet activates per-set SES features (event
	// publishing to SNS, IP-pool routing). Empty leaves the default.
	ConfigurationSet string `env:"CONFIGURATION_SET"`

	// Endpoint overrides the default service endpoint (LocalStack /
	// kit tests). Empty uses AWS resolved endpoint.
	Endpoint string `env:"ENDPOINT"`

	// Timeout caps the round-trip per Send. Default 15s.
	Timeout time.Duration `env:"TIMEOUT" envDefault:"15s"`
}

type sesSender struct {
	cfg    SESConfig
	base   baseSender
	client *sesv2.Client
}

func newSESSender(cfg SESConfig, base baseSender) (Sender, error) {
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 15 * time.Second
	}
	loadOpts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cfg.Region),
	}
	if cfg.AccessKeyID != "" && cfg.SecretAccessKey != "" {
		loadOpts = append(loadOpts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, cfg.SessionToken)))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(), loadOpts...)
	if err != nil {
		return nil, xerrs.Wrap(err, xerrs.KindInternal, CodeInvalidConfig,
			"email/ses: aws config load failed")
	}
	clientOpts := []func(*sesv2.Options){}
	if cfg.Endpoint != "" {
		ep := cfg.Endpoint
		clientOpts = append(clientOpts, func(o *sesv2.Options) {
			o.BaseEndpoint = aws.String(ep)
		})
	}
	client := sesv2.NewFromConfig(awsCfg, clientOpts...)
	return &sesSender{cfg: cfg, base: base, client: client}, nil
}

func (s *sesSender) Send(ctx context.Context, msg Message) (err error) {
	if err = msg.Validate(); err != nil {
		return err
	}
	start := time.Now()
	defer func() { s.base.observe("ses", start, msg, err) }()

	// SES SDK supports either Simple (struct fields) or Raw (full
	// MIME bytes). Raw is mandatory for attachments / custom
	// headers, and the simpler always-Raw path keeps the SES
	// surface identical to SMTP. Reuses [buildRFC5322] so the wire
	// shape matches across backends.
	raw, err := buildRFC5322(msg)
	if err != nil {
		return err
	}
	input := &sesv2.SendEmailInput{
		FromEmailAddress: aws.String(msg.From.String()),
		Content: &sestypes.EmailContent{
			Raw: &sestypes.RawMessage{Data: bytes.Clone(raw)},
		},
	}
	if s.cfg.ConfigurationSet != "" {
		input.ConfigurationSetName = aws.String(s.cfg.ConfigurationSet)
	}
	if msg.Tag != "" {
		input.EmailTags = []sestypes.MessageTag{{
			Name:  aws.String("Tag"),
			Value: aws.String(msg.Tag),
		}}
	}
	cctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	if _, err := s.client.SendEmail(cctx, input); err != nil {
		return xerrs.Wrap(err, xerrs.KindUnavailable, CodeSendFailed,
			"email/ses: SendEmail failed")
	}
	return nil
}
