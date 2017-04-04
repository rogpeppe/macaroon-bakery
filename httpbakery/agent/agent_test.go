package agent_test

import (
	"encoding/base64"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"time"

	"github.com/juju/httprequest"
	"github.com/juju/testing"
	"golang.org/x/net/context"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
	"gopkg.in/macaroon-bakery.v2-unstable/bakery/checkers"
	"gopkg.in/macaroon-bakery.v2-unstable/bakerytest"
	"gopkg.in/macaroon-bakery.v2-unstable/httpbakery"
	"gopkg.in/macaroon-bakery.v2-unstable/httpbakery/agent"
)

type agentSuite struct {
	testing.LoggingSuite
	agentBakery *bakery.Bakery
	bakery      *bakery.Bakery
	discharger  *bakerytest.Discharger
}

func (s *agentSuite) SetUpTest(c *gc.C) {
	s.LoggingSuite.SetUpTest(c)
	s.discharger = bakerytest.NewDischarger(nil)
}

type legacyAgentSuite struct {
	testing.LoggingSuite
	agentBakery *bakery.Bakery
	bakery      *bakery.Bakery
	visitWaiter *bakerytest.VisitWaitHandler
	discharger  *bakerytest.Discharger
}

type visitFunc func(w http.ResponseWriter, req *http.Request, dischargeId string) error

var _ = gc.Suite(&legacyAgentSuite{})

func (s *legacyAgentSuite) SetUpTest(c *gc.C) {
	s.LoggingSuite.SetUpTest(c)
	s.discharger = bakerytest.NewDischarger(nil)
	s.visitWaiter = bakerytest.NewVisitWaitHandler(s.discharger, nil)
	s.visitWaiter.Visit = handleLoginMethods(s.visit)
	s.discharger.AddInteractor(s.visitWaiter)
	s.discharger.Checker = httpbakery.ThirdPartyCaveatCheckerFunc(func(ctx context.Context, req *http.Request, info *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
		err := s.discharger.NewInteractionRequiredError(info, req)
		// Remove non-legacy interaction methods so that
		// the client will follow the legacy protocol.
		err.Info.InteractionMethods = nil
		return nil, err
	})

	key, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)
	s.agentBakery = bakery.New(bakery.BakeryParams{
		IdentityClient: idmClient{s.discharger.Location()},
		Key:            key,
	})

	key, err = bakery.GenerateKey()
	c.Assert(err, gc.IsNil)
	s.bakery = bakery.New(bakery.BakeryParams{
		Locator:        s.discharger,
		IdentityClient: idmClient{s.discharger.Location()},
		Key:            key,
	})
}

func (s *legacyAgentSuite) TearDownTest(c *gc.C) {
	s.discharger.Close()
	s.LoggingSuite.TearDownTest(c)
}

