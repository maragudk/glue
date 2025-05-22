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

type Sender interface {
	SendTransactional(ctx context.Context, name string, email model.EmailAddress, subject, preheader, template string, kw model.Keywords) error
}
