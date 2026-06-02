// Package inboxnats wires [db/inbox] onto [clients/natsmap]
// handlers. It adapts a domain function with signature
//
//	func(ctx context.Context, tx *db.Tx, m natsclient.Msg[T]) error
//
// (which receives the inbox-owned transaction handle) into the
// natsmap [Handler[T]] signature, with dedup against the inbox
// table happening transparently before fn runs.
//
// Typical use:
//
//	natsmap.RegisterHandler[Order](eng, "orders-sink",
//	    inboxnats.Wrap[Order](
//	        "orders-svc:order.created",  // consumer
//	        svc.DB,
//	        func(ctx context.Context, tx *db.Tx, m natsclient.Msg[Order]) error {
//	            return repo.Insert(ctx, tx, m.Data)
//	        },
//	    ))
//
// The wrapped handler:
//
//  1. Reads "Nats-Msg-Id" from msg.Headers (override via
//     [WithEventIDFn]).
//  2. Calls [inbox.Process] with Key{consumer, msgID}.
//  3. On [inbox.OutcomeProcessed] — the wrapper-supplied fn ran
//     inside the inbox transaction and committed. Returns nil.
//  4. On [inbox.OutcomeDuplicate] — fn did NOT run; the wrapper
//     returns nil so natsmap acks the redelivery.
//  5. On a missing message id — returns *errs.Error{Code:
//     [CodeMissingMessageID]}. natsmap propagates it as a Nak so
//     the publisher misconfig is loud, not silent.
//
// Lives at db/inbox/inboxnats so [db/inbox] core stays free of any
// natsmap import — symmetric to [auth/refreshpg] / [db/outbox/outboxnats].
package inboxnats
