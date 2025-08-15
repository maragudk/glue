package sql

import (
	"context"
	"embed"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing/fstest"

	"maragu.dev/migrate"
)

//go:embed migrations
var migrations embed.FS
var migrationsOnce sync.Once
var allMigrations fs.FS

func (h *Helper) MigrateUp(ctx context.Context) error {
	return migrate.Up(ctx, h.DB.DB, h.getMigrations())
}

func (h *Helper) MigrateDown(ctx context.Context) error {
	return migrate.Down(ctx, h.DB.DB, h.getMigrations())
}

// getMigrations both embedded here in this module, as well as in the client module.
func (h *Helper) getMigrations() fs.FS {
	migrationsOnce.Do(func() {
		migrationsDirs := []fs.FS{migrations}

		for _, path := range []string{"postgres/migrations", "../postgres/migrations", "sqlite/migrations", "../sqlite/migrations"} {
			ms := os.DirFS(path)
			matches, err := fs.Glob(ms, "*.sql")
			if err == nil && len(matches) > 0 {
				migrationsDirs = append(migrationsDirs, ms)
			}
		}

		var err error
		allMigrations, err = toMapFS(migrationsDirs...)
		if err != nil {
			panic(err)
		}
		migrationNames, err := fs.Glob(allMigrations, "*.sql")
		if err != nil {
			panic(err)
		}
		h.log.Info("Found migrations", "files", migrationNames)
	})
	return allMigrations
}

// toMapFS reads all files from the given [fs.FS] arguments into an [fstest.MapFS].
func toMapFS(filesystems ...fs.FS) (fstest.MapFS, error) {
	result := make(fstest.MapFS)

	for _, fsys := range filesystems {
		err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) (outErr error) {
			if err != nil {
				return err
			}

			if d.IsDir() {
				return nil
			}

			info, err := d.Info()
			if err != nil {
				return err
			}

			file, err := fsys.Open(path)
			if err != nil {
				return err
			}
			defer func() {
				if err := file.Close(); err != nil && outErr == nil {
					outErr = err
				}
			}()

			data, err := io.ReadAll(file)
			if err != nil {
				return err
			}

			base := filepath.Base(path)
			result[base] = &fstest.MapFile{
				Data:    data,
				Mode:    info.Mode(),
				ModTime: info.ModTime(),
				Sys:     info.Sys(),
			}

			return nil
		})

		if err != nil {
			return nil, err
		}
	}

	return result, nil
}
