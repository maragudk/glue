# Diary: Quote display names per RFC 5322 in the postmark sender (issue 180)

The Postmark sender builds `From`, `ReplyTo`, and `To` header values as `fmt.Sprintf("%v <%v>", name, email)` with the display name unquoted. A name containing a comma, double quote, `<`, or `>` can terminate the display name early — in the worst case smuggling a second recipient into the `To` header. The fix is to render display names as RFC 5322 quoted strings, which the stdlib `net/mail` package (`mail.Address.String()`) already does correctly, including RFC 2047 encoding of non-ASCII names.

## Step 1: Refine requirements and set up the team

**Author:** main (lead)

### Prompt Context

**Verbatim prompt:** Have a look at issue 180
**Interpretation:** Read GitHub issue 180 in maragudk/glue, understand the problem, and drive a fix.
**Inferred intent:** Get the display-name quoting vulnerability in the Postmark sender fixed properly, with the lead refining requirements and a builder implementing.

### What I did

Read issue 180 (`postmark: quote display names per RFC 5322`), read `/email/postmark/postmark.go` and `/email/postmark/postmark_test.go`. Located the single culprit, `createNameAndEmail` at `/email/postmark/postmark.go:258`, which feeds all three headers: `From` and `ReplyTo` (built once in `NewSender`) and `To` (built per-send from user-supplied names). Confirmed with Markus to proceed with the stdlib `net/mail` approach, created the worktree `postmark-quote-display-names`, and started this diary.

### Why

The user-supplied `To` path is the dangerous one (header/recipient smuggling), but a single fix in `createNameAndEmail` covers all three headers. `mail.Address.String()` is the boring, battle-tested choice: it quotes only when needed, escapes `"` and `\`, and RFC 2047-encodes non-ASCII names.

### What worked

The issue was well-scoped and the code confirmed it exactly: one function, three call sites, no other headers assembled from names.

### What didn't work

Nothing failed at this stage.

### What I learned

Existing tests in `/email/postmark/postmark_test.go` only exercise error handling — no test asserts on the actual request payload sent to the Postmark API, so the vulnerable formatting was never pinned by a test.

### What was tricky

The only subtlety is non-ASCII names: RFC 5322 quoted strings only cover printable ASCII, so `mail.Address.String()` emits RFC 2047 encoded-words for names like "Søren". Postmark parses full RFC 5322 addresses, so this should be fine, but it is a consumer-visible change worth pinning in a test.

### What warrants review

Requirements handed to the builder: names containing `,`, `"`, `\`, `<`, `>` must render as valid RFC 5322 quoted strings with no recipient injection possible; plain ASCII names render unchanged; the email address is still lowercased; tests must capture and decode the JSON request body to assert on `To`/`From`/`ReplyTo`; a non-ASCII name test pins the encoded-word behavior. Also: empty name should render as `<email@example.com>` (stdlib behavior) rather than the current `" <email@example.com>"` with a leading space.

### Future work

None identified yet; scope is deliberately limited to `createNameAndEmail` with no API changes.

## Step 2: Render display names with `net/mail`, and pin the payload in tests

**Author:** postmark-builder

### Prompt Context

**Verbatim prompt:** Fix GitHub issue 180 in maragudk/glue: "postmark: quote display names per RFC 5322". [...] Render the display name per RFC 5322 using the stdlib `net/mail` package: `mail.Address{Name: name, Address: ...}.String()`. Do not hand-roll quoting. Keep lowercasing the email address. [...] Scope is exactly this one function. No public API changes, no new options. [...] If anything in the requirements seems wrong once you're in the code (e.g. `mail.Address.String()` output that Postmark's JSON API might mishandle), stop and ask me rather than improvising.

**Interpretation:** Replace the `fmt.Sprintf` formatting in `createNameAndEmail` with stdlib address rendering, and back it with tests that decode the JSON request body instead of only checking error handling.

**Inferred intent:** Close the recipient-smuggling hole for good, and leave a test that would catch it coming back.

### What I did

