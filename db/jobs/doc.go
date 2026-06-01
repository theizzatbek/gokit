// Package jobs is a Postgres-backed delayed-job queue. Use it for
// one-shot scheduled work the cron primitive can't cleanly express:
// "send the welcome email 5 minutes after signup", "retry the
// webhook in 1 hour", "cleanup the upload in 7 days".
//
// Schedule[T any](ctx, q, runAt, type, payload) inserts one row;
// Worker.Start spins a goroutine that polls SKIP LOCKED, deserializes
// each row's payload into the handler-registered T, runs the handler,
// and either marks the row done OR re-queues with exponential
// backoff. After max attempts the row is left in state `failed` for
// operator triage — never silently dropped.
//
//	worker, _ := jobs.NewWorker(svc.DB,
//	    jobs.WithInterval(time.Second),
//	    jobs.WithBatchSize(50),
//	    jobs.WithWorkerID(svc.NodeName))
//
//	jobs.RegisterHandler[Welcome](worker, "user.welcome", sendWelcome)
//
//	go worker.Start(ctx)  // OR svc.OnShutdown(worker.Stop)
//
//	// Enqueue from anywhere:
//	_, _ = jobs.Schedule(ctx, svc.DB,
//	    time.Now().Add(5*time.Minute),
//	    "user.welcome",
//	    Welcome{UserID: u.ID})
//
// Multi-pod safe: every Worker shares the same jobs table, the
// SKIP LOCKED scan ensures at-most-one worker grabs a given row per
// tick, and the locked_by column lets ops trace which pod claimed a
// stuck row.
//
// Schema lives in schema.sql; service.WithJobsAutoSchema applies it
// at boot. Most deployments wire DDL through their migration tool —
// the kit ships the raw SQL but does not require it.
package jobs
