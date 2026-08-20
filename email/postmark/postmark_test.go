package postmark_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"maragu.dev/is"

	"maragu.dev/glue/email"
	"maragu.dev/glue/email/postmark"
	"maragu.dev/glue/model"
)

func TestSender_SendTransactional(t *testing.T) {
	t.Run("returns error on status code 422 and errors from API", func(t *testing.T) {
		sender, _ := newSender(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, err := w.Write([]byte(`{"ErrorCode":100, "Message":"Datacenter burning."}`))
			is.NotError(t, err)
		})

		err := sender.SendTransactional(t.Context(), newSendOptions())
		is.Equal(t, "error sending email, got error code 100", err.Error())
	})

	t.Run("returns error on 300+ HTTP status code from API", func(t *testing.T) {
		sender, _ := newSender(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})

		err := sender.SendTransactional(t.Context(), newSendOptions())
		is.Equal(t, "error sending email, got http status code 500", err.Error())
	})

	t.Run("does not return error on inactive recipient", func(t *testing.T) {
		sender, _ := newSender(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, err := w.Write([]byte(`{"ErrorCode":406, "Message":"Blerp."}`))
			is.NotError(t, err)
		})

		err := sender.SendTransactional(t.Context(), newSendOptions())
		is.NotError(t, err)
	})

	t.Run("sends the configured from and reply-to on the transactional message stream", func(t *testing.T) {
		sender, rec := newSender(t, nil)

		err := sender.SendTransactional(t.Context(), newSendOptions())
		is.NotError(t, err)

		body := rec.get()
		is.Equal(t, `"Transactionaler" <transactional@example.com>`, body.From)
		is.Equal(t, `"Supporter" <support@example.com>`, body.ReplyTo)
		is.Equal(t, "Hi", body.Subject)
		is.Equal(t, "outbound", body.MessageStream)
	})

	t.Run("lowercases the recipient email address", func(t *testing.T) {
		sender, rec := newSender(t, nil)

		opts := newSendOptions()
		opts.To = "YOU@Example.COM"

		err := sender.SendTransactional(t.Context(), opts)
		is.NotError(t, err)

		is.Equal(t, `"You" <you@example.com>`, rec.get().To)
	})

	// The keywords and the preheader go in, and the layout and the email under them stay markup, or
	// every email would arrive as a page of angle brackets.
	t.Run("replaces keywords in the email and leaves its markup alone", func(t *testing.T) {
		sender, rec := newSender(t, nil)

		opts := newSendOptions()
		opts.Keywords = model.Keywords{"title": "Hello", "content": "World"}

		err := sender.SendTransactional(t.Context(), opts)
		is.NotError(t, err)

		body := rec.get().HtmlBody
		is.True(t, strings.Contains(body, "<h1>Hello</h1>"), "title keyword was not replaced")
		is.True(t, strings.Contains(body, "<p>World</p>"), "content keyword was not replaced")
		is.True(t, strings.Contains(body, `<span class="preheader">Hey there.</span>`), "preheader was not replaced")
	})

	// The preheader and the keywords are both text a sender hands over, and both are escaped where
	// they go into the email, so neither can bring markup of its own into it.
	substituted := []struct {
		name      string
		preheader string
		keywords  model.Keywords
		expected  string
	}{
		{
			name:      "escapes the preheader",
			preheader: `<script>alert("hi")</script>`,
			expected:  `<span class="preheader">&lt;script&gt;alert(&#34;hi&#34;)&lt;/script&gt;</span>`,
		},
		{
			name:      "escapes a keyword",
			preheader: "Hey there.",
			keywords:  model.Keywords{"title": `<script>alert("hi")</script>`},
			expected:  `<h1>&lt;script&gt;alert(&#34;hi&#34;)&lt;/script&gt;</h1>`,
		},
	}

	for _, test := range substituted {
		t.Run(test.name, func(t *testing.T) {
			sender, rec := newSender(t, nil)

			opts := newSendOptions()
			opts.Preheader = test.preheader
			opts.Keywords = test.keywords

			err := sender.SendTransactional(t.Context(), opts)
			is.NotError(t, err)

			body := rec.get().HtmlBody
			is.True(t, strings.Contains(body, test.expected), "substituted text was not escaped")
			is.True(t, !strings.Contains(body, "<script>"), "substituted text brought markup into the email")
		})
	}

	// The sender fills in these three itself, from what it and the send were given, and the templates
	// under emails use them.
	t.Run("replaces the always-included keywords in the email", func(t *testing.T) {
		sender, rec := newSender(t, nil)

		opts := newSendOptions()
		opts.Template = "new-email-notification"
		opts.ToName = "You"

		err := sender.SendTransactional(t.Context(), opts)
		is.NotError(t, err)

		body := rec.get().HtmlBody
		is.True(t, strings.Contains(body, "<h1>Hi You!</h1>"), "name keyword was not replaced")
		is.True(t, strings.Contains(body, ">Appy</a>"), "appName keyword was not replaced")
		is.True(t, strings.Contains(body, `href="http://localhost:1234"`), "baseURL keyword was not replaced")
	})

	t.Run("leaves the given keywords alone", func(t *testing.T) {
		sender, _ := newSender(t, nil)

		opts := newSendOptions()
		opts.Keywords = model.Keywords{"title": "Hello"}

		err := sender.SendTransactional(t.Context(), opts)
		is.NotError(t, err)

		is.Equal(t, 1, len(opts.Keywords))
		is.Equal(t, "Hello", opts.Keywords["title"])
	})

	// Nothing an address can be made to say from outside produces any of these, so each one is a
	// mistake in the code that called, and panics rather than failing the send.
	panics := []struct {
		name     string
		adjust   func(opts *email.SendOptions)
		expected string
	}{
		{
			name:     "panics without a template",
			adjust:   func(opts *email.SendOptions) { opts.Template = "" },
			expected: "no email template given",
		},
		{
			name: "panics without a recipient email address",
			adjust: func(opts *email.SendOptions) {
				opts.To = " "
				opts.ToName = ""
			},
			expected: "no recipient email address given",
		},
		{
			name:     "panics on a recipient name without an address",
			adjust:   func(opts *email.SendOptions) { opts.To = "" },
			expected: "no recipient email address given",
		},
		{
			// Falling back to the configured reply-to here would answer a name with somebody else's
			// address.
			name:     "panics on a reply-to name without an address",
			adjust:   func(opts *email.SendOptions) { opts.ReplyToName = "Someone" },
			expected: `the reply-to has a display name "Someone" but no email address`,
		},
	}

	for _, test := range panics {
		t.Run(test.name, func(t *testing.T) {
			sender, rec := newSender(t, nil)

			opts := newSendOptions()
			test.adjust(&opts)

			defer func() {
				r := recover()
				is.True(t, r != nil, "SendTransactional did not panic")

				message, ok := r.(string)
				is.True(t, ok, "panic value is not a string")
				is.Equal(t, test.expected, message)

				is.Equal(t, 0, rec.requests())
			}()

			_ = sender.SendTransactional(t.Context(), opts)
		})
	}

	t.Run("sends the given reply-to instead of the configured one, lowercased", func(t *testing.T) {
		sender, rec := newSender(t, nil)

		opts := newSendOptions()
		opts.ReplyTo = "Someone@Example.com"
		opts.ReplyToName = "Someone"

		err := sender.SendTransactional(t.Context(), opts)
		is.NotError(t, err)

		is.Equal(t, `"Someone" <someone@example.com>`, rec.get().ReplyTo)
	})

	t.Run("sends the given reply-to without a name", func(t *testing.T) {
		sender, rec := newSender(t, nil)

		opts := newSendOptions()
		opts.ReplyTo = "someone@example.com"

		err := sender.SendTransactional(t.Context(), opts)
		is.NotError(t, err)

		is.Equal(t, `<someone@example.com>`, rec.get().ReplyTo)
	})

	t.Run("encodes a reply-to name like a recipient name", func(t *testing.T) {
		sender, rec := newSender(t, nil)

		opts := newSendOptions()
		opts.ReplyTo = "someone@example.com"
		opts.ReplyToName = "Søren\r\nBcc: evil@example.com"

		err := sender.SendTransactional(t.Context(), opts)
		is.NotError(t, err)

		replyTo := rec.get().ReplyTo
		is.Equal(t, `=?utf-8?b?U8O4cmVuDQpCY2M6IGV2aWxAZXhhbXBsZS5jb20=?= <someone@example.com>`, replyTo)

		addresses, err := mail.ParseAddressList(replyTo)
		is.NotError(t, err)
		is.Equal(t, 1, len(addresses))
		is.Equal(t, "someone@example.com", addresses[0].Address)
	})

	t.Run("leaves out the reply-to when neither the sender nor the send has one", func(t *testing.T) {
		endpointURL, rec := newServer(t, nil)

		sender := postmark.NewSender(postmark.NewSenderOptions{
			Emails:                    os.DirFS("../emails"),
			EndpointURL:               endpointURL,
			TransactionalEmailAddress: "transactional@example.com",
		})

		err := sender.SendTransactional(t.Context(), newSendOptions())
		is.NotError(t, err)

		var fields map[string]any
		is.NotError(t, json.Unmarshal(rec.json(), &fields))
		_, found := fields["ReplyTo"]
		is.True(t, !found, "a reply-to was sent")
	})

	// An address can be made hostile by whoever supplies it, so a refused one fails the send and does
	// not panic: panicking would hand anyone who can influence an address a way to stop the process.
	t.Run("returns error on a reply-to address that would change the structure of the header", func(t *testing.T) {
		sender, rec := newSender(t, nil)

		opts := newSendOptions()
		opts.ReplyTo = "someone@example.com>, <evil@example.com"

		err := sender.SendTransactional(t.Context(), opts)
		is.True(t, err != nil, "address was not rejected")
		is.Equal(t, `error creating reply-to: email address contains '>', which would change the structure of the header`, err.Error())

		is.Equal(t, 0, rec.requests())
	})

	tests := []struct {
		name        string
		displayName string
		expected    string
	}{
		{
			name:        "renders a plain ASCII name as a quoted string",
			displayName: "Markus",
			expected:    `"Markus" <you@example.com>`,
		},
		{
			name:        "renders an empty name as just the address",
			displayName: "",
			expected:    `<you@example.com>`,
		},
		{
			name:        "quotes a name containing a comma",
			displayName: "Doe, John",
			expected:    `"Doe, John" <you@example.com>`,
		},
		{
			name:        "escapes double quotes and backslashes in a name",
			displayName: `Ba"ck\slash`,
			expected:    `"Ba\"ck\\slash" <you@example.com>`,
		},
		{
			name:        "quotes a name that tries to smuggle in a second recipient",
			displayName: `You, "Friend" <evil@example.com>`,
			expected:    `"You, \"Friend\" <evil@example.com>" <you@example.com>`,
		},
		{
			name:        "quotes a name with a tab that tries to smuggle in a second recipient",
			displayName: "You\t<evil@example.com>",
			expected:    "\"You\t<evil@example.com>\" <you@example.com>",
		},
		{
			// A line break in a name would otherwise end the header and let the rest of the name
			// start one of its own. Encoding the name leaves no raw CRLF to break on.
			name:        "encodes a name containing a line break, so it cannot start a header of its own",
			displayName: "You\r\nBcc: evil@example.com",
			expected:    `=?utf-8?b?WW91DQpCY2M6IGV2aWxAZXhhbXBsZS5jb20=?= <you@example.com>`,
		},
		{
			name:        "encodes a non-ASCII name as an encoded-word",
			displayName: "Søren",
			expected:    `=?utf-8?b?U8O4cmVu?= <you@example.com>`,
		},
		{
			name:        "encodes a non-ASCII name containing a backslash as an encoded-word",
			displayName: `Sø\ren`,
			expected:    `=?utf-8?b?U8O4XHJlbg==?= <you@example.com>`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sender, rec := newSender(t, nil)

			opts := newSendOptions()
			opts.ToName = test.displayName

			err := sender.SendTransactional(t.Context(), opts)
			is.NotError(t, err)

			to := rec.get().To
			is.Equal(t, test.expected, to)

			// Whatever the name contains, the rendered value must be one address, with the name
			// intact, and must carry no raw line break for a header of its own to start after.
			is.True(t, !strings.ContainsAny(to, "\r\n"), "rendered value has a raw line break")

			addresses, err := mail.ParseAddressList(to)
			is.NotError(t, err)
			is.Equal(t, 1, len(addresses))
			is.Equal(t, test.displayName, addresses[0].Name)
			is.Equal(t, "you@example.com", addresses[0].Address)
		})
	}

	// The display name is quoted or encoded, but the address goes into the header as given, so an
	// address that would restructure the header around it has to be refused outright. Rewriting it
	// into something acceptable risks delivering to a mailbox nobody asked for.
	rejected := []struct {
		name     string
		address  model.EmailAddress
		expected string
	}{
		{
			name:     "rejects an address that closes the angle-addr to append a second recipient",
			address:  "you@example.com>, <evil@example.com",
			expected: `error creating recipient: email address contains '>', which would change the structure of the header`,
		},
		{
			name:     "rejects an address with a line break that would start a header of its own",
			address:  "you@example.com\r\nBcc: evil@example.com",
			expected: `error creating recipient: email address contains '\r', which would change the structure of the header`,
		},
		{
			name:     "rejects an address with a blank line that would start the body",
			address:  "you@example.com\r\n\r\nEvil body",
			expected: `error creating recipient: email address contains '\r', which would change the structure of the header`,
		},
		{
			name:     "rejects an address with a null byte, which would silently change the mailbox",
			address:  "yo\x00u@example.com",
			expected: `error creating recipient: email address contains '\x00', which would change the structure of the header`,
		},
		{
			name:     "rejects an address with a delete, which would silently change the mailbox",
			address:  "yo\x7fu@example.com",
			expected: `error creating recipient: email address contains '\x7f', which would change the structure of the header`,
		},
		{
			name:     "rejects an address opening a second angle-addr",
			address:  "you@example.com <evil@example.com",
			expected: `error creating recipient: email address contains '<', which would change the structure of the header`,
		},
		{
			name:     "rejects an address separating a second recipient with a comma",
			address:  "you@example.com,evil@example.com",
			expected: `error creating recipient: email address contains ',', which would change the structure of the header`,
		},
		{
			name:     "rejects an address with a tab",
			address:  "you@example.com\tevil",
			expected: `error creating recipient: email address contains '\t', which would change the structure of the header`,
		},
	}

	for _, test := range rejected {
		t.Run(test.name, func(t *testing.T) {
			sender, rec := newSender(t, nil)

			opts := newSendOptions()
			opts.To = test.address

			err := sender.SendTransactional(t.Context(), opts)
			is.True(t, err != nil, "address was not rejected")
			is.Equal(t, test.expected, err.Error())

			// The address is refused before anything reaches Postmark.
			is.Equal(t, 0, rec.requests())
		})
	}

	// The check is narrow on purpose: it asks only what the address would do to the header around it,
	// never whether the address is one Postmark can deliver to.
	accepted := []struct {
		name     string
		address  model.EmailAddress
		expected string
	}{
		{
			name:     "accepts an address without a dotted domain",
			address:  "you@localhost",
			expected: `"You" <you@localhost>`,
		},
		{
			name:     "accepts an IPv4 address literal",
			address:  "you@[192.168.1.1]",
			expected: `"You" <you@[192.168.1.1]>`,
		},
		{
			// The address is lowercased, so the standardised IPv6 tag comes out as "ipv6".
			name:     "accepts an IPv6 address literal, colons and all",
			address:  "you@[IPv6:2001:db8::1]",
			expected: `"You" <you@[ipv6:2001:db8::1]>`,
		},
		{
			name:     "accepts an internationalised domain name",
			address:  "you@例え.jp",
			expected: `"You" <you@例え.jp>`,
		},
		{
			// A semicolon ends a group in RFC 5322, but a display name is always followed by an
			// angle-addr here, so there is no group for it to end, and any second address it tries to
			// introduce is pulled into the quoted local part.
			name:     "accepts a semicolon, which cannot open a group from inside the angle-addr",
			address:  "you@example.com;evil@example.com",
			expected: `"You" <"you@example.com;evil"@example.com>`,
		},
	}

	for _, test := range accepted {
		t.Run(test.name, func(t *testing.T) {
			sender, rec := newSender(t, nil)

			opts := newSendOptions()
			opts.To = test.address

			err := sender.SendTransactional(t.Context(), opts)
			is.NotError(t, err)

			to := rec.get().To
			is.Equal(t, test.expected, to)

			// An accepted address still has to leave the header with one address in it and no way to
			// start another, even where a stricter parser would not accept the address itself.
			is.True(t, !strings.ContainsAny(to, "\r\n"), "rendered value has a raw line break")
			is.Equal(t, 1, strings.Count(to, "<"))
			is.True(t, strings.HasSuffix(to, ">"), "rendered value does not end in an angle-addr")
			is.True(t, !strings.ContainsAny(to[strings.LastIndex(to, "<"):], ","), "angle-addr can introduce another address")
		})
	}
}

