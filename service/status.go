package service

// Status is a flat snapshot of which subsystems Service.New brought up.
// All fields are booleans except Cron (count of registered jobs); each
// reflects whether the corresponding public field on [Service] is
// non-nil at the time Status() was called.
//
// The struct is stable so callers can rely on it for /healthz banners,
// startup tests, and admin endpoints. New subsystems append fields at
// the bottom — never reorder, never remove without a deprecation window.
type Status struct {
	DB          bool // svc.DB != nil
	Auth        bool // svc.Auth != nil
	NATS        bool // svc.NATS != nil
	NATSMap     bool // svc.NATSMap != nil
	Redis       bool // svc.Redis != nil
	APIMap      bool // svc.APIMap != nil
	S3          bool // svc.S3 != nil
	Outbox      bool // svc.Outbox != nil
	Webhooks    bool // svc.WebhooksWorker != nil || svc.WebhooksFanout != nil
	RateLimiter bool // svc.RateLimiter != nil
	OTel        bool // WithOtel was passed
	Sentry      bool // WithSentry was passed
	RefreshGC   bool // WithRefreshGC was passed AND Auth is configured
	Cron        int  // count of registered scheduler jobs (service.WithCron)
	CronMap     int  // count of jobs in cronmap.Runtime (WithCronMap)
}

// Status returns the current snapshot. Cheap (no locks taken for the
// per-subsystem checks — these fields settle once during Service.New
// and never change afterwards). Cron is read under the scheduler's
// internal lock when the scheduler exists.
//
// Nil receiver returns the zero value.
func (s *Service[T, C]) Status() Status {
	if s == nil {
		return Status{}
	}
	st := Status{
		DB:          s.DB != nil,
		Auth:        s.Auth != nil,
		NATS:        s.NATS != nil,
		NATSMap:     s.NATSMap != nil,
		Redis:       s.Redis != nil,
		APIMap:      s.APIMap != nil,
		S3:          s.S3 != nil,
		Outbox:      s.Outbox != nil,
		Webhooks:    s.WebhooksWorker != nil || s.WebhooksFanout != nil,
		RateLimiter: s.RateLimiter != nil,
		OTel:        s.otelShutdown != nil,
		Sentry:      s.sentryShutdown != nil,
		RefreshGC:   s.refreshStore != nil && s.opts != nil && s.opts.refreshGCInterval > 0,
	}
	if s.scheduler != nil {
		st.Cron = s.scheduler.jobCount()
	}
	if s.CronMap != nil {
		st.CronMap = len(s.CronMap.JobNames())
	}
	return st
}

// logReady emits the startup banner — one structured Info line
// summarising what came up. Called from [New] right before returning
// the constructed Service. Skipped when the logger is nil (which the
// kit's default path never produces, but tests that pass
// WithoutLogger() may).
func (s *Service[T, C]) logReady() {
	if s.logger == nil {
		return
	}
	st := s.Status()
	s.logger.Info("service ready",
		"db", st.DB,
		"auth", st.Auth,
		"nats", st.NATS,
		"natsmap", st.NATSMap,
		"redis", st.Redis,
		"apimap", st.APIMap,
		"s3", st.S3,
		"outbox", st.Outbox,
		"webhooks", st.Webhooks,
		"ratelimit", st.RateLimiter,
		"otel", st.OTel,
		"sentry", st.Sentry,
		"refresh_gc", st.RefreshGC,
		"cron_jobs", st.Cron,
		"cronmap_jobs", st.CronMap,
	)
}