Rewrote `createNameAndEmail` in `/email/postmark/postmark.go` to build a `mail.Address` and render it, dropping the `fmt` import for `net/mail` and `mime`. Updated the `nameAndEmail` type comment, which described a shape the function no longer produces. Rewrote `/email/postmark/postmark_test.go` so the test server decodes the JSON body into a `requestBody` mirror and stores it in a small mutex-guarded `recorder`; `newSender` now takes `*testing.T`, owns the server lifetime through `t.Cleanup`, and accepts a nil handler for the common "just answer 200" case. Added a table of display-name cases plus subtests for the `From`/`ReplyTo`/`Subject`/`MessageStream` envelope and for address lowercasing. Every table case additionally asserts that `mail.ParseAddressList` recovers exactly one address with the name intact.

Ran `go test -shuffle on -race -count=2 ./email/...` (green), `golangci-lint run ./email/...` (0 issues), and `make fmt`. Then self-reviewed with two competing reviewer agents, verified both of their findings myself with throwaway programs rather than taking them on faith, and fixed what they turned up.

### Why

`mail.Address.String()` is the boring, battle-tested choice, and the round-trip assertion in every table case states the actual security property — one address in, one address out — rather than just freezing today's output string.

### What didn't work

Three things, in escalating order of how close they came to shipping.

First, an acceptance criterion was simply unachievable, and it has been formally amended. The task asked that plain ASCII names "render unchanged as `Markus <markus@example.com>`" while also forbidding hand-rolled quoting. Those two cannot both hold: Go's `mail.Address.String()` always wraps a non-empty printable-ASCII name in double quotes through its internal `quoteString`, and has no conditional-quoting path at all. Rendering `Markus` unquoted would mean writing our own needs-quoting check, which is exactly what the brief ruled out. Verified against go1.26.5:

    Name "Markus"    -> `"Markus" <you@example.com>`
    Name ""          -> `<you@example.com>`
    Name "Doe, John" -> `"Doe, John" <you@example.com>`

I stopped and put this to the lead rather than picking one criterion over the other, recommending stdlib: the quoted form is valid RFC 5322, `mail.ParseAddress` round-trips it to the same name, and Postmark's own docs use `"John Doe" <sender@example.com>` in their examples. The lead confirmed the amendment — **plain ASCII names now render as `"Markus" <markus@example.com>`** — on the grounds that keeping stdlib beats cosmetic header stability, and put it in the same category as the intentional empty-name change. Both are visible to consumers reading rendered headers, and neither changes who receives the mail.

Second, both reviewers independently found that a backslash in a name that also needs RFC 2047 encoding produces a header Go itself cannot parse. `mail.Address.String()` only falls back to B-encoding when the name contains one of ``"#$%&'(),.:;<>@[]^`{|}~``; a backslash is not in that set, so the name takes the Q-encoding path, and `mime.QEncoding` writes a backslash literally into the encoded-word:

    name `Sø\ren` -> `=?utf-8?q?S=C3=B8\ren?= <you@example.com>`
    mail.ParseAddressList: mail: missing '@' or angle-addr

The lead's criteria named both `\` and non-ASCII names; their intersection was broken, so I fixed it by B-encoding every name that needs encoding.

Third, and worst: that fix reintroduced the original vulnerability. `mime.WordEncoder.Encode` returns the input **unchanged** when it decides no encoding is needed, and its `needsEncoding` test exempts tab. My first predicate treated tab as non-printable, so a name containing a tab was routed to the encoder, passed straight through, and concatenated into the header raw and unquoted:

    name "You\t<evil@example.com>" -> `You\t<evil@example.com> <you@example.com>`  (two addresses)

No unit test caught this. A property check did, on the first run.

### What worked

Writing a throwaway property check instead of trusting either the reviewers or myself. It renders a name, then asserts four things: no raw CR or LF in the output, exactly one address parsed back, the address unchanged, and the name recovered byte-for-byte. Run over roughly 2.4M inputs — every rune from 0x00 to 0x2FF alone and paired with a backslash and with a non-ASCII rune, random strings over an alphabet of the nastiest characters, and lengths up to 1000 — it went from 16,457 failures to 0. It caught the tab regression immediately, and it also corrected me twice: I assumed a tab inside a quoted string would be folded to a space on parse, and it is not; and I assumed long encoded names might introduce CRLF folding, but `mime` joins split encoded-words with a plain space.

Competing reviewers earned their keep here. Both found the address hole and the backslash quirk independently, which is exactly the corroboration the review method is designed to produce.

### What I learned

