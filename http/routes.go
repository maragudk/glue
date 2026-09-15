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
	// Kept despite deprecation to preserve the [http.Request.RemoteAddr] rewriting behavior.
	// Switching to the ClientIP* middlewares requires choosing a proxy trust model first.
	r.Use(middleware.RealIP) //nolint:staticcheck
	r.Use(OpenTelemetry)

	protection := http.NewCrossOriginProtection()
	if err := protection.AddTrustedOrigin(s.baseURL); err != nil {
		panic("error adding trusted origin to CrossOriginProtection middleware (with " + s.baseURL + "): " + err.Error())
	}
	r.Use(protection.Handler)

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

		// Registered inside this group so the not-found page is rendered with the session loaded and the
		// user authenticated, like any other HTML page. Chi stores it on the parent mux already wrapped
		// in this group's middleware, so routing elsewhere is unaffected.
		r.NotFound(NotFound(s.htmlPage))

		Logout(r, s.log, s.r.SM, s.htmlPage)

		r.Group(func(r *Router) {
			if s.httpRouterInjector != nil {
				s.httpRouterInjector(r)
			}
		})
	})
}
