# glue

This is maragu.dev/glue, a Go module with glue code for applications. It's an opinionated and personal framework for web applications in Go. I use it for all my web apps. It is specifically not a general-purpose framework, so there are many things that are intentionally not configurable.

This also means that breaking changes are okay, since all users of the module are well-known and controlled.

The primary storage is either SQLite or PostgreSQL, for which there is setup code and helpers in packages `sql`, `sqlitetest`, `postgrestest`.

Background jobs use a queue in the database, and supporting code is in `jobs`.

HTTP server, handlers, and middleware is in `http`. HTTP session storage is set up in `sqlitestore` or `postgresstore`. HTML is generated with gomponents in the `html` package.

Transactional emails are sent using postmark, see `email`.

Object storage code is in the `s3` and `s3test` packages.