func TestNewSender(t *testing.T) {
	// Every configured address goes through the same check, and the panic has to say which one to fix.
	options := []struct {
		name     string
		opts     postmark.NewSenderOptions
		expected string
	}{
		{
			name:     "names the marketing address in the panic",
			opts:     postmark.NewSenderOptions{MarketingEmailAddress: "marketing@example.com,evil@example.com"},
			expected: `error creating name and email for MarketingEmailAddress: email address contains ',', which would change the structure of the header`,
		},
		{
			name:     "names the reply-to address in the panic",
			opts:     postmark.NewSenderOptions{ReplyToEmailAddress: "support@example.com>, <evil@example.com"},
			expected: `error creating name and email for ReplyToEmailAddress: email address contains '>', which would change the structure of the header`,
		},
		{
			name:     "names the transactional address in the panic",
			opts:     postmark.NewSenderOptions{TransactionalEmailAddress: "transactional@example.com\rBcc: evil@example.com"},
			expected: `error creating name and email for TransactionalEmailAddress: email address contains '\r', which would change the structure of the header`,
		},
	}

	for _, test := range options {
		t.Run(test.name, func(t *testing.T) {
			defer func() {
				r := recover()
				is.True(t, r != nil, "NewSender did not panic")

				// The refused address is what went wrong, so the panic carries that error itself.
				err, ok := r.(error)
				is.True(t, ok, "panic value is not an error")
				is.Equal(t, test.expected, err.Error())
			}()

			postmark.NewSender(test.opts)
		})
	}

	// The second entry point for the name-without-address rule: without it the sender would be built
	// with the name dropped and no reply-to at all. Nothing went wrong further down to carry up here,
	// so this one panics with a message rather than an error.
	t.Run("panics on a configured name without its address", func(t *testing.T) {
		defer func() {
			r := recover()
			is.True(t, r != nil, "NewSender did not panic")

			message, ok := r.(string)
			is.True(t, ok, "panic value is not a string")
			is.Equal(t, `ReplyToEmailAddress has a display name "Supporter" but no email address`, message)
		}()

		postmark.NewSender(postmark.NewSenderOptions{ReplyToEmailName: "Supporter"})
	})

	t.Run("does not panic on ordinary configured email addresses", func(t *testing.T) {
		postmark.NewSender(postmark.NewSenderOptions{
			MarketingEmailAddress:     "marketing@example.com",
			ReplyToEmailAddress:       "support@localhost",
			TransactionalEmailAddress: "transactional@[192.168.1.1]",
		})
	})
}

