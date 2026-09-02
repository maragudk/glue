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

	// The plain not-found handler. chi hands a copy of it to every group and mounted sub-router set up
	// below which has none of its own, and a request reaching a mounted sub-router has run the HTML
	// group's middleware on the way in, so that copy must not carry the same middleware again. Without
	// this line, a sub-router answers an unmatched path with chi's own plain-text 404 instead. The
	// root's copy is replaced from inside the HTML group below, where the middleware is missing.
	r.NotFound(NotFound(s.htmlPage))

	r.Group(func(r *Router) {
		r.Use(httph.VersionedAssets)

		Static(r.Mux)
	})

	// HTML
	r.Group(func(r *Router) {
		r.Use(httph.NoClickjacking, httph.ContentSecurityPolicy(s.csp))
		// [NoStoreIfNonce] goes under the session middleware, which adds a Cache-Control of its own
		// whenever it writes a session cookie, and which does so through the writer of whatever is
		// above it. From up there its header would read as one the handler chose, and no-store would
		// be skipped on every response that touches the session. From here it also covers the error
		// responses the two middlewares below produce.
		r.Use(s.r.SM.LoadAndSave, NoStoreIfNonce, Authenticate(s.log, s.r.SM, s.userActiveChecker))

		if s.permissionsGetter != nil {
			r.Use(SavePermissionsInContext(s.log, s.permissionsGetter))
		}

		// Registering the not-found handler from inside this group installs it on the router this
		// group hangs off, wrapped in this group's middleware, so a request matching no route at all
		// renders the not-found page with the same headers, session, user and permissions as a page
		// that matched. Two things follow: the wrapping takes the middleware registered above this
		// line, so this has to come after all of it, and the group has to hang off the root router
		// directly, since a group one level further down would install onto its own parent instead,
		// which routes nothing.
		r.NotFound(NotFound(s.htmlPage))

		Logout(r, s.log, s.r.SM, s.htmlPage)

		r.Group(func(r *Router) {
			if s.httpRouterInjector != nil {
				s.httpRouterInjector(r)
			}
		})
	})
}
