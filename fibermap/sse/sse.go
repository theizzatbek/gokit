package sse

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"

	"github.com/gofiber/fiber/v2"
	"github.com/valyala/fasthttp"

	"github.com/theizzatbek/gokit/fibermap"
)

// HandlerFunc is the per-request SSE callback. fibermap invokes it
// with the request's UserContext, the kit's typed *Context[T], and a
// kit-managed Stream the handler uses to push events to the client.
//
// Return nil to close the stream cleanly (the kit emits no body
// trailer; clients see the connection close). A non-nil return is
// logged but does NOT send any HTTP error because the response
// headers have already flushed by the time the handler runs — there
// is no error path back to the client at this stage.
type HandlerFunc[T any] func(ctx context.Context, c *fibermap.Context[T], s *Stream) error

// Stream is the kit-managed SSE writer. Send / SendJSON / Comment
// methods append one frame each and Flush automatically — the user
// never has to remember to flush. Methods return an error on write
// failure (typically client disconnect); callers should bail out of
// their loop on the first error.
//
// Not safe for concurrent use — pin one goroutine per Stream. Use a
// channel + a single goroutine fan-in if you need multi-publisher
// SSE. Concurrent calls to Send / SendJSON / Comment are detected
// at runtime via a CAS guard and panic the second caller with a
// guiding message (pgx-style) rather than corrupting the wire frame
// or doubly-flushing the buffer.
type Stream struct {
	w *bufio.Writer
	// err records the first non-nil write/flush failure so subsequent
	// Sends become no-ops without overwriting the diagnostic. Surfaced
	// via the Send/Comment return value.
	err error
	// inUse guards Send / SendJSON / Comment from concurrent entry.
	// Each method CAS-flips false→true at entry and back at exit; a
	// failed CAS panics with a guiding message rather than racing on
	// the underlying bufio.Writer.
	inUse atomic.Bool
}

// enter / exit form the CAS guard used by Send / SendJSON / Comment
// to make concurrent use fail loudly. Internal sendLocked /
// commentLocked variants bypass the guard so SendJSON can call
// Send-style logic without re-entering its own guard.
func (s *Stream) enter(method string) {
	if !s.inUse.CompareAndSwap(false, true) {
		panic("fibermap/sse: concurrent " + method +
			" on the same Stream — pin one goroutine per Stream " +
			"or fan-in writes through a channel")
	}
}

func (s *Stream) exit() { s.inUse.Store(false) }

// Send writes one SSE event frame:
//
//	event: <event>
//	data: <data>
//
// event MAY be empty (the wire becomes just `data: <data>`); data
// MUST NOT contain CR/LF — the kit splits it into multiple `data:`
// lines following the SSE spec. Auto-flushes the underlying writer
// so the client sees the event immediately.
func (s *Stream) Send(event, data string) error {
	s.enter("Send")
	defer s.exit()
	return s.sendLocked(event, data)
}

// sendLocked is the Send body without the concurrency guard. Used by
// SendJSON so it can hold its own guard while delegating frame-write
// logic without re-entering Send's CAS.
func (s *Stream) sendLocked(event, data string) error {
	if s.err != nil {
		return s.err
	}
	if event != "" {
		if _, err := s.w.WriteString("event: " + event + "\n"); err != nil {
			s.err = err
			return err
		}
	}
	// Per the SSE spec, data containing newlines is split into one
	// `data:` field per line. Empty payload still emits an empty data
	// field so the client gets a delivery.
	if data == "" {
		if _, err := s.w.WriteString("data:\n"); err != nil {
			s.err = err
			return err
		}
	} else {
		for _, line := range strings.Split(data, "\n") {
			if _, err := s.w.WriteString("data: " + line + "\n"); err != nil {
				s.err = err
				return err
			}
		}
	}
	if _, err := s.w.WriteString("\n"); err != nil {
		s.err = err
		return err
	}
	if err := s.w.Flush(); err != nil {
		s.err = err
		return err
	}
	return nil
}

// SendJSON is the JSON-encoded variant of [Stream.Send] — Marshal'es
// payload then forwards to Send with the encoded body. JSON encode
// failures surface as the returned error (no frame is written).
func (s *Stream) SendJSON(event string, payload any) error {
	s.enter("SendJSON")
	defer s.exit()
	if s.err != nil {
		return s.err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return s.sendLocked(event, string(raw))
}

// Comment writes an SSE comment frame (`: <text>`). Use for keep-
// alive pings on idle streams — most browsers + intermediaries kill
// an HTTP connection after ~60s of no traffic, and a periodic
// comment keeps the stream alive without delivering a real event
// the client has to ignore.
func (s *Stream) Comment(text string) error {
	s.enter("Comment")
	defer s.exit()
	if s.err != nil {
		return s.err
	}
	if _, err := s.w.WriteString(": " + text + "\n\n"); err != nil {
		s.err = err
		return err
	}
	if err := s.w.Flush(); err != nil {
		s.err = err
		return err
	}
	return nil
}

// Err returns the first non-nil error observed by Send/SendJSON/
// Comment. Useful for handlers that want to detect a disconnect
// without checking every Send return value (e.g. a fire-and-forget
// publish goroutine that just probes Err periodically).
func (s *Stream) Err() error { return s.err }

// Register binds an SSE handler under `name` in the engine's
// handler map. The actual fibermap handler the engine sees is a
// fiber-compatible wrapper that:
//
//  1. Sets SSE response headers (Content-Type, Cache-Control,
//     Connection, X-Accel-Buffering — the last suppresses Nginx
//     buffering that would otherwise drop event delivery).
//  2. Installs fasthttp.SetBodyStreamWriter pointing at the kit
//     Stream wrapper.
//  3. Invokes the user's HandlerFunc with the request's UserContext
//     so the handler can observe Mount-level shutdown via ctx.Done().
//
// The same handler name resolution flow as RegisterHandler applies —
// YAML routes pointing at `name` get this wrapped handler. The
// route's middleware chain runs BEFORE the SSE wrapper (so auth /
// rate-limit / etc. can reject the upgrade with a regular HTTP
// response).
//
// Panics with fibermap's stable Codes on duplicate registration
// (same as RegisterHandler).
func Register[T any](eng *fibermap.Engine[T], name string, fn HandlerFunc[T]) {
	if eng == nil {
		panic(errors.New("sse.Register: engine is nil"))
	}
	if fn == nil {
		panic(errors.New("sse.Register: handler is nil"))
	}
	eng.RegisterHandler(name, func(c *fibermap.Context[T]) error {
		setSSEHeaders(c.Ctx)
		ctx := c.UserContext()
		// fasthttp's SetBodyStreamWriter calls the supplied callback
		// AFTER request headers have been sent — we cannot return an
		// HTTP error from inside the callback. User handler errors
		// are observed via Stream.Err().
		c.Context().SetBodyStreamWriter(fasthttp.StreamWriter(func(w *bufio.Writer) {
			stream := &Stream{w: w}
			// fn's error is intentionally swallowed here: the handler
			// either Sends successfully or hits Stream.err, both of
			// which the handler has already observed.
			_ = fn(ctx, c, stream)
		}))
		return nil
	})
}

// setSSEHeaders applies the kit-default SSE headers. Split out for
// reuse in tests + so callers extending the subpackage can call the
// same set without copy/paste.
func setSSEHeaders(c *fiber.Ctx) {
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Set("Connection", "keep-alive")
	// Disable nginx/proxy buffering — without this header an
	// intermediary buffers the entire stream and clients see no
	// events until the handler returns.
	c.Set("X-Accel-Buffering", "no")
}