// requestBody sent to the Postmark API.
// See https://postmarkapp.com/developer/user-guide/send-email-with-api
type requestBody struct {
	MessageStream string
	From          string
	To            string
	ReplyTo       string
	Subject       string
	TextBody      string
	HtmlBody      string
}

// recorder of the request bodies received by the test server.
type recorder struct {
	mu    sync.Mutex
	body  requestBody
	count int
	raw   []byte
}

func (r *recorder) set(body requestBody, raw []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.body = body
	r.raw = raw
	r.count++
}

func (r *recorder) get() requestBody {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body
}

// json of the last request body, for telling a field that was left out from one that was sent empty.
func (r *recorder) json() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.raw
}

// requests received so far.
func (r *recorder) requests() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count
}

// newServer which records the request body before delegating to the given handler, returning the
// endpoint URL to point a sender at and the recorder. A nil handler responds with a bare 200.
func newServer(t *testing.T, h http.HandlerFunc) (string, *recorder) {
	t.Helper()

	var rec recorder

	mux := chi.NewRouter()
	mux.Post("/email", func(w http.ResponseWriter, r *http.Request) {
		bodyAsBytes, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error("error reading request body:", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		var body requestBody
		if err := json.Unmarshal(bodyAsBytes, &body); err != nil {
			t.Error("error decoding request body:", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		rec.set(body, bodyAsBytes)

		if h != nil {
			h(w, r)
		}
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	return server.URL + "/email", &rec
}

// newSender with every address configured, backed by [newServer].
func newSender(t *testing.T, h http.HandlerFunc) (*postmark.Sender, *recorder) {
	t.Helper()

	endpointURL, rec := newServer(t, h)

	sender := postmark.NewSender(postmark.NewSenderOptions{
		AppName:                   "Appy",
		BaseURL:                   "http://localhost:1234",
		Emails:                    os.DirFS("../emails"),
		EndpointURL:               endpointURL,
		Key:                       "123abc",
		MarketingEmailAddress:     "marketing@example.com",
		MarketingEmailName:        "Marketer",
		ReplyToEmailAddress:       "support@example.com",
		ReplyToEmailName:          "Supporter",
		TransactionalEmailAddress: "transactional@example.com",
		TransactionalEmailName:    "Transactionaler",
	})

	return sender, rec
}

// newSendOptions for an ordinary email, which a test can adjust one field at a time.
func newSendOptions() email.SendOptions {
	return email.SendOptions{
		Preheader: "Hey there.",
		Subject:   "Hi",
		Template:  "generic",
		To:        "you@example.com",
		ToName:    "You",
	}
}
