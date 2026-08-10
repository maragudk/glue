# Project Decisions

This document records significant architectural and design decisions made throughout the project's development.

## 2026-08-10: The `postmark` sender guards header structure but does not validate email addresses

While fixing display-name quoting per RFC 5322 (issue #180, PR #182), review found that the email address itself also reaches the `To`/`From`/`ReplyTo` headers verbatim, so a hostile address could smuggle a second recipient or split the header. We had to decide how much the sender should check.

Alternatives considered:

- Full validation with `model.EmailAddress.IsValid()`: rejects all known attack payloads, but also rejects working addresses such as `a@localhost`, internationalised domain names, and address literals — a behaviour change with real breakage risk for consumers.
- Narrow structural guard: reject only characters that can restructure the header (`<`, `>`, `,`, C0 controls, DEL). Everything else passes through as given.

Decision: the narrow guard, and reject rather than sanitise. `createNameAndEmail` returns an error for a structurally dangerous address (`NewSender` panics on a bad configured address, matching the existing fail-fast-on-bad-config pattern), because silently rewriting an address risks delivering to a different mailbox than the caller asked for. The sender deliberately makes no claim about address validity or deliverability — that remains the application's concern. A consequence is mild over-rejection: RFC 5321-legal quoted local parts like `"very,unusual"@example.com` are refused, accepted as the price of one simple rule instead of two.
