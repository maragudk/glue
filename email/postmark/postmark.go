package postmark

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
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

// nameAndEmail combo, of the form "Name <email@example.com>"
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
		marketingFrom:     createNameAndEmail(opts.MarketingEmailName, opts.MarketingEmailAddress),
		replyTo:           createNameAndEmail(opts.ReplyToEmailName, opts.ReplyToEmailAddress),
		tracer:            otel.Tracer("maragu.dev/glue/email/postmark"),
		transactionalFrom: createNameAndEmail(opts.TransactionalEmailName, opts.TransactionalEmailAddress),
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

	ctx, span := s.operationTracerStart(ctx, "postmark.send", email.String(),
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

	// Keywords that are always included
	keywords["appName"] = s.appName
	keywords["baseURL"] = s.baseURL
	keywords["name"] = name

	err := s.sendRequest(ctx, requestBody{
		MessageStream: messageStream,
		From:          from,
		ReplyTo:       s.replyTo,
		To:            createNameAndEmail(name, email),
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
	ctx, span := s.operationTracerStart(ctx, "postmark.sendRequest", body.To,
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
			s.log.Info("Not sending email, recipient is inactive", "recipient", body.To)
			span.SetStatus(codes.Ok, "recipient inactive, email not sent")
			return nil
		default:
			s.log.Error("Error sending email, got error code", "error code", r.ErrorCode, "message", r.Message)
			err := errors.Newf("error sending email, got error code %v", r.ErrorCode)
			span.RecordError(err)
			span.SetStatus(codes.Error, "postmark error")
			return err
		}
	}

	if res.StatusCode >= 300 {
		s.log.Info("Error sending email, got http status code", "status code", res.StatusCode, "body", string(bodyAsBytes))
		err := errors.Newf("error sending email, got http status code %v", res.StatusCode)
		span.RecordError(err)
		span.SetStatus(codes.Error, "http error")
		return err
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
		email = strings.ReplaceAll(email, "{{"+keyword+"}}", template.HTMLEscapeString(replacement))
	}

	return email
}

func (s *Sender) operationTracerStart(ctx context.Context, operation, recipient string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	allOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("email.recipient", recipient),
		),
	}
	allOpts = append(allOpts, opts...)
	return s.tracer.Start(ctx, operation, allOpts...)
}

var _ email.Sender = (*Sender)(nil)
