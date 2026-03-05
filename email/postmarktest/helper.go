// Package postmarktest provides test helpers for the postmark package.
package postmarktest

import (
	"testing"

	"maragu.dev/env"

	"maragu.dev/glue/email"
	"maragu.dev/glue/email/postmark"
	"maragu.dev/glue/model"
)

// NewSender for testing.
func NewSender(t *testing.T) *postmark.Sender {
	t.Helper()

	if testing.Short() {
		t.SkipNow()
	}

	_ = env.Load("../../.env.test")

	return postmark.NewSender(postmark.NewSenderOptions{
		AppName:                   env.GetStringOrDefault("APP_NAME", "Test"),
		BaseURL:                   env.GetStringOrDefault("BASE_URL", "http://localhost:8080"),
		Emails:                    email.GetTemplates(),
		Key:                       env.GetStringOrDefault("POSTMARK_KEY", "test"),
		MarketingEmailAddress:     model.EmailAddress(env.GetStringOrDefault("MARKETING_EMAIL_ADDRESS", "marketing@example.com")),
		MarketingEmailName:        env.GetStringOrDefault("MARKETING_EMAIL_NAME", "Marketing"),
		ReplyToEmailAddress:       model.EmailAddress(env.GetStringOrDefault("REPLY_TO_EMAIL_ADDRESS", "support@example.com")),
		ReplyToEmailName:          env.GetStringOrDefault("REPLY_TO_EMAIL_NAME", "Support"),
		TransactionalEmailAddress: model.EmailAddress(env.GetStringOrDefault("TRANSACTIONAL_EMAIL_ADDRESS", "transactional@example.com")),
		TransactionalEmailName:    env.GetStringOrDefault("TRANSACTIONAL_EMAIL_NAME", "Transactional"),
	})
}
