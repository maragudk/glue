# Project Decisions

This document records significant architectural and design decisions made throughout the project's development.

## 2026-08-10: The `postmark` sender guards header structure but does not validate email addresses

While fixing display-name quoting per RFC 5322 (issue #180, PR #182), review found that the email address itself also reaches the `To`/`From`/`ReplyTo` headers verbatim, so a hostile address could smuggle a second recipient or split the header. We had to decide how much the sender should check.

Alternatives considered:

- Full validation with `model.EmailAddress.IsValid()`: rejects all known attack payloads, but also rejects working addresses such as `a@localhost`, internationalised domain names, and address literals — a behaviour change with real breakage risk for consumers.
- Narrow structural guard: reject only characters that can restructure the header (`<`, `>`, `,`, C0 controls, DEL). Everything else passes through as given.

Decision: the narrow guard, and reject rather than sanitise. `createNameAndEmail` returns an error for a structurally dangerous address (`NewSender` panics on a bad configured address, matching the existing fail-fast-on-bad-config pattern), because silently rewriting an address risks delivering to a different mailbox than the caller asked for. The sender deliberately makes no claim about address validity or deliverability — that remains the application's concern. A consequence is mild over-rejection: RFC 5321-legal quoted local parts like `"very,unusual"@example.com` are refused, accepted as the price of one simple rule instead of two.

## 2026-08-19: Sending an email takes an options struct, not positional parameters

Adding a per-send reply-to address to `email.Sender.SendTransactional` needed two more values — an address and a display name — on a method that already took seven positional parameters, three of them adjacent bare strings (`subject`, `preheader`, `template`).

Alternatives considered:

- Two more positional parameters: no migration for callers who ignore reply-to, but nine parameters, and the transposition hazard among the adjacent strings gets worse.
- Variadic functional options (`SendTransactional(ctx, ..., opts ...SendOption)`): backwards compatible, but introduces an option-function mechanism the module uses nowhere outside test helpers, and leaves the seven parameters in place.
- One options struct, as every constructor in the module already takes.

Decision: `SendTransactional(ctx context.Context, opts email.SendOptions) error`. The struct lives in `email` beside the `Sender` interface it parameterises, orders its fields alphabetically like `NewSenderOptions`, and gives reply-to the same treatment as any other field, so later additions cost no signature change. Breaking every call site is acceptable here — all consumers are known — and the module's constructors already establish the shape.

Two consequences were accepted deliberately. Losing arity checking means a dropped field is a runtime problem rather than a compile error, so `send` refuses an empty `Template` and an empty recipient address instead of panicking inside template rendering or spending a round trip to learn it from the API. And `Keywords` is copied before the sender adds its own, so the given map comes back untouched; it may be nil.

An empty address now yields an empty name-and-email combo rather than `<@>`, which is what an unconfigured reply-to used to put in every request. A display name with no address is an error instead, at both send time and construction time, because answering a named person at some other address is the failure this package already refuses elsewhere.