`mail.Address.String()` and `mime.WordEncoder.Encode` have complementary notions of "printable", and tab is the seam between them: `mail` treats it as quotable whitespace, `mime` treats it as not needing encoding. Any code that routes between the two has to use a predicate that agrees with `mime`'s exactly, or names fall through the gap unencoded. `needsEncodedWord` now mirrors `mime.needsEncoding` character for character, and the doc comment says why rather than just what.

Also worth knowing: the `marketing` branch of `send` is unreachable from outside the package. There is no exported `SendMarketing`, so `marketingFrom` and the `broadcast` stream have no external test coverage. Pre-existing, not introduced here.

### What was tricky

Deciding how much to fix. The reviewers found that the email **address** is still emitted unescaped — `mail.Address.String()` quotes only the local part and passes everything after the last `@` through verbatim — so `you@example.com>, <evil` still yields two angle-addrs, a CRLF in the address still reaches the header raw, and a NUL is silently dropped so mail goes to a different mailbox than the caller asked for. Every fix for that leaves the stated scope: it needs validation, an error return from `createNameAndEmail`, propagation through `send`, and a decision about what `NewSender` does with three bad configured addresses. I reported it to the lead with reproductions rather than improvising, and kept the doc comment honest by claiming only what is true — that no *display name* can turn one address into several.

### What warrants review

The predicate in `needsEncodedWord` at `/email/postmark/postmark.go` is the load-bearing part; it must stay in agreement with `mime`'s `needsEncoding`, and the tab case in the test table is what pins that. Every table case also asserts that the rendered value carries no raw CR or LF, so a name like `You\r\nBcc: evil@example.com` cannot end the header and start one of its own. That protection comes free from encoding the name, which makes it easy to lose to a well-meaning simplification — the assertion and the case comment exist to stop that. Worth a look too: display names are now B-encoded rather than Q-encoded for all non-ASCII names, so `Søren` goes out as `=?utf-8?b?U8O4cmVu?=`. That is valid and decodes correctly, but nobody has yet confirmed against the live Postmark API that recipients see `Søren` rather than the raw encoded-word — `/email/postmarktest/helper.go` provides a harness for one real send if that is worth settling before release.

### Future work

Validate the email address, not just the display name. This is the open half of the same hole and is written up in the message to the lead: decide between `model.EmailAddress.IsValid()` (already rejects all three payloads, but also rejects `a@localhost`, IDN, and address literals, so it is a behaviour change for consumers) and a narrower guard against `\r`, `\n`, `,`, `<`, and `>`. Separately, `SendMarketing` does not exist, leaving the `marketing` path dead code.

## Step 3: Guard the address against restructuring the header

**Author:** postmark-builder

### Prompt Context

**Verbatim prompt:** Decision from Markus: fold a NARROW guard into this branch. Not full validation — `model.EmailAddress.IsValid()` stays out of the sender; `a@localhost`, IDN, and address literals must keep working. [...] Property to enforce: a supplied address must not be able to alter header structure. Reject any address that could terminate the angle-addr, introduce a second address, or put raw control characters into the header [...] Reject, don't sanitize — silently rewriting an address risks delivering to the wrong mailbox [...] `createNameAndEmail` returns an error; `send` propagates it wrapped with context. `NewSender` panics on an invalid configured address.

**Interpretation:** Close the address half of the injection hole from step 2 with a structural check only, not an address validator, and let bad configured addresses fail at construction.

**Inferred intent:** Make the `To` header structurally safe end to end without narrowing which addresses the library will send to.

### What I did

Added `structuralRune` to `/email/postmark/postmark.go`, which returns the first rune of an address that would change the structure of the header: `<`, `>`, `,`, any C0 control, or DEL. `createNameAndEmail` now returns `(nameAndEmail, error)` and refuses such an address; `Sender.send` propagates the error wrapped as "error creating recipient", recording it on the span like the other failures in that function. `NewSender` builds its three addresses through a new `mustCreateNameAndEmail`, which panics with the name of the option that carried the bad address.

In `/email/postmark/postmark_test.go`, added a table of eight rejected addresses covering all three original reproductions plus `<`, `,`, tab, and DEL, a table of five accepted addresses covering `you@localhost`, both address literal forms, an IDN, and a semicolon, and a table over the three configured options asserting the panic names the right one.

