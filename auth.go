package auth

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-errors/errors"
	"github.com/go-pkgz/rest"

	"github.com/go-pkgz/auth/avatar"
	"github.com/go-pkgz/auth/middleware"
	"github.com/go-pkgz/auth/provider"
	"github.com/go-pkgz/auth/token"
)

// Service provides higher level wrapper allowing to construct everything and get back token middleware
type Service struct {
	opts           Opts
	jwtService     *token.Service
	providers      []provider.Service
	authMiddleware middleware.Authenticator
	avatarProxy    *avatar.Proxy
	issuer         string
}

// Opts is a full set of all parameters to initialize Service
type Opts struct {
	SecretReader   token.Secret        // reader returns secret for given site id (aud)
	ClaimsUpd      token.ClaimsUpdater // updater for jwt to add/modify values stored in the token
	SecureCookies  bool                // makes jwt cookie secure
	TokenDuration  time.Duration       // token's TTL, refreshed automatically
	CookieDuration time.Duration       // cookie's TTL. This cookie stores JWT token
	DisableXSRF    bool                // disable XSRF protection, useful for testing/debugging

	// optional (custom) names for cookies and headers
	JWTCookieName  string // default "JWT"
	JWTHeaderKey   string // default "X-JWT"
	XSRFCookieName string // default "XSRF-TOKEN"
	XSRFHeaderKey  string // default "X-XSRF-TOKEN"

	Issuer string // optional value for iss claim, usually the application name, default "go-pkgz/auth"

	URL       string               // root url for the rest service, i.e. http://blah.example.com
	Validator middleware.Validator // validator allows to reject some valid tokens with user-defined logic
	DevPasswd string               // if presented, allows basic auth with user dev and given password

	AvatarStore avatar.Store // store to save/load avatars
}

// NewService initializes everything
func NewService(opts Opts) (*Service, error) {

	// check mandatory options
	if opts.SecretReader == nil {
		return nil, errors.New("SecretReader not defined")
	}

	jwtService := token.NewService(token.Opts{
		SecretReader:   opts.SecretReader,
		ClaimsUpd:      opts.ClaimsUpd,
		SecureCookies:  opts.SecureCookies,
		TokenDuration:  opts.TokenDuration,
		CookieDuration: opts.CookieDuration,
		DisableXSRF:    opts.DisableXSRF,
		JWTCookieName:  opts.JWTCookieName,
		JWTHeaderKey:   opts.JWTHeaderKey,
		XSRFCookieName: opts.XSRFCookieName,
		XSRFHeaderKey:  opts.XSRFHeaderKey,
		Issuer:         opts.Issuer,
	})

	res := Service{
		opts:       opts,
		jwtService: jwtService,
		authMiddleware: middleware.Authenticator{
			JWTService: jwtService,
			Validator:  opts.Validator,
			DevPasswd:  opts.DevPasswd,
		},
	}

	if opts.Issuer == "" {
		res.issuer = "go-pkgz/auth"
	}

	if opts.AvatarStore != nil {
		res.avatarProxy = &avatar.Proxy{
			Store:     opts.AvatarStore,
			URL:       opts.URL,
			RoutePath: "/avatar",
		}
	}

	return &res, nil
}

// Handlers gets http.Handler for all providers and avatars
func (s *Service) Handlers() (authHandler http.Handler, avatarHandler http.Handler) {

	providerHandler := func(w http.ResponseWriter, r *http.Request) {
		elems := strings.Split(r.URL.Path, "/")
		if len(elems) < 2 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// list all providers
		if elems[len(elems)-1] == "list" {
			list := []string{}
			for _, p := range s.providers {
				list = append(list, p.Name)
			}
			rest.RenderJSON(w, r, list)
			return
		}

		// allow logout without specifying provider
		if elems[len(elems)-1] == "logout" {
			s.providers[0].Handler(w, r)
			return
		}

		provName := elems[len(elems)-2]
		p, err := s.Provider(provName)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			rest.RenderJSON(w, r, rest.JSON{"error": fmt.Sprintf("provider %s not supported", provName)})
			return
		}
		p.Handler(w, r)
	}

	return http.HandlerFunc(providerHandler), http.HandlerFunc(s.avatarProxy.Handler)
}

// Middleware returns token middleware
func (s *Service) Middleware() middleware.Authenticator {
	return s.authMiddleware
}

// AddProvider adds provider for given name
func (s *Service) AddProvider(name string, cid string, csecret string) {

	p := provider.Params{
		URL:         s.opts.URL,
		JwtService:  s.jwtService,
		Issuer:      s.issuer,
		AvatarProxy: s.avatarProxy,
		Cid:         cid,
		Csecret:     csecret,
	}

	switch strings.ToLower(name) {
	case "github":
		s.providers = append(s.providers, provider.NewGithub(p))
	case "google":
		s.providers = append(s.providers, provider.NewGoogle(p))
	case "facebook":
		s.providers = append(s.providers, provider.NewFacebook(p))
	case "yandex":
		s.providers = append(s.providers, provider.NewFacebook(p))
	case "dev":
		s.providers = append(s.providers, provider.NewDev(p))
	default:
		return
	}

	s.authMiddleware.Providers = s.providers
}

// Provider gets provider by name
func (s *Service) Provider(name string) (provider.Service, error) {
	for _, p := range s.providers {
		if p.Name == name {
			return p, nil
		}
	}
	return provider.Service{}, errors.Errorf("provider %s not found", name)
}
