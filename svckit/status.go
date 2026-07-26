package svckit

// Status is a snapshot of what the core brought up. There are no more
// flat per-subsystem fields: the core doesn't know the list of mods,
// so they arrive as a list instead.
type Status struct {
	DB        bool
	Auth      bool
	RefreshGC bool
	Cron      int
	Mods      []ModStatus // in connect order
}

// ModStatus is one row per mod. Detail is populated when the mod
// implements Statuser; nil otherwise.
type ModStatus struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Detail  any    `json:"detail,omitempty"`
}

// collectModStatus snapshots the slice once at the end of New: after
// that, mod state doesn't change, just like the fields didn't change
// in v1.
func (s *Service[T, C]) collectModStatus() {
	s.modStat = make([]ModStatus, 0, len(s.mods))
	for _, m := range s.mods {
		ms := ModStatus{Name: m.Name(), Enabled: true}
		if st, ok := m.(Statuser); ok {
			ms.Detail = st.Status()
		}
		s.modStat = append(s.modStat, ms)
	}
}

// Status returns the current snapshot. Cheap: apart from the cron job
// count, every field settled during New.
func (s *Service[T, C]) Status() Status {
	if s == nil {
		return Status{}
	}
	st := Status{
		DB:        s.DB != nil,
		Auth:      s.Auth != nil,
		RefreshGC: s.refreshStore != nil && s.opts != nil && s.opts.refreshGCInterval > 0,
		Mods:      s.modStat,
	}
	if s.scheduler != nil {
		st.Cron = s.scheduler.jobCount()
	}
	return st
}

// logReady writes the readiness line. Mods are listed by name so the
// operator can see what actually came up.
func (s *Service[T, C]) logReady() {
	if s.logger == nil {
		return
	}
	st := s.Status()
	names := make([]string, 0, len(st.Mods))
	for _, m := range st.Mods {
		names = append(names, m.Name)
	}
	s.logger.Info("svckit ready",
		"db", st.DB,
		"auth", st.Auth,
		"refresh_gc", st.RefreshGC,
		"cron_jobs", st.Cron,
		"mods", names,
	)
}
