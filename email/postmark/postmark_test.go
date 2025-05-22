package postmark_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"
	"maragu.dev/is"

	"maragu.dev/glue/email/postmark"
	"maragu.dev/glue/model"
)

func TestSender_SendTransactional(t *testing.T) {
	t.Run("returns error on status code 422 and errors from API", func(t *testing.T) {
		server, sender := newSender(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, err := w.Write([]byte(`{"ErrorCode":100, "Message":"Datacenter burning."}`))
			is.NotError(t, err)
		})
		defer server.Close()

		err := sender.SendTransactional(t.Context(), "You", "you@example.com", "Hi", "Hey there.", "generic", model.Keywords{})
		is.Equal(t, "error sending email, got error code 100", err.Error())
	})

	t.Run("returns error on 300+ HTTP status code from API", func(t *testing.T) {
		server, sender := newSender(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		defer server.Close()

		err := sender.SendTransactional(t.Context(), "You", "you@example.com", "Hi", "Hey there.", "generic", model.Keywords{})
		is.Equal(t, "error sending email, got http status code 500", err.Error())
	})

	t.Run("does not return error on inactive recipient", func(t *testing.T) {
		server, sender := newSender(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, err := w.Write([]byte(`{"ErrorCode":406, "Message":"Blerp."}`))
			is.NotError(t, err)
		})
		defer server.Close()

		err := sender.SendTransactional(t.Context(), "You", "you@example.com", "Hi", "Hey there.", "generic", model.Keywords{})
		is.NotError(t, err)
	})
}

func newSender(h http.HandlerFunc) (*httptest.Server, *postmark.Sender) {
	mux := chi.NewRouter()
	mux.Post("/email", h)
	server := httptest.NewServer(mux)
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
	return server, sender
}
