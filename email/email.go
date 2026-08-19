package email

import (
	"context"
	"embed"
	"io/fs"

	"maragu.dev/glue/model"
)

//go:embed emails
var emails embed.FS

func GetTemplates() fs.FS {
	emails, err := fs.Sub(emails, "emails")
	if err != nil {
		panic(err)
	}
	return emails
}

// SendOptions for one email.
// Template names the email to render, with Keywords replaced in it. The name, appName, and baseURL
// keywords are filled in by the sender, name from ToName, and cannot be set here.
// ReplyTo and ReplyToName set the reply-to address for this email, and an empty ReplyTo leaves the
// choice of reply-to to the sender.
type SendOptions struct {
	Keywords    model.Keywords
	Preheader   string
	ReplyTo     model.EmailAddress
	ReplyToName string
	Subject     string
	Template    string
	To          model.EmailAddress
	ToName      string
}

type Sender interface {
	SendTransactional(ctx context.Context, opts SendOptions) error
}
