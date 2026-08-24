# Project Decisions

This document records significant architectural and design decisions made throughout the project's development.

## 2026-08-10: The `postmark` sender guards header structure but does not validate email addresses

While fixing display-name quoting per RFC 5322 (issue #180, PR #182), review found that the email address itself also reaches the `To`/`From`/`ReplyTo` headers verbatim, so a hostile address could smuggle a second recipient or split the header. We had to decide how much the sender should check.

Alternatives considered:

- Full validation with `model.EmailAddress.IsValid()`: rejects all known attack payloads, but also rejects working addresses such as `a@localhost`, internationalised domain names, and address literals — a behaviour change with real breakage risk for consumers.
- Narrow structural guard: reject only characters that can restructure the header (`<`, `>`, `,`, C0 controls, DEL). Everything else passes through as given.

Decision: the narrow guard, and reject rather than sanitise. `createNameAndEmail` returns an error for a structurally dangerous address (`NewSender` panics on a bad configured address, matching the existing fail-fast-on-bad-config pattern), because silently rewriting an address risks delivering to a different mailbox than the caller asked for. The sender deliberately makes no claim about address validity or deliverability — that remains the application's concern. A consequence is mild over-rejection: RFC 5321-legal quoted local parts like `"very,unusual"@example.com` are refused, accepted as the price of one simple rule instead of two.

## 2026-08-24: A client's trace context and baggage never become this service's own

Honeycomb showed every `POST /mcp` server span in production parented to a trace we never see: the client sent `traceparent`, `otelhttp` extracted it, and our main spans dangled off a missing root. One client even grouped two MCP calls and a token refresh under a single trace ID of its own choosing (issue #197). A client outside our control was deciding both the grouping and the sampling of our telemetry.

Alternatives considered:

- Stop configuring propagators globally: kills outbound injection and the enqueue-time trace context the `jobs` queue carries, so a job would no longer join the request's trace. Far too broad.
- `otelhttp.WithPublicEndpointFn` answering true for every request: the literal "every server span is a root" reading. But `trace.WithNewRoot` severs *any* parent in the context, and `otelhttp` links only a parent which is remote, so a span started above the middleware in the same process — another copy of this middleware, a consumer's own tracing middleware, an in-process `ServeHTTP` — would be dropped with nothing recording that it existed.
- Answering true only when the extracted span context is remote.

Decision: the last one. `OpenTelemetry` passes `otelhttp.WithPublicEndpointFn(func(r *http.Request) bool { return trace.SpanContextFromContext(r.Context()).IsRemote() })`, unconditional in the sense that matters — there is no option to turn it off, since no client is trusted enough to parent our spans. A request which brought trace context gets a trace root of its own plus a span link to the remote context, which keeps the correlation available without granting control. A request whose context already holds a local span keeps that span as its parent, because that parent is this process's own and severing it would lose a real connection for nothing.

`otelhttp` v0.65.0 has no plain `WithPublicEndpoint()` any more, only the function form, so the predicate was the available shape regardless.

Baggage is the same problem wearing different clothes, and gets a blunter answer. It is client-controlled key-value state which rides the context into handlers, out through anything the propagator is injected into, and into the job envelopes the queue persists, so a client which sets it writes into places it has no business reaching.

The precise alternative was to discard only the baggage the request carried, preserving any the context already held so that an in-process `ServeHTTP` kept its own. It distinguishes baggage by when it entered the context rather than where it came from, which leaves a hole — tracing above the middleware which extracts baggage itself defeats the discard — and it is machinery for a use nothing has.

Decision: discard all of it. `OpenTelemetry` hands `otelhttp.WithPropagators` a `discardBaggage`, which delegates extraction to the global propagator and then returns the context with no baggage at all. No baggage crosses the HTTP layer, whatever its source, and no hole is left where one source slips through. Because the discard happens during extraction rather than below it, the baggage is gone before the server span starts, so a sampler or span processor does not see it either. `Inject` and `Fields` delegate to the global propagator, since `discardBaggage` has to satisfy `propagation.TextMapPropagator` whole; only extraction changes. Outbound propagation and the queue's enqueue-time trace context go through the global propagator directly and are untouched.

Baggage set while handling a request still propagates from there, but that is a consequence rather than a supported feature. Carrying baggage through the HTTP layer would be a deliberate change to make when something actually needs it.
