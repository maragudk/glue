package http

import (
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
	"maragu.dev/httph"
)

// setupRoutes as well as middleware.
func (s *Server) setupRoutes() {
	r := s.r

	r.Use(middleware.Compress(5))
	r.Use(middleware.RealIP)
	r.Use(OpenTelemetry)

	protection := http.NewCrossOriginProtection()
	if err := protection.AddTrustedOrigin(s.baseURL); err != nil {
		panic("error adding trusted origin to CrossOriginProtection middleware (with " + s.baseURL + "): " + err.Error())
	}
	r.Use(protection.Handler)

	r.NotFound(NotFound(s.htmlPage))

	r.Group(func(r *Router) {
		r.Use(httph.VersionedAssets)

		Static(r.Mux)
	})

	// HTML
	r.Group(func(r *Router) {
		r.Use(httph.NoClickjacking, httph.ContentSecurityPolicy(s.csp))
		r.Use(s.r.SM.LoadAndSave, Authenticate(s.log, s.r.SM, s.userActiveChecker))

		if s.permissionsGetter != nil {
			r.Use(SavePermissionsInContext(s.log, s.permissionsGetter))
		}

		Logout(r, s.log, s.r.SM, s.htmlPage)

		r.Group(func(r *Router) {
			if s.httpRouterInjector != nil {
				s.httpRouterInjector(r)
			}
		})
	})
}