Ran `go test -shuffle on -race -count=2 ./...` (green), `golangci-lint run` (0 issues), `make fmt`, and a second round of competing reviewers over the new code.

### Why

The scope was set deliberately narrow. `model.EmailAddress.IsValid()` would have rejected all three attacks, but it also rejects `a@localhost`, internationalised domains, and address literals, so using it would have quietly narrowed which addresses the library can send to. The guard instead asks one question — what would this address do to the header around it — and answers it the same way for every address.

### What worked

Mutation testing the new tests, prompted by a reviewer finding that the DEL branch was dead. Deleting `|| r == '\x7f'` left the whole suite green before I added a case for it; with the case, that mutation fails, and blanking the entire condition fails all eight rejection cases and all three panic cases cleanly. Extending the property check to addresses also paid: ~1.6M name/address pairs, every rune spliced into five positions in the address plus random strings over a hostile alphabet, asserting that an accepted address always renders to exactly one angle-addr at the end of the value with no delimiter or line break inside it. Zero failures, and all four address forms the brief protects still render.

### What didn't work

Nothing failed outright, but two reviewer findings showed the first draft was wrong in ways the tests did not catch.

The first was a comment defect with teeth. `mustCreateNameAndEmail` was documented as panicking "because addresses configured on the sender are read once at startup, where a bad one should stop the program". Both reviewers flagged it as a package looking outward at its consumers, which `CLAUDE.md` forbids, and one pointed out it is false even inside this repo: `/email/postmarktest/helper.go` calls `NewSender` per test, from environment variables. The comment now says only what the function does, and the rationale lives here.

The second was that `is.Equal(t, test.expected, err.Error())` dereferences `err` before anything checks it is non-nil. A reviewer demonstrated by mutation that a regressed guard made the first rejection case nil-panic, which aborts the test binary — so instead of eight failures naming what broke, you get one stack trace. Each case now asserts `err != nil` first.

### What I learned

`mail.Address.String()` quotes the local part of an address but copies the domain verbatim, and those two halves fail differently. In the domain, a delimiter reaches the header intact. In the local part, `quoteString` does not escape control characters — it drops them, so `a\r\nb@example.com` renders as `"ab"@example.com` and the mail goes to a mailbox nobody asked for. That is why the control check covers the whole address while the delimiters only matter in the domain, and it is the sharpest argument for rejecting rather than sanitising.

Also: RFC 5322 group syntax is not reachable here. A semicolon cannot open a group because the display name is always followed immediately by an angle-addr, and any second address a payload tries to introduce gets pulled into the quoted local part, because `mail.Address.String()` splits on the *last* `@`. The semicolon case in the accepted table records that, so the next reader does not have to re-derive why `;` is safely out of the reject set.

### What was tricky

Knowing where to stop. The guard rejects `<`, `>`, and `,` across the whole address, which technically over-rejects RFC 5321-legal quoted local parts like `"very,unusual"@example.com` — `mail.Address.String()` would quote those safely. Narrowing the delimiter scan to the domain would be more precise, but it means splitting the address on the last `@` and reasoning about two rules instead of one, to buy support for addresses nobody in practice uses. I kept the single rule and said so in the doc comment rather than pretending the check is exact.

### What warrants review

`structuralRune` is deliberately not an address validator, and the doc comment says so; the risk over time is someone "completing" it into one and breaking `you@localhost` or IDN addresses, which the accepted table exists to prevent. Worth a look too: the address is still lowercased in full, including the local part, which RFC 5321 §2.4 leaves case-sensitive to the destination host, so `John.Doe@example.com` is delivered as `john.doe@example.com`. That is pre-existing and the brief asked to keep it, but it sits oddly next to a guard whose rationale is not silently changing which mailbox is addressed. It is visible in the accepted table, where `you@[IPv6:2001:db8::1]` renders with a lowercased `ipv6:` tag.

### Future work

Three things, none of them in scope here. Lowercasing could be narrowed to the domain, which would leave the local part as the sender gave it. `NewSender` panics on `MarketingEmailAddress` even though nothing can send marketing mail, since there is no `SendMarketing` — the dead `marketing` branch of `send` is worth either finishing or removing. And a consumer that builds a `Sender` from runtime configuration has no way to check an address before handing it over, since the check is unexported; exporting it would give them an alternative to `recover()`, but that is a public API change and was explicitly out of scope.
