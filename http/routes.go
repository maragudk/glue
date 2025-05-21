package http

import (
	"filippo.io/csrf"
	"github.com/go-chi/chi/v5/middleware"
	"maragu.dev/httph"
)

// setupRoutes as well as middleware.
func (s *Server) setupRoutes() {
	r := s.r

	r.Use(middleware.Compress(5))
	r.Use(middleware.RealIP)
	r.Use(middleware.RequestID)

	protection := csrf.New()
	if err := protection.AddTrustedOrigin(s.baseURL); err != nil {
		panic("error adding CSRF protection (with " + s.baseURL + "): " + err.Error())
	}
	r.Use(protection.Handler)

	r.NotFound(NotFound(s.htmlPage))

	r.Group(func(r *Router) {
		r.Use(httph.VersionedAssets)

		Static(r.Mux)
	})

	// HTML
	r.Group(func(r *Router) {
		r.Use(httph.NoClickjacking, httph.ContentSecurityPolicy(func(opts *httph.ContentSecurityPolicyOptions) {
			opts.ManifestSrc = "'self'"
			opts.ConnectSrc = "'self'"
		}))
		r.Use(s.sm.LoadAndSave, Authenticate(s.log, s.sm, s.userActiveChecker))

		if s.httpRouterInjector != nil {
			s.httpRouterInjector(r)
		}
	})
}
