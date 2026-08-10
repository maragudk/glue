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