var legacyAgentLoginErrorTests = []struct {
	about string

	visitHandler visitFunc
	expectError  string
}{{
	about: "error response",
	visitHandler: func(w http.ResponseWriter, req *http.Request, dischargeId string) error {
		return errgo.Newf("test error")
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: Get http(s)?://.*: test error`,
}, {
	about: "unexpected response",
	visitHandler: func(w http.ResponseWriter, req *http.Request, dischargeId string) error {
		w.Write([]byte("OK"))
		return nil
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: Get http(s)?://.*: unexpected content type text/plain; want application/json; content: OK`,
}, {
	about: "unexpected error response",
	visitHandler: func(w http.ResponseWriter, req *http.Request, dischargeId string) error {
		httprequest.WriteJSON(w, http.StatusBadRequest, httpbakery.Error{})
		return nil
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: Get http(s)?://.*: no error message found`,
}, {
	about: "login false value",
	visitHandler: func(w http.ResponseWriter, req *http.Request, dischargeId string) error {
		httprequest.WriteJSON(w, http.StatusOK, agent.LegacyAgentResponse{})
		return nil
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: agent login failed`,
}}

func (s *legacyAgentSuite) TestAgentLogin(c *gc.C) {
	for i, test := range legacyAgentLoginErrorTests {
		c.Logf("%d. %s", i, test.about)
		s.visitWaiter.Visit = func(w http.ResponseWriter, req *http.Request, dischargeId string) error {
			if req.Header.Get("Accept") == "application/json" {
				httprequest.WriteJSON(w, http.StatusOK, map[string]string{
					"agent": req.URL.String(),
				})
				return nil
			}
			return test.visitHandler(w, req, dischargeId)
		}
		key, err := bakery.GenerateKey()
		c.Assert(err, gc.IsNil)

		client := httpbakery.NewClient()
		err = agent.SetUpAuth(client, &agent.AuthInfo{
			Key: key,
			Agents: []agent.Agent{{
				URL:      s.discharger.Location(),
				Username: "test-user",
			}},
		})
		c.Assert(err, gc.IsNil)
		m, err := s.bakery.Oven.NewMacaroon(
			context.Background(),
			bakery.LatestVersion,
			time.Now().Add(time.Minute),
			identityCaveats(s.discharger.Location()),
			bakery.LoginOp,
		)
		c.Assert(err, gc.IsNil)
		ms, err := client.DischargeAll(context.Background(), m)
		if test.expectError != "" {
			c.Assert(err, gc.ErrorMatches, test.expectError)
			continue
		}
		c.Assert(err, gc.IsNil)
		authInfo, err := s.bakery.Checker.Auth(ms).Allow(context.Background(), bakery.LoginOp)
		c.Assert(err, gc.IsNil)
		c.Assert(authInfo.Identity, gc.Equals, bakery.SimpleIdentity("test-user"))
	}
}

func (s *legacyAgentSuite) TestSetUpAuth(c *gc.C) {
	client := httpbakery.NewClient()
	key, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)
	err = agent.SetUpAuth(client, &agent.AuthInfo{
		Key: key,
		Agents: []agent.Agent{{
			URL:      s.discharger.Location(),
			Username: "test-user",
		}},
	})
	c.Assert(err, gc.IsNil)
	m, err := s.bakery.Oven.NewMacaroon(
		context.Background(),
		bakery.LatestVersion,
		time.Now().Add(time.Minute),
		identityCaveats(s.discharger.Location()),
		bakery.LoginOp,
	)
	c.Assert(err, gc.IsNil)
	ms, err := client.DischargeAll(context.Background(), m)
	c.Assert(err, gc.IsNil)
	authInfo, err := s.bakery.Checker.Auth(ms).Allow(context.Background(), bakery.LoginOp)
	c.Assert(err, gc.IsNil)
	c.Assert(authInfo.Identity, gc.Equals, bakery.SimpleIdentity("test-user"))
}

func (s *legacyAgentSuite) TestNoCookieError(c *gc.C) {
	client := httpbakery.NewClient()
	key, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)
	err = agent.SetUpAuth(client, &agent.AuthInfo{
		Key: key,
		Agents: []agent.Agent{{
			URL:      "http://0.1.2.3/",
			Username: "test-user",
		}},
	})
	c.Assert(err, gc.IsNil)
	m, err := s.bakery.Oven.NewMacaroon(
		context.Background(),
		bakery.LatestVersion,
		time.Now().Add(time.Minute),
		identityCaveats(s.discharger.Location()),
		bakery.LoginOp,
	)

	c.Assert(err, gc.IsNil)
	_, err = client.DischargeAll(context.Background(), m)
	c.Assert(err, gc.ErrorMatches, "cannot get discharge from .*: cannot read agent login: no agent-login cookie found")
	_, ok := errgo.Cause(err).(*httpbakery.InteractionError)
	c.Assert(ok, gc.Equals, true)
}

func (s *legacyAgentSuite) TestLoginCookie(c *gc.C) {
	key, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)
	dischargerURL, err := url.Parse(s.discharger.Location())
	c.Assert(err, gc.IsNil)
	tests := []struct {
		about       string
		setCookie   func(http.CookieJar)
		expectUser  string
		expectKey   *bakery.PublicKey
		expectError string
		expectCause error
	}{{
		about: "success",
		setCookie: func(jar http.CookieJar) {
			agent.SetCookie(jar, dischargerURL, "bob", &key.Public)
		},
		expectUser: "bob",
		expectKey:  &key.Public,
	}, {
		about:       "no cookie",
		setCookie:   func(jar http.CookieJar) {},
		expectError: "no agent-login cookie found",
		expectCause: agent.ErrNoAgentLoginCookie,
	}, {
		about: "invalid base64 encoding",
		setCookie: func(jar http.CookieJar) {
			jar.SetCookies(dischargerURL, []*http.Cookie{{
				Name:  "agent-login",
				Value: "x",
			}})
		},
		expectError: "cannot decode cookie value: illegal base64 data at input byte 0",
	}, {
		about: "invalid JSON",
		setCookie: func(jar http.CookieJar) {
			jar.SetCookies(dischargerURL, []*http.Cookie{{
				Name:  "agent-login",
				Value: base64.StdEncoding.EncodeToString([]byte("}")),
			}})
		},
		expectError: "cannot unmarshal agent login: invalid character '}' looking for beginning of value",
	}, {
		about: "no username",
		setCookie: func(jar http.CookieJar) {
			agent.SetCookie(jar, dischargerURL, "", &key.Public)
		},
		expectError: "agent login has no user name",
	}, {
		about: "no public key",
		setCookie: func(jar http.CookieJar) {
			agent.SetCookie(jar, dischargerURL, "bob", nil)
		},
		expectError: "agent login has no public key",
	}}

	for i, test := range tests {
		c.Logf("test %d: %s", i, test.about)

		jar, _ := cookiejar.New(nil)
		test.setCookie(jar)
		req, err := http.NewRequest("GET", "", nil)
		c.Assert(err, gc.IsNil)
		for _, cookie := range jar.Cookies(dischargerURL) {
			req.AddCookie(cookie)
		}
		username, key, err := agent.LoginCookie(req)

		if test.expectError != "" {
			c.Assert(err, gc.ErrorMatches, test.expectError)
			if test.expectCause != nil {
				c.Assert(errgo.Cause(err), gc.Equals, test.expectCause)
			}
			continue
		}
		c.Assert(username, gc.Equals, test.expectUser)
		c.Assert(key, gc.DeepEquals, test.expectKey)
	}
}

func ExampleVisitWebPage() {
	// In practice the key would be read from persistent
	// storage.
	key, err := bakery.GenerateKey()
	if err != nil {
		// handle error
	}

	client := httpbakery.NewClient()
	err = agent.SetUpAuth(client, &agent.AuthInfo{
		Key: key,
		Agents: []agent.Agent{{
			URL:      "http://foo.com",
			Username: "agent-username",
		}},
	})
	if err != nil {
		// handle error
	}
}

type idmClient struct {
	dischargerURL string
}

func (c idmClient) IdentityFromContext(ctx context.Context) (bakery.Identity, []checkers.Caveat, error) {
	return nil, identityCaveats(c.dischargerURL), nil
}

func identityCaveats(dischargerURL string) []checkers.Caveat {
	return []checkers.Caveat{{
		Location:  dischargerURL,
		Condition: "test condition",
	}}
}

func (c idmClient) DeclaredIdentity(ctx context.Context, declared map[string]string) (bakery.Identity, error) {
	return bakery.SimpleIdentity(declared["username"]), nil
}

func mustParseURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

var testKey = func() *bakery.KeyPair {
	key, err := bakery.GenerateKey()
	if err != nil {
		panic(err)
	}
	return key
}()

var ages = time.Now().Add(time.Hour)

func handleLoginMethods(f visitFunc) visitFunc {
	return func(w http.ResponseWriter, req *http.Request, dischargeId string) error {
		if req.Header.Get("Accept") == "application/json" {
			httprequest.WriteJSON(w, http.StatusOK, map[string]string{
				"agent": req.URL.String(),
			})
			return nil
		}
		return f(w, req, dischargeId)
	}
}

func (s *legacyAgentSuite) visit(w http.ResponseWriter, req *http.Request, dischargeId string) error {
	ctx := context.TODO()
	username, userPublicKey, err := agent.LoginCookie(req)
	if err != nil {
		return errgo.Notef(err, "cannot read agent login")
	}
	_, authErr := s.agentBakery.Checker.Auth(httpbakery.RequestMacaroons(req)...).Allow(ctx, bakery.LoginOp)
	if authErr == nil {
		s.visitWaiter.FinishInteraction(dischargeId, []checkers.Caveat{
			checkers.DeclaredCaveat("username", username),
		}, nil)
		httprequest.WriteJSON(w, http.StatusOK, agent.LegacyAgentResponse{true})
		return nil
	}
	version := httpbakery.RequestVersion(req)
	m, err := s.agentBakery.Oven.NewMacaroon(ctx, version, ages, []checkers.Caveat{
		bakery.LocalThirdPartyCaveat(userPublicKey, version),
		checkers.DeclaredCaveat("username", username),
	}, bakery.LoginOp)
	if err != nil {
		return errgo.Notef(err, "cannot create macaroon")
	}
	return httpbakery.NewDischargeRequiredErrorForRequest(m, "/", nil, req)
}


TODO use agentInteractor

type agentInteractor struct {
	discharger *bakerytest.Discharger
}

func (i agentInteractor) SetInteraction(ierr *httpbakery.Error, req *http.Request, dischargeId string) {
	username, userPublicKey, err := agent.LoginCookie(req)
	if err != nil && errgo.Cause(err) != agent.ErrNoAgentLoginCookie {
		panic(err)
	}
	version := httpbakery.RequestVersion(req)
	checker := bakery.ThirdPartyCaveatCheckerFunc(func(ctx context.Context, cav *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
		return []checkers.Caveat{
			bakery.LocalThirdPartyCaveat(userPublicKey, version),
			checkers.DeclaredCaveat("username", username),
		}, nil
	})
	m, err := i.discharger.CompleteDischarge(context.TODO(), dischargeId, checker)
	if err != nil {
		panic(err)
	}
	ierr.SetInteraction("agent", agent.InteractionParams{
		Macaroon: m,
	})
}

func (agentInteractor) Handlers() []httprequest.Handler {
	return nil
}
