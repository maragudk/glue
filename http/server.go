package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"maragu.dev/glue/html"
)

type Server struct {
	baseURL            string
	htmlPage           html.PageFunc
	httpRouterInjector func(*Router)
	log                *slog.Logger
	r                  *Router
	server             *http.Server
	sessionStore       scs.Store
	sm                 *scs.SessionManager
	userActiveChecker  userActiveChecker
}

type NewServerOptions struct {
	Address            string
	BaseURL            string
	HTMLPage           html.PageFunc
	HTTPRouterInjector func(*Router)
	Log                *slog.Logger
	SecureCookie       bool
	SessionStore       scs.Store
	UserActiveChecker  userActiveChecker
}

func NewServer(opts NewServerOptions) *Server {
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}

	if opts.Address == "" {
		opts.Address = ":8080"
	}

	mux := chi.NewRouter()

	sm := scs.New()
	if opts.SessionStore != nil {
		sm.Store = opts.SessionStore
	}
	sm.Lifetime = 365 * 24 * time.Hour
	sm.Cookie.Secure = opts.SecureCookie
	sm.Cookie.SameSite = http.SameSiteStrictMode

	return &Server{
		baseURL:            opts.BaseURL,
		htmlPage:           opts.HTMLPage,
		httpRouterInjector: opts.HTTPRouterInjector,
		log:                opts.Log,
		r:                  &Router{Mux: mux, SM: sm},
		server: &http.Server{
			Addr:         opts.Address,
			Handler:      mux,
			IdleTimeout:  time.Minute,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
		},
		sm:                sm,
		userActiveChecker: opts.UserActiveChecker,
	}
}

// Start the server by setting up routes and listening on the supplied address.
func (s *Server) Start() error {
	s.log.Info("Starting server", "address", s.baseURL)

	s.setupRoutes()

	if err := s.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}

// Stop the Server gracefully, waiting for existing HTTP connections to finish.
func (s *Server) Stop(ctx context.Context) error {
	s.log.Info("Stopping server")

	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()

	if err := s.server.Shutdown(ctx); err != nil {
		return err
	}

	s.log.Info("Stopped server")

	return nil
}
