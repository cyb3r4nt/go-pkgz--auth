package auth

import (
	"io/ioutil"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/go-pkgz/auth/avatar"
	"github.com/go-pkgz/auth/middleware"
	"github.com/go-pkgz/auth/provider"
	"github.com/go-pkgz/auth/token"
)

func TestNewService(t *testing.T) {

	options := Opts{
		SecretReader:   token.SecretFunc(func(id string) (string, error) { return "secret", nil }),
		TokenDuration:  time.Hour,
		CookieDuration: time.Hour * 24,
		Issuer:         "my-test-app",
		URL:            "http://127.0.0.1:8080",
		AvatarStore:    avatar.NewLocalFS("/tmp", 120),
	}

	_, err := NewService(options)
	assert.NoError(t, err)
}

func TestNewServiceFailed(t *testing.T) {
	_, err := NewService(Opts{})
	assert.NotNil(t, err)
}

func TestProvider(t *testing.T) {
	options := Opts{
		SecretReader: token.SecretFunc(func(id string) (string, error) { return "secret", nil }),
		URL:          "http://127.0.0.1:8080",
	}
	svc, err := NewService(options)
	assert.NoError(t, err)

	_, err = svc.Provider("some provider")
	assert.EqualError(t, err, "provider some provider not found")

	svc.AddProvider("dev", "cid", "csecret")
	svc.AddProvider("github", "cid", "csecret")

	p, err := svc.Provider("dev")
	assert.NoError(t, err)
	assert.Equal(t, "dev", p.Name)
	assert.Equal(t, "cid", p.Cid)
	assert.Equal(t, "csecret", p.Csecret)
	assert.Equal(t, "go-pkgz/auth", p.Issuer)

	p, err = svc.Provider("github")
	assert.NoError(t, err)
	assert.Equal(t, "github", p.Name)
}

func TestIntegration(t *testing.T) {
	options := Opts{
		SecretReader:   token.SecretFunc(func(id string) (string, error) { return "secret", nil }),
		TokenDuration:  time.Hour,
		CookieDuration: time.Hour * 24,
		Issuer:         "my-test-app",
		URL:            "http://127.0.0.1:8080",
		DisableXSRF:    true,
		Validator: middleware.ValidatorFunc(func(_ string, claims token.Claims) bool {
			return claims.User != nil && strings.HasPrefix(claims.User.Name, "dev_") // allow only dev_ names
		}),
		AvatarStore: avatar.NewLocalFS("/tmp/auth-pkgz", 120),
	}

	svc, err := NewService(options)
	require.NoError(t, err)
	svc.AddProvider("dev", "", "") // add dev provider

	// run dev/test oauth2 server on :8084
	go func() {
		p, err := svc.Provider("dev")
		if err != nil {
			t.Fatal(err)
		}
		devAuthServer := provider.DevAuthServer{Provider: p, Automatic: true}
		devAuthServer.Run()
	}()

	m := svc.Middleware()
	// setup http server
	mux := http.NewServeMux()
	mux.Handle("/open", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { // no token required
		_, _ = w.Write([]byte("open route, no token needed\n"))
	}))
	mux.Handle("/private", m.Auth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { // token required
		_, _ = w.Write([]byte("open route, no token needed\n"))
	})))

	// setup auth routes
	authRoute, avaRoutes := svc.Handlers()
	mux.Handle("/auth/", authRoute)                                // add token handlers
	mux.Handle("/avatar/", http.StripPrefix("/avatar", avaRoutes)) // add avatar handler

	l, err := net.Listen("tcp", "127.0.0.1:8080")
	require.Nil(t, err)
	ts := httptest.NewUnstartedServer(mux)
	ts.Listener.Close()
	ts.Listener = l
	ts.Start()
	defer ts.Close()

	jar, err := cookiejar.New(nil)
	require.Nil(t, err)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}

	// check non-admin, permanent
	resp, err := client.Get("http://127.0.0.1:8080/auth/dev/login?site=my-test-site")
	require.Nil(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	body, err := ioutil.ReadAll(resp.Body)
	assert.Nil(t, err)
	t.Logf("resp %s", string(body))
	t.Logf("headers: %+v", resp.Header)
}
