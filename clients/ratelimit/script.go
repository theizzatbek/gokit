package ratelimit

// slidingWindowScript implements an atomic sliding-window-counter on a
// ZSET. Each entry is one allowed request with score=now_ms; on every
// call we drop entries older than (now - window), count what's left,
// and either add the new entry (allowed) or compute when the oldest
// in-window entry will expire (retry-after).
//
// Returns array [allowed, remaining, retry_after_ms]:
//
//	allowed         — 1 on accept, 0 on deny
//	remaining       — slots free after this call (0 on deny)
//	retry_after_ms  — ms until the oldest in-window entry expires (0 on accept)
//
// KEYS[1] — bucket key (full Redis key, prefix already applied caller-side)
// ARGV[1] — now in ms
// ARGV[2] — window in ms
// ARGV[3] — limit
// ARGV[4] — unique member id (random nonce) — required so two
//
//	requests in the same ms don't collapse into one ZSET entry.
const slidingWindowScript = `
local key = KEYS[1]
local now = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local member = ARGV[4]

redis.call('ZREMRANGEBYSCORE', key, 0, now - window)

local count = redis.call('ZCARD', key)
if count >= limit then
    local oldest = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')
    local retry = window
    if #oldest >= 2 then
        retry = (tonumber(oldest[2]) + window) - now
        if retry < 0 then retry = 0 end
    end
    return {0, 0, retry}
end

redis.call('ZADD', key, now, member)
redis.call('PEXPIRE', key, window)
return {1, limit - count - 1, 0}
`
