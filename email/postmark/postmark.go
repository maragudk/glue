package postmark

import (
	"bytes"
	"context"
	"encoding/json"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
	"go.opentelemetry.io/otel/trace"
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

// nameAndEmail combo, of the form "Name" <email@example.com>, where the name is either a quoted
// string or an RFC 2047 encoded-word, and is left out entirely when empty.
type nameAndEmail = string

// Sender can send transactional and marketing emails through Postmark.
// See https://postmarkapp.com/developer
type Sender struct {
	appName           string
	baseURL           string
	client            *http.Client
	emails            fs.FS
	endpointURL       string
	key               string
	log               *slog.Logger
	marketingFrom     nameAndEmail
	replyTo           nameAndEmail
	tracer            trace.Tracer
	transactionalFrom nameAndEmail
}

type NewSenderOptions struct {
	AppName                   string
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
		appName:           strings.TrimSpace(opts.AppName),
		baseURL:           strings.TrimSuffix(opts.BaseURL, "/"),
		client:            &http.Client{Timeout: 3 * time.Second},
		emails:            opts.Emails,
		endpointURL:       strings.TrimSuffix(opts.EndpointURL, "/"),
		key:               opts.Key,
		log:               opts.Log,
		marketingFrom:     mustCreateNameAndEmail("MarketingEmailAddress", opts.MarketingEmailName, opts.MarketingEmailAddress),
		replyTo:           mustCreateNameAndEmail("ReplyToEmailAddress", opts.ReplyToEmailName, opts.ReplyToEmailAddress),
		tracer:            otel.Tracer("maragu.dev/glue/email/postmark"),
		transactionalFrom: mustCreateNameAndEmail("TransactionalEmailAddress", opts.TransactionalEmailName, opts.TransactionalEmailAddress),
	}
}

func (s *Sender) SendTransactional(ctx context.Context, name string, email model.EmailAddress, subject, preheader, template string, kw model.Keywords) error {
	return s.send(ctx, transactional, name, email, subject, preheader, template, kw)
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

func (s *Sender) send(ctx context.Context, typ emailType, name string, email model.EmailAddress, subject, preheader, template string, keywords model.Keywords) error {
	var emailTypeStr string
	switch typ {
	case marketing:
		emailTypeStr = "marketing"
	case transactional:
		emailTypeStr = "transactional"
	}

	ctx, span := s.operationTracerStart(ctx, "postmark.send",
		trace.WithAttributes(
			attribute.String("email.type", emailTypeStr),
			attribute.String("email.template", template),
		),
	)
	defer span.End()

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

	to, err := createNameAndEmail(name, email)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "invalid recipient")
		return errors.Wrap(err, "error creating recipient")
	}

	// Keywords that are always included
	keywords["appName"] = s.appName
	keywords["baseURL"] = s.baseURL
	keywords["name"] = name

	err = s.sendRequest(ctx, requestBody{
		MessageStream: messageStream,
		From:          from,
		ReplyTo:       s.replyTo,
		To:            to,
		Subject:       subject,
		HtmlBody:      getEmail(s.emails, template, preheader, keywords),
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "send failed")
		return err
	}

	return nil
}

type postmarkResponse struct {
	ErrorCode int
	Message   string
}

// send using the Postmark API.
func (s *Sender) sendRequest(ctx context.Context, body requestBody) error {
	ctx, span := s.operationTracerStart(ctx, "postmark.sendRequest",
		trace.WithAttributes(
			attribute.String("postmark.message_stream", body.MessageStream),
			semconv.HTTPRequestMethodPost,
			semconv.URLFull(s.endpointURL),
		),
	)
	defer span.End()

	bodyAsBytes, err := json.Marshal(body)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "marshal failed")
		return errors.Wrap(err, "error marshalling request body to json")
	}

	span.SetAttributes(semconv.HTTPRequestBodySize(len(bodyAsBytes)))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpointURL, bytes.NewReader(bodyAsBytes))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "request creation failed")
		return errors.Wrap(err, "error creating request")
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Postmark-Server-Token", s.key)

	res, err := s.client.Do(req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "request failed")
		return errors.Wrap(err, "error making request")
	}
	defer func() {
		_ = res.Body.Close()
	}()
	bodyAsBytes, err = io.ReadAll(res.Body)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "read response failed")
		return errors.Wrap(err, "error reading response body")
	}

	span.SetAttributes(
		semconv.HTTPResponseStatusCode(res.StatusCode),
		semconv.HTTPResponseBodySize(len(bodyAsBytes)),
	)

	// https://postmarkapp.com/developer/api/overview#response-codes
	if res.StatusCode == http.StatusUnprocessableEntity {
		var r postmarkResponse
		if err := json.Unmarshal(bodyAsBytes, &r); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "unmarshal error response failed")
			return errors.Wrap(err, "error unwrapping postmark error response body")
		}

		span.SetAttributes(attribute.Int("postmark.error_code", r.ErrorCode))

		// https://postmarkapp.com/developer/api/overview#error-codes
		switch r.ErrorCode {
		case 406:
			s.log.InfoContext(ctx, "Not sending email, recipient is inactive", "recipient", body.To)
			span.SetStatus(codes.Ok, "recipient inactive, email not sent")
			return nil
		default:
			s.log.ErrorContext(ctx, "Error sending email, got error code", "error code", r.ErrorCode, "message", r.Message)
			err := errors.Newf("error sending email, got error code %v", r.ErrorCode)
			span.RecordError(err)
			span.SetStatus(codes.Error, "postmark error")
			return err
		}
	}

	if res.StatusCode >= 300 {
		s.log.InfoContext(ctx, "Error sending email, got http status code", "status code", res.StatusCode, "body", string(bodyAsBytes))
		err := errors.Newf("error sending email, got http status code %v", res.StatusCode)
		span.RecordError(err)
		span.SetStatus(codes.Error, "http error")
		return err
	}

	return nil
}

