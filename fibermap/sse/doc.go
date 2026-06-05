// Package sse adds Server-Sent Events handlers to fibermap. The
// subpackage lives outside core fibermap so the kit's main router
// stays free of streaming-specific code paths even though SSE is
// otherwise dep-free (just fiber + stdlib).
//
// Quickstart:
//
//	eng := fibermap.New[appCtx]()
//	eng.SetContextBuilder(buildAppCtx)
//
//	sse.Register(eng, "events.stream",
//	    func(ctx context.Context, c *fibermap.Context[appCtx], s *sse.Stream) error {
//	        for {
//	            select {
//	            case <-ctx.Done():
//	                return nil
//	            case msg := <-events:
//	                if err := s.SendJSON("update", msg); err != nil {
//	                    return err // client disconnected — close the loop
//	                }
//	            }
//	        }
//	    })
//
// And in routes.yaml:
//
//   - method: GET
//     path:   /events
//     handler: events.stream
//
// The kit auto-emits the SSE response headers (Content-Type, Cache-
// Control, Connection, X-Accel-Buffering), wires fasthttp's
// SetBodyStreamWriter, and surfaces write failures back through
// Stream.Send so the handler can detect client disconnects.
package sse
