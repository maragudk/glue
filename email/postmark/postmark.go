package postmark

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"maragu.dev/errors"

	"maragu.dev/glue/email"
	"maragu.dev/glue/model"
)

const (
	marketingMessageStream     = "broadcast"
	transactionalMessageStream = "outbound"
)

type emailType int

const (
	marketing emailType = iota
	transactional
)

// nameAndEmail combo, of the form "Name <email@example.com>"
type nameAndEmail = string

// Sender can send transactional and marketing emails through Postmark.
// See https://postmarkapp.com/developer
type Sender struct {
	baseURL           string
	client            *http.Client
	emails            fs.FS
	endpointURL       string
	key               string
	log               *slog.Logger
	marketingFrom     nameAndEmail
	replyTo           nameAndEmail
	transactionalFrom nameAndEmail
}

type NewSenderOptions struct {
	BaseURL                   string
	EndpointURL               string
	Emails                    fs.FS
	Key                       string
	Log                       *slog.Logger
	MarketingEmailAddress     model.EmailAddress
	MarketingEmailName        string
	ReplyToEmailAddress       model.EmailAddress
	ReplyToEmailName          string
	TransactionalEmailAddress model.EmailAddress
	TransactionalEmailName    string
}

func NewSender(opts NewSenderOptions) *Sender {
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}

	if opts.EndpointURL == "" {
		opts.EndpointURL = "https://api.postmarkapp.com/email"
	}

	return &Sender{
		baseURL:           strings.TrimSuffix(opts.BaseURL, "/"),
		client:            &http.Client{Timeout: 3 * time.Second},
		emails:            opts.Emails,
		endpointURL:       strings.TrimSuffix(opts.EndpointURL, "/"),
		key:               opts.Key,
		log:               opts.Log,
		marketingFrom:     createNameAndEmail(opts.MarketingEmailName, opts.MarketingEmailAddress),
		replyTo:           createNameAndEmail(opts.ReplyToEmailName, opts.ReplyToEmailAddress),
		transactionalFrom: createNameAndEmail(opts.TransactionalEmailName, opts.TransactionalEmailAddress),
	}
}

func (s *Sender) SendTransactional(ctx context.Context, name string, email model.EmailAddress, subject, preheader, template string, kw model.Keywords) error {
	return s.send(ctx, transactional, createNameAndEmail(name, email), subject, preheader, template, kw)
}

// requestBody used in [Sender.send].
// See https://postmarkapp.com/developer/user-guide/send-email-with-api
type requestBody struct {
	MessageStream string
	From          nameAndEmail
	To            nameAndEmail
	ReplyTo       nameAndEmail
	Subject       string
	TextBody      string
	HtmlBody      string
}

func (s *Sender) send(ctx context.Context, typ emailType, to nameAndEmail, subject, preheader, template string, keywords model.Keywords) error {
	var messageStream string
	var from nameAndEmail
	switch typ {
	case marketing:
		from = s.marketingFrom
		messageStream = marketingMessageStream
	case transactional:
		from = s.transactionalFrom
		messageStream = transactionalMessageStream
	}

	// Keywords that are always included
	keywords["baseURL"] = s.baseURL

	err := s.sendRequest(ctx, requestBody{
		MessageStream: messageStream,
		From:          from,
		ReplyTo:       s.replyTo,
		To:            to,
		Subject:       subject,
		HtmlBody:      getEmail(s.emails, template, preheader, keywords),
	})

	return err
}

type postmarkResponse struct {
	ErrorCode int
	Message   string
}

// send using the Postmark API.
func (s *Sender) sendRequest(ctx context.Context, body requestBody) error {
	bodyAsBytes, err := json.Marshal(body)
	if err != nil {
		return errors.Wrap(err, "error marshalling request body to json")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpointURL, bytes.NewReader(bodyAsBytes))
	if err != nil {
		return errors.Wrap(err, "error creating request")
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Postmark-Server-Token", s.key)

	res, err := s.client.Do(req)
	if err != nil {
		return errors.Wrap(err, "error making request")
	}
	defer func() {
		_ = res.Body.Close()
	}()
	bodyAsBytes, err = io.ReadAll(res.Body)
	if err != nil {
		return errors.Wrap(err, "error reading response body")
	}

	// https://postmarkapp.com/developer/api/overview#response-codes
	if res.StatusCode == http.StatusUnprocessableEntity {
		var r postmarkResponse
		if err := json.Unmarshal(bodyAsBytes, &r); err != nil {
			return errors.Wrap(err, "error unwrapping postmark error response body")
		}

		// https://postmarkapp.com/developer/api/overview#error-codes
		switch r.ErrorCode {
		case 406:
			s.log.Info("Not sending email, recipient is inactive", "recipient", body.To)
			return nil
		default:
			s.log.Error("Error sending email, got error code", "error code", r.ErrorCode, "message", r.Message)
			return errors.Newf("error sending email, got error code %v", r.ErrorCode)
		}
	}

	if res.StatusCode >= 300 {
		s.log.Info("Error sending email, got http status code", "status code", res.StatusCode, "body", string(bodyAsBytes))
		return errors.Newf("error sending email, got http status code %v", res.StatusCode)
	}

	return nil
}

// createNameAndEmail returns a name and email string ready for inserting into From and To fields.
func createNameAndEmail(name string, email model.EmailAddress) nameAndEmail {
	return fmt.Sprintf("%v <%v>", name, email.ToLower())
}

// getEmail from the given path, panicking on errors.
// It also replaces keywords given in the map.
// Email preheader text should be between 40-130 characters long.
func getEmail(emails fs.FS, path, preheader string, keywords model.Keywords) string {
	emailBody, err := fs.ReadFile(emails, path+".html")
	if err != nil {
		panic(err)
	}

	layout, err := fs.ReadFile(emails, "layout.html")
	if err != nil {
		panic(err)
	}

	email := string(layout)
	email = strings.ReplaceAll(email, "{{preheader}}", preheader)
	email = strings.ReplaceAll(email, "{{body}}", string(emailBody))

	if _, ok := keywords["unsubscribe"]; ok {
		email = strings.ReplaceAll(email, "{{unsubscribe}}", "{{{ pm:unsubscribe }}}")
	} else {
		email = strings.ReplaceAll(email, "{{unsubscribe}}", "")
	}

	for keyword, replacement := range keywords {
		email = strings.ReplaceAll(email, "{{"+keyword+"}}", replacement)
	}

	return email
}

var _ email.Sender = (*Sender)(nil)
