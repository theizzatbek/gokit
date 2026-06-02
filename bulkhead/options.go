package bulkhead

// Option tunes [New] beyond what [Config] covers. Reserved for the
// adaptive concurrency layer and any future opt-in features that
// should NOT widen the basic Config struct.
type Option func(*options)

type options struct {
	adaptive *AdaptiveConfig
}

// WithAdaptive enables auto-tuning of capacity via the configured
// [Controller]. The bulkhead starts at [AdaptiveConfig.InitialCap]
// and ticks every [AdaptiveConfig.TickInterval], clamped between
// [AdaptiveConfig.MinCapacity] and [AdaptiveConfig.MaxCapacity].
//
// When [WithAdaptive] is set, [Config.MaxConcurrent] MUST be 0 (the
// initial value comes from AdaptiveConfig.InitialCap instead). The
// bulkhead must be [Bulkhead.Close]'d to stop the controller
// goroutine.
func WithAdaptive(ac AdaptiveConfig) Option {
	return func(o *options) { o.adaptive = &ac }
}
