package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"maragu.dev/httph"

	"maragu.dev/glue/html"
)

type Server struct {
	baseURL            string
	csp                func(opts *httph.ContentSecurityPolicyOptions)
	htmlPage           html.PageFunc
	httpRouterInjector func(*Router)
	log                *slog.Logger
	permissionsGetter  permissionsGetter
	r                  *Router
	server             *http.Server
	userActiveChecker  userActiveChecker
}

type NewServerOptions struct {
	Address            string
	BaseURL            string
	CSP                func(opts *httph.ContentSecurityPolicyOptions)
	HTMLPage           html.PageFunc
	HTTPRouterInjector func(*Router)
	Log                *slog.Logger
	PermissionsGetter  permissionsGetter
	SecureCookie       bool
	SessionStore       scs.Store
	UserActiveChecker  userActiveChecker
	WriteTimeout       time.Duration
}

func NewServer(opts NewServerOptions) *Server {
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}

	if opts.Address == "" {
		opts.Address = ":8080"
	}

	if opts.WriteTimeout == 0 {
		opts.WriteTimeout = 10 * time.Second
	}

	mux := chi.NewRouter()

	sm := scs.New()
	if opts.SessionStore != nil {
		sm.Store = opts.SessionStore
	}
	sm.Lifetime = 365 * 24 * time.Hour
	sm.Cookie.Secure = opts.SecureCookie

	return &Server{
		baseURL:            opts.BaseURL,
		csp:                opts.CSP,
		htmlPage:           opts.HTMLPage,
		httpRouterInjector: opts.HTTPRouterInjector,
		log:                opts.Log,
		permissionsGetter:  opts.PermissionsGetter,
		r:                  &Router{Mux: mux, SM: sm},
		server: &http.Server{
			Addr:         opts.Address,
			ErrorLog:     slog.NewLogLogger(opts.Log.Handler(), slog.LevelError),
			Handler:      mux,
			IdleTimeout:  time.Minute,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: opts.WriteTimeout,
		},
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
