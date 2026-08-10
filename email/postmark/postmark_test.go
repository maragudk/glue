package postmark_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"maragu.dev/is"

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

		err := sender.SendTransactional(t.Context(), "You", "you@example.com", "Hi", "Hey there.", "generic", model.Keywords{})
		is.Equal(t, "error sending email, got error code 100", err.Error())
	})

	t.Run("returns error on 300+ HTTP status code from API", func(t *testing.T) {
		sender, _ := newSender(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})

		err := sender.SendTransactional(t.Context(), "You", "you@example.com", "Hi", "Hey there.", "generic", model.Keywords{})
		is.Equal(t, "error sending email, got http status code 500", err.Error())
	})

	t.Run("does not return error on inactive recipient", func(t *testing.T) {
		sender, _ := newSender(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, err := w.Write([]byte(`{"ErrorCode":406, "Message":"Blerp."}`))
			is.NotError(t, err)
		})

		err := sender.SendTransactional(t.Context(), "You", "you@example.com", "Hi", "Hey there.", "generic", model.Keywords{})
		is.NotError(t, err)
	})

	t.Run("sends the configured from and reply-to on the transactional message stream", func(t *testing.T) {
		sender, rec := newSender(t, nil)

		err := sender.SendTransactional(t.Context(), "You", "you@example.com", "Hi", "Hey there.", "generic", model.Keywords{})
		is.NotError(t, err)

		body := rec.get()
		is.Equal(t, `"Transactionaler" <transactional@example.com>`, body.From)
		is.Equal(t, `"Supporter" <support@example.com>`, body.ReplyTo)
		is.Equal(t, "Hi", body.Subject)
		is.Equal(t, "outbound", body.MessageStream)
	})

	t.Run("lowercases the recipient email address", func(t *testing.T) {
		sender, rec := newSender(t, nil)

		err := sender.SendTransactional(t.Context(), "You", "YOU@Example.COM", "Hi", "Hey there.", "generic", model.Keywords{})
		is.NotError(t, err)

		is.Equal(t, `"You" <you@example.com>`, rec.get().To)
	})

	tests := []struct {
		name     string
		to       string
		expected string
	}{
		{
			name:     "renders a plain ASCII name as a quoted string",
			to:       "Markus",
			expected: `"Markus" <you@example.com>`,
		},
		{
			name:     "renders an empty name as just the address",
			to:       "",
			expected: `<you@example.com>`,
		},
		{
			name:     "quotes a name containing a comma",
			to:       "Doe, John",
			expected: `"Doe, John" <you@example.com>`,
		},
		{
			name:     "escapes double quotes and backslashes in a name",
			to:       `Ba"ck\slash`,
			expected: `"Ba\"ck\\slash" <you@example.com>`,
		},
		{
			name:     "quotes a name that tries to smuggle in a second recipient",
			to:       `You, "Friend" <evil@example.com>`,
			expected: `"You, \"Friend\" <evil@example.com>" <you@example.com>`,
		},
		{
			name:     "quotes a name with a tab that tries to smuggle in a second recipient",
			to:       "You\t<evil@example.com>",
			expected: "\"You\t<evil@example.com>\" <you@example.com>",
		},
		{
			// A line break in a name would otherwise end the header and let the rest of the name
			// start one of its own. Encoding the name leaves no raw CRLF to break on.
			name:     "encodes a name containing a line break, so it cannot start a header of its own",
			to:       "You\r\nBcc: evil@example.com",
			expected: `=?utf-8?b?WW91DQpCY2M6IGV2aWxAZXhhbXBsZS5jb20=?= <you@example.com>`,
		},
		{
			name:     "encodes a non-ASCII name as an encoded-word",
			to:       "Søren",
			expected: `=?utf-8?b?U8O4cmVu?= <you@example.com>`,
		},
		{
			name:     "encodes a non-ASCII name containing a backslash as an encoded-word",
			to:       `Sø\ren`,
			expected: `=?utf-8?b?U8O4XHJlbg==?= <you@example.com>`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sender, rec := newSender(t, nil)

			err := sender.SendTransactional(t.Context(), test.to, "you@example.com", "Hi", "Hey there.", "generic", model.Keywords{})
			is.NotError(t, err)

			to := rec.get().To
			is.Equal(t, test.expected, to)

			// Whatever the name contains, the rendered value must be one address, with the name
			// intact, and must carry no raw line break for a header of its own to start after.
			is.True(t, !strings.ContainsAny(to, "\r\n"), "rendered value has a raw line break")

			addresses, err := mail.ParseAddressList(to)
			is.NotError(t, err)
			is.Equal(t, 1, len(addresses))
			is.Equal(t, test.to, addresses[0].Name)
			is.Equal(t, "you@example.com", addresses[0].Address)
		})
	}
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

// recorder of the request body received by the test server.
type recorder struct {
	mu   sync.Mutex
	body requestBody
}

func (r *recorder) set(body requestBody) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.body = body
}

func (r *recorder) get() requestBody {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body
}

// newSender backed by a test server which records the request body before delegating to the given
// handler. A nil handler responds with a bare 200.
func newSender(t *testing.T, h http.HandlerFunc) (*postmark.Sender, *recorder) {
	t.Helper()

	var rec recorder

	mux := chi.NewRouter()
	mux.Post("/email", func(w http.ResponseWriter, r *http.Request) {
		var body requestBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error("error decoding request body:", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		rec.set(body)

		if h != nil {
			h(w, r)
		}
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	sender := postmark.NewSender(postmark.NewSenderOptions{
		BaseURL:                   "http://localhost:1234",
		Emails:                    os.DirFS("../emails"),
		EndpointURL:               server.URL + "/email",
		Key:                       "123abc",
		MarketingEmailAddress:     "marketing@example.com",
		MarketingEmailName:        "Marketer",
		ReplyToEmailAddress:       "support@example.com",
		ReplyToEmailName:          "Supporter",
		TransactionalEmailAddress: "transactional@example.com",
		TransactionalEmailName:    "Transactionaler",
	})

	return sender, &rec
}
