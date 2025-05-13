package http

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
)

type Server struct {
	log    *slog.Logger
	mux    *chi.Mux
	port   int
	server *http.Server
	sm     *scs.SessionManager
}

type NewServerOptions struct {
	Log          *slog.Logger
	Port         int
	SecureCookie bool
}

func NewServer(opts NewServerOptions) *Server {
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}

	if opts.Port == 0 {
		opts.Port = 8080
	}

	mux := chi.NewRouter()

	sm := scs.New()
	sm.Lifetime = 365 * 24 * time.Hour
	sm.Cookie.Secure = opts.SecureCookie
	sm.Cookie.SameSite = http.SameSiteStrictMode

	return &Server{
		log:  opts.Log,
		mux:  mux,
		port: opts.Port,
		server: &http.Server{
			Addr:         fmt.Sprintf(":%d", opts.Port),
			Handler:      mux,
			IdleTimeout:  time.Minute,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
		},
		sm: sm,
	}
}

// Start the server by setting up routes and listening on the supplied address.
func (s *Server) Start() error {
	s.log.Info("Starting server", "address", fmt.Sprintf("http://localhost:%d", s.port))

	s.registerRoutes()

	if err := s.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}

// Stop the Server gracefully, waiting for existing HTTP connections to finish.
func (s *Server) Stop() error {
	s.log.Info("Stopping server")

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	if err := s.server.Shutdown(ctx); err != nil {
		return err
	}

	s.log.Info("Stopped server")

	return nil
}