// createNameAndEmail returns a name and email string ready for inserting into From and To fields, and
// an error if [structuralRune] finds a character in the address that would change the structure of
// the header. The address is lowercased and trimmed of surrounding whitespace first.
// A printable ASCII display name becomes an RFC 5322 quoted string, any other name an RFC 2047
// encoded-word, and an empty name is left out entirely, so no display name can end its own quoted
// string or encoded-word. Between that and the check on the address, neither part can turn one
// address into several or end the header line. Nothing here checks that the address is deliverable
// or even well-formed, and an address that passes is used exactly as given.
func createNameAndEmail(name string, email model.EmailAddress) (nameAndEmail, error) {
	address := mail.Address{Address: email.ToLower().String()}

	if r, found := structuralRune(address.Address); found {
		return "", errors.Newf("email address contains %q, which would change the structure of the header", r)
	}

	// [mail.Address.String] reaches for Q-encoding for most names that need RFC 2047 encoding, and
	// Q-encoding leaves a backslash bare, which makes the encoded-word unparseable. B-encoding has no
	// such gap, so encode those names here and leave only the address to [mail.Address.String].
	if needsEncodedWord(name) {
		return mime.BEncoding.Encode("utf-8", name) + " " + address.String(), nil
	}

	address.Name = name
	return address.String(), nil
}

// mustCreateNameAndEmail as [createNameAndEmail], panicking instead of returning an error, and naming
// the option the address came from so the panic says which one to fix.
func mustCreateNameAndEmail(option, name string, email model.EmailAddress) nameAndEmail {
	combo, err := createNameAndEmail(name, email)
	if err != nil {
		panic(errors.Wrap(err, "error creating name and email for %v", option))
	}
	return combo
}

// structuralRune returns the first rune of the email address that would change the structure of the
// header it goes into, and whether there was one. Angle brackets and the comma delimit addresses, and
// [mail.Address.String] copies the domain of an address into the header verbatim, so one placed there
// would end the address, start another, or split it in two. Control characters are refused across the
// whole address for two reasons: a carriage return or line feed in the domain would end the header
// line, and [mail.Address.String] quotes the local part without escaping controls, dropping them
// instead, which would send the mail to a different mailbox than the one asked for.
func structuralRune(email string) (rune, bool) {
	for _, r := range email {
		if r == '<' || r == '>' || r == ',' || r < ' ' || r == '\x7f' {
			return r, true
		}
	}
	return 0, false
}

// needsEncodedWord reports whether the display name has to become an RFC 2047 encoded-word because a
// quoted string cannot carry it. The condition is the one [mime.WordEncoder.Encode] uses to decide
// whether to encode at all: a name it would pass through unchanged must go down the quoting path
// instead, or it would end up in the header raw and unquoted.
func needsEncodedWord(name string) bool {
	for _, r := range name {
		if (r < ' ' || r > '~') && r != '\t' {
			return true
		}
	}
	return false
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
		email = strings.ReplaceAll(email, "{{"+keyword+"}}", template.HTMLEscapeString(replacement))
	}

	return email
}

func (s *Sender) operationTracerStart(ctx context.Context, operation string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	allOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindClient),
	}
	allOpts = append(allOpts, opts...)
	return s.tracer.Start(ctx, operation, allOpts...)
}

var _ email.Sender = (*Sender)(nil)
