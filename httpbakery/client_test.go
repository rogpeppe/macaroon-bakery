package httpbakery_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/juju/httprequest"
	jujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	"golang.org/x/net/context"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"
	"gopkg.in/macaroon.v2-unstable"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
	"gopkg.in/macaroon-bakery.v2-unstable/bakery/checkers"
	"gopkg.in/macaroon-bakery.v2-unstable/bakerytest"
	"gopkg.in/macaroon-bakery.v2-unstable/httpbakery"
)

type ClientSuite struct {
	jujutesting.LoggingSuite
}

var _ = gc.Suite(&ClientSuite{})

var macaroonCurrentVersion = bakery.MacaroonVersion(bakery.LatestVersion)

// TestSingleServiceFirstParty creates a single service
// with a macaroon with one first party caveat.
// It creates a request with this macaroon and checks that the service
// can verify this macaroon as valid.
func (s *ClientSuite) TestSingleServiceFirstParty(c *gc.C) {
	// Create a target service.
	svc := newService("loc", nil, nil)
	// No discharge required, so pass "unknown" for the third party
	// caveat discharger location so we know that we don't try
	// to discharge the location.
	ts := httptest.NewServer(serverHandler(serverHandlerParams{
		service:      svc,
		authLocation: "unknown",
	}))
	defer ts.Close()

	// Mint a macaroon for the target service.
	serverMacaroon, err := svc.NewMacaroon(bakery.LatestVersion, nil)
	c.Assert(err, gc.IsNil)
	c.Assert(serverMacaroon.Location(), gc.Equals, "loc")
	err = svc.AddCaveat(serverMacaroon, checkers.Caveat{
		Condition: "is something",
	})
	c.Assert(err, gc.IsNil)

	// Create a client request.
	req, err := http.NewRequest("GET", ts.URL, nil)
	c.Assert(err, gc.IsNil)
	client := clientRequestWithCookies(c, ts.URL, macaroon.Slice{serverMacaroon})
	// Somehow the client has accquired the macaroon. Add it to the cookiejar in our request.

	// Make the request to the server.
	resp, err := client.Do(req)
	c.Assert(err, gc.IsNil)
	defer resp.Body.Close()
	assertResponse(c, resp, "done")
}

func (s *ClientSuite) TestSingleServiceFirstPartyWithHeader(c *gc.C) {
	// Create a target service.
	svc := newService("loc", nil, nil)
	// No discharge required, so pass "unknown" for the third party
	// caveat discharger location so we know that we don't try
	// to discharge the location.
	ts := httptest.NewServer(serverHandler(serverHandlerParams{
		service:      svc,
		authLocation: "unknown",
	}))
	defer ts.Close()

	// Mint a macaroon for the target service.
	serverMacaroon, err := svc.NewMacaroon(bakery.LatestVersion, nil)
	c.Assert(err, gc.IsNil)
	c.Assert(serverMacaroon.Location(), gc.Equals, "loc")
	err = svc.AddCaveat(serverMacaroon, checkers.Caveat{
		Condition: "is something",
	})
	c.Assert(err, gc.IsNil)

	// Serialize the macaroon slice.
	data, err := json.Marshal(macaroon.Slice{serverMacaroon})
	c.Assert(err, gc.IsNil)
	value := base64.StdEncoding.EncodeToString(data)

	// Create a client request.
	req, err := http.NewRequest("GET", ts.URL, nil)
	c.Assert(err, gc.IsNil)
	req.Header.Set(httpbakery.MacaroonsHeader, value)
	client := httpbakery.NewHTTPClient()

	// Make the request to the server.
	resp, err := client.Do(req)
	c.Assert(err, gc.IsNil)
	defer resp.Body.Close()
	assertResponse(c, resp, "done")
}

func (s *ClientSuite) TestRepeatedRequestWithBody(c *gc.C) {
	d := bakerytest.NewDischarger(nil, nil)
	defer d.Close()

	// Create a target service.
	svc := newService("loc", d, nil)

	ts := httptest.NewServer(serverHandler(serverHandlerParams{
		service:        svc,
		authLocation:   d.Location(),
		alwaysReadBody: true,
	}))
	defer ts.Close()

	// Create a client request.
	req, err := http.NewRequest("POST", ts.URL, nil)
	c.Assert(err, gc.IsNil)

	// Make the request to the server.

	// First try with a body in the request, which should be denied
	// because we must use DoWithBody.
	req.Body = ioutil.NopCloser(strings.NewReader("postbody"))
	resp, err := httpbakery.NewClient().Do(req)
	c.Assert(err, gc.ErrorMatches, "body unexpectedly provided in request - use DoWithBody")
	c.Assert(resp, gc.IsNil)

	// Then try with no authorization, so make sure that httpbakery.Do
	// really will retry the request.

	req.Body = nil

	bodyText := "postbody"
	bodyReader := &readCounter{ReadSeeker: strings.NewReader(bodyText)}

	resp, err = httpbakery.NewClient().DoWithBody(req, bodyReader)
	c.Assert(err, gc.IsNil)
	defer resp.Body.Close()
	assertResponse(c, resp, "done postbody")

	// Sanity check that the body really was read twice and hence
	// that we are checking the logic we intend to check.
	c.Assert(bodyReader.byteCount, gc.Equals, len(bodyText)*2)
}

func (s ClientSuite) TestWithLargeBody(c *gc.C) {
	// This test is designed to fail when run with the race
	// checker enabled and when go issue #12796
	// is not fixed.

	d := bakerytest.NewDischarger(nil, nil)
	defer d.Close()

	// Create a target service.
	svc := newService("loc", d, nil)

	ts := httptest.NewServer(serverHandler(serverHandlerParams{
		service:      svc,
		authLocation: d.Location(),
	}))
	defer ts.Close()

	// Create a client request.
	req, err := http.NewRequest("POST", ts.URL+"/no-body", nil)
	c.Assert(err, gc.IsNil)

	body := &largeReader{total: 3 * 1024 * 1024}
	resp, err := httpbakery.NewClient().DoWithBody(req, body)
	c.Assert(err, gc.IsNil)
	resp.Body.Close()
	body.Close()

	c.Assert(resp.StatusCode, gc.Equals, http.StatusOK)
}

// largeReader implements a reader that produces up to total bytes
// in 1 byte reads.
type largeReader struct {
	total int
	n     int
}

func (r *largeReader) Read(buf []byte) (int, error) {
	if r.n >= r.total {
		return 0, io.EOF
	}
	r.n++
	return copy(buf, []byte("a")), nil
}

func (r *largeReader) Seek(offset int64, whence int) (int64, error) {
	if offset != 0 || whence != 0 {
		panic("unexpected seek")
	}
	r.n = 0
	return 0, nil
}

func (r *largeReader) Close() error {
	// By setting n to zero, we ensure that if there's
	// a concurrent read, it will also read from n
	// and so the race detector should pick up the
	// problem.
	r.n = 0
	return nil
}

func (s *ClientSuite) TestDoWithBodyFailsWithBodyInRequest(c *gc.C) {
	body := strings.NewReader("foo")
	// Create a client request.
	req, err := http.NewRequest("POST", "http://0.1.2.3/", body)
	c.Assert(err, gc.IsNil)
	_, err = httpbakery.NewClient().DoWithBody(req, body)
	c.Assert(err, gc.ErrorMatches, "body unexpectedly supplied in Request struct")
}

func (s *ClientSuite) TestDischargeServerWithBinaryCaveatId(c *gc.C) {
	assertDischargeServerDischargesConditionForVersion(c, "\xff\x00\x89", macaroon.V2)
}

func (s *ClientSuite) TestDischargeServerWithStringCaveatId(c *gc.C) {
	assertDischargeServerDischargesConditionForVersion(c, "foo", macaroon.V1)
}

func assertDischargeServerDischargesConditionForVersion(c *gc.C, cond string, version macaroon.Version) {
	called := 0
	checker := func(req *http.Request, cav *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
		called++
		c.Check(cav.Condition, gc.Equals, cond)
		return nil, nil
	}
	discharger := bakerytest.NewDischarger(nil, httpbakery.ThirdPartyCheckerFunc(checker))

	svcKey, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)

	m, err := macaroon.New([]byte("root key"), []byte("id"), "location", version)
	c.Assert(err, gc.IsNil)
	err = bakery.AddCaveat(svcKey, discharger, m, checkers.Caveat{
		Location:  discharger.Location(),
		Condition: cond,
	}, nil)
	c.Assert(err, gc.IsNil)
	client := httpbakery.NewClient()
	ms, err := client.DischargeAll(m)
	c.Assert(err, gc.IsNil)
	c.Check(ms, gc.HasLen, 2)
	c.Check(called, gc.Equals, 1)
}

func (s *ClientSuite) TestDischargeServerWithMacaraqOnDischarge(c *gc.C) {
	locator := bakery.NewThirdPartyLocatorStore()

	var called [3]int

	// create the services from leaf discharger to primary
	// service so that each one can know the location
	// to discharge at.
	dsvc1 := newService("loc", locator, nil)
	key2, h2 := newHTTPDischarger(dsvc1, httpbakery.ThirdPartyCheckerFunc(func(req *http.Request, cav *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
		called[2]++
		if cav.Condition != "is-ok" {
			return nil, fmt.Errorf("unrecognized caveat at srv2")
		}
		return nil, nil
	}))
	srv2 := httptest.NewServer(h2)
	defer srv2.Close()
	locator.AddInfo(srv2.URL, bakery.ThirdPartyInfo{
		PublicKey: *key2,
		Version:   bakery.LatestVersion,
	})

	dsvc2 := newService("loc", locator, nil)
	key1, h1 := newHTTPDischarger(dsvc2, httpbakery.ThirdPartyCheckerFunc(func(req *http.Request, cav *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
		called[1]++
		if _, _, err := httpbakery.CheckRequest(context.Background(), dsvc2, req, nil); err != nil {
			c.Logf("returning discharge required error")
			return nil, newDischargeRequiredError(serverHandlerParams{
				service:      dsvc2,
				authLocation: srv2.URL,
			}, err, req)
		}
		if cav.Condition != "is-ok" {
			return nil, fmt.Errorf("unrecognized caveat at srv1")
		}
		return nil, nil
	}))
	srv1 := httptest.NewServer(h1)
	defer srv1.Close()
	locator.AddInfo(srv1.URL, bakery.ThirdPartyInfo{
		PublicKey: *key1,
		Version:   bakery.LatestVersion,
	})

	svc0 := newService("loc", locator, nil)
	srv0 := httptest.NewServer(serverHandler(serverHandlerParams{
		service:      svc0,
		authLocation: srv1.URL,
	}))
	defer srv0.Close()

	// Make a client request.
	client := httpbakery.NewClient()
	req, err := http.NewRequest("GET", srv0.URL, nil)
	c.Assert(err, gc.IsNil)
	resp, err := client.Do(req)
	c.Assert(err, gc.IsNil)
	defer resp.Body.Close()
	assertResponse(c, resp, "done")

	c.Assert(called, gc.DeepEquals, [3]int{0, 2, 1})
}

func (s *ClientSuite) TestVersion0Generates407Status(c *gc.C) {
	m, err := macaroon.New([]byte("root key"), []byte("id"), "location", macaroon.V1)
	c.Assert(err, gc.IsNil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		httpbakery.WriteDischargeRequiredErrorForRequest(w, m, "", errgo.New("foo"), req)
	}))
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	c.Assert(err, gc.IsNil)
	c.Assert(resp.StatusCode, gc.Equals, http.StatusProxyAuthRequired)
}

func (s *ClientSuite) TestVersion1Generates401Status(c *gc.C) {
	m, err := macaroon.New([]byte("root key"), []byte("id"), "location", macaroon.V1)
	c.Assert(err, gc.IsNil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		httpbakery.WriteDischargeRequiredErrorForRequest(w, m, "", errgo.New("foo"), req)
	}))
	defer srv.Close()

	req, err := http.NewRequest("GET", srv.URL, nil)
	c.Assert(err, gc.IsNil)
	req.Header.Set(httpbakery.BakeryProtocolHeader, "1")
	resp, err := http.DefaultClient.Do(req)
	c.Assert(err, gc.IsNil)
	c.Assert(resp.StatusCode, gc.Equals, http.StatusUnauthorized)
	c.Assert(resp.Header.Get("WWW-Authenticate"), gc.Equals, "Macaroon")
}

func newHTTPDischarger(svc *bakery.Service, checker httpbakery.ThirdPartyChecker) (*bakery.PublicKey, http.Handler) {
	mux := http.NewServeMux()
	d := httpbakery.NewDischargerFromService(svc, checker)
	d.AddMuxHandlers(mux, "/")
	return svc.PublicKey(), mux
}

func (s *ClientSuite) TestDischargeAcquirer(c *gc.C) {
	rootKey := []byte("secret")
	m, err := macaroon.New(rootKey, nil, "here", macaroonCurrentVersion)
	c.Assert(err, gc.IsNil)

	dischargeRootKey := []byte("shared root key")
	thirdPartyCaveatId := []byte("3rd party caveat")
	err = m.AddThirdPartyCaveat(dischargeRootKey, thirdPartyCaveatId, "there")
	c.Assert(err, gc.IsNil)

	dm, err := macaroon.New(dischargeRootKey, thirdPartyCaveatId, "there", macaroonCurrentVersion)
	c.Assert(err, gc.IsNil)

	ta := &testAcquirer{dischargeMacaroon: dm}
	cl := httpbakery.NewClient()
	cl.DischargeAcquirer = ta

	ms, err := cl.DischargeAll(m)
	c.Assert(err, gc.IsNil)
	c.Assert(ms, gc.HasLen, 2)

	c.Assert(ta.acquireCaveat.Id, gc.DeepEquals, thirdPartyCaveatId)
	expectCaveat := "must foo"
	var lastCaveat string
	err = ms[0].Verify(rootKey, func(s string) error {
		if s != expectCaveat {
			return errgo.Newf(`expected %q, got %q`, expectCaveat, s)
		}
		lastCaveat = s
		return nil
	}, ms[1:])
	c.Assert(err, gc.IsNil)
	c.Assert(lastCaveat, gc.Equals, expectCaveat)
}

type testAcquirer struct {
	dischargeMacaroon *macaroon.Macaroon

	acquireCaveat macaroon.Caveat
}

// AcquireDischarge implements httpbakery.DischargeAcquirer.
func (ta *testAcquirer) AcquireDischarge(cav macaroon.Caveat) (*macaroon.Macaroon, error) {
	ta.acquireCaveat = cav
	err := ta.dischargeMacaroon.AddFirstPartyCaveat("must foo")
	if err != nil {
		return nil, err
	}
	return ta.dischargeMacaroon, nil
}

func (s *ClientSuite) TestMacaroonCookieName(c *gc.C) {
	d := bakerytest.NewDischarger(nil, nil)
	defer d.Close()

	checked := make(map[string]bool)
	checker := checkers.New(nil)
	checker.Namespace().Register("testns", "")
	checker.Register("once", "testns", func(ctxt context.Context, _, arg string) error {
		if checked[arg] {
			return errgo.Newf("caveat %q has already been checked once", arg)
		}
		checked[arg] = true
		return nil
	})

	svc := newService("loc", nil, checker)

	// We arrange things so that although we use the same client
	// (with the same cookie jar), the macaroon verification only
	// succeeds once, so the client always fetches a new macaroon.

	caveatSeq := 0
	cookieName := ""
	ts := httptest.NewServer(serverHandler(serverHandlerParams{
		service: svc,
		mutateError: func(e *httpbakery.Error) {
			e.Info.CookieNameSuffix = cookieName
			e.Info.MacaroonPath = "/"
		},
		caveats: func() []checkers.Caveat {
			caveatSeq++
			return []checkers.Caveat{{
				Condition: fmt.Sprintf("once %d", caveatSeq),
			}}
		},
	}))
	defer ts.Close()

	client := httpbakery.NewClient()
	doRequest := func() {
		req, err := http.NewRequest("GET", ts.URL+"/foo/bar/", nil)
		c.Assert(err, gc.IsNil)
		resp, err := client.Do(req)
		c.Assert(err, gc.IsNil)
		defer resp.Body.Close()
		assertResponse(c, resp, "done")
	}
	assertCookieNames := func(names ...string) {
		u, err := url.Parse(ts.URL)
		c.Assert(err, gc.IsNil)
		sort.Strings(names)
		var gotNames []string
		for _, c := range client.Jar.Cookies(u) {
			gotNames = append(gotNames, c.Name)
		}
		sort.Strings(gotNames)
		c.Assert(gotNames, jc.DeepEquals, names)
	}
	cookieName = "foo"
	doRequest()
	assertCookieNames("macaroon-foo")

	// Another request with the same cookie name should
	// overwrite the old cookie.
	doRequest()
	assertCookieNames("macaroon-foo")

	// A subsequent request with a different cookie name
	// should create a new cookie, but the old one will still
	// be around.
	cookieName = "bar"
	doRequest()
	assertCookieNames("macaroon-foo", "macaroon-bar")
}

func (s *ClientSuite) TestMacaroonCookiePath(c *gc.C) {
	svc := newService("loc", nil, nil)

	cookiePath := ""
	ts := httptest.NewServer(serverHandler(serverHandlerParams{
		service: svc,
		mutateError: func(e *httpbakery.Error) {
			e.Info.MacaroonPath = cookiePath
		},
	}))
	defer ts.Close()

	var client *httpbakery.Client
	doRequest := func() {
		req, err := http.NewRequest("GET", ts.URL+"/foo/bar/", nil)
		c.Assert(err, gc.IsNil)
		client = httpbakery.NewClient()
		resp, err := client.Do(req)
		c.Assert(err, gc.IsNil)
		defer resp.Body.Close()
		assertResponse(c, resp, "done")
	}
	assertCookieCount := func(path string, n int) {
		u, err := url.Parse(ts.URL + path)
		c.Assert(err, gc.IsNil)
		c.Assert(client.Jar.Cookies(u), gc.HasLen, n)
	}
	cookiePath = ""
	c.Logf("- cookie path %q", cookiePath)
	doRequest()
	assertCookieCount("", 0)
	assertCookieCount("/foo", 0)
	assertCookieCount("/foo", 0)
	assertCookieCount("/foo/", 0)
	assertCookieCount("/foo/bar/", 1)
	assertCookieCount("/foo/bar/baz", 1)

	cookiePath = "/foo/"
	c.Logf("- cookie path %q", cookiePath)
	doRequest()
	assertCookieCount("", 0)
	assertCookieCount("/foo", 0)
	assertCookieCount("/foo/", 1)
	assertCookieCount("/foo/bar/", 1)
	assertCookieCount("/foo/bar/baz", 1)

	cookiePath = "/foo"
	c.Logf("- cookie path %q", cookiePath)
	doRequest()
	assertCookieCount("", 0)
	assertCookieCount("/bar", 0)
	assertCookieCount("/foo", 1)
	assertCookieCount("/foo/", 1)
	assertCookieCount("/foo/bar/", 1)
	assertCookieCount("/foo/bar/baz", 1)

	cookiePath = "../"
	c.Logf("- cookie path %q", cookiePath)
	doRequest()
	assertCookieCount("", 0)
	assertCookieCount("/bar", 0)
	assertCookieCount("/foo", 0)
	assertCookieCount("/foo/", 1)
	assertCookieCount("/foo/bar/", 1)
	assertCookieCount("/foo/bar/baz", 1)

	cookiePath = "../bar"
	c.Logf("- cookie path %q", cookiePath)
	doRequest()
	assertCookieCount("", 0)
	assertCookieCount("/bar", 0)
	assertCookieCount("/foo", 0)
	assertCookieCount("/foo/", 0)
	assertCookieCount("/foo/bar/", 1)
	assertCookieCount("/foo/bar/baz", 1)
	assertCookieCount("/foo/baz", 0)
	assertCookieCount("/foo/baz/", 0)
	assertCookieCount("/foo/baz/bar", 0)

	cookiePath = "/"
	c.Logf("- cookie path %q", cookiePath)
	doRequest()
	assertCookieCount("", 1)
	assertCookieCount("/bar", 1)
	assertCookieCount("/foo", 1)
	assertCookieCount("/foo/", 1)
	assertCookieCount("/foo/bar/", 1)
	assertCookieCount("/foo/bar/baz", 1)
}

func (s *ClientSuite) TestThirdPartyDischargeRefused(c *gc.C) {
	d := bakerytest.NewDischarger(nil, bakerytest.ConditionParser(func(cond, arg string) ([]checkers.Caveat, error) {
		return nil, errgo.New("boo! cond " + cond)
	}))
	defer d.Close()

	// Create a target service.
	svc := newService("loc", d, nil)

	ts := httptest.NewServer(serverHandler(serverHandlerParams{
		service:      svc,
		authLocation: d.Location(),
	}))
	defer ts.Close()

	// Create a client request.
	req, err := http.NewRequest("GET", ts.URL, nil)
	c.Assert(err, gc.IsNil)

	client := httpbakery.NewClient()

	// Make the request to the server.
	resp, err := client.Do(req)
	c.Assert(errgo.Cause(err), gc.FitsTypeOf, (*httpbakery.DischargeError)(nil))
	c.Assert(err, gc.ErrorMatches, `cannot get discharge from ".*": third party refused discharge: cannot discharge: boo! cond is-ok`)
	c.Assert(resp, gc.IsNil)
}

func (s *ClientSuite) TestDischargeWithInteractionRequiredError(c *gc.C) {
	d := bakerytest.NewDischarger(nil, bakerytest.ConditionParser(func(cond, arg string) ([]checkers.Caveat, error) {
		return nil, &httpbakery.Error{
			Code:    httpbakery.ErrInteractionRequired,
			Message: "interaction required",
			Info: &httpbakery.ErrorInfo{
				VisitURL: "http://0.1.2.3/",
				WaitURL:  "http://0.1.2.3/",
			},
		}
	}))
	defer d.Close()

	// Create a target service.
	svc := newService("loc", d, nil)

	ts := httptest.NewServer(serverHandler(serverHandlerParams{
		service:      svc,
		authLocation: d.Location(),
	}))
	defer ts.Close()

	// Create a client request.
	req, err := http.NewRequest("GET", ts.URL, nil)
	c.Assert(err, gc.IsNil)

	errCannotVisit := errgo.New("cannot visit")
	client := httpbakery.NewClient()
	client.VisitWebPage = func(*url.URL) error {
		return errCannotVisit
	}

	// Make the request to the server.
	resp, err := client.Do(req)
	c.Assert(err, gc.ErrorMatches, `cannot get discharge from "https://.*": cannot start interactive session: cannot visit`)
	c.Assert(httpbakery.IsInteractionError(errgo.Cause(err)), gc.Equals, true)
	ierr, ok := errgo.Cause(err).(*httpbakery.InteractionError)
	c.Assert(ok, gc.Equals, true)
	c.Assert(ierr.Reason, gc.Equals, errCannotVisit)
	c.Assert(resp, gc.IsNil)
}

func (s *ClientSuite) TestDischargeWithInteractionRequiredErrorAndWebPageVisitor(c *gc.C) {
	d := bakerytest.NewDischarger(nil, bakerytest.ConditionParser(func(cond, arg string) ([]checkers.Caveat, error) {
		return nil, &httpbakery.Error{
			Code:    httpbakery.ErrInteractionRequired,
			Message: "interaction required",
			Info: &httpbakery.ErrorInfo{
				VisitURL: "http://0.1.2.3/",
				WaitURL:  "http://0.1.2.3/",
			},
		}
	}))
	defer d.Close()

	// Create a target service.
	svc := newService("loc", d, nil)

	ts := httptest.NewServer(serverHandler(serverHandlerParams{
		service:      svc,
		authLocation: d.Location(),
	}))
	defer ts.Close()

	// Create a client request.
	req, err := http.NewRequest("GET", ts.URL, nil)
	c.Assert(err, gc.IsNil)

	errCannotVisit := errgo.New("cannot visit")
	client := httpbakery.NewClient()
	client.WebPageVisitor = visitorFunc(func(_ *httpbakery.Client, m map[string]*url.URL) error {
		return errCannotVisit
	})

	// Make the request to the server.
	resp, err := client.Do(req)
	c.Assert(err, gc.ErrorMatches, `cannot get discharge from "https://.*": cannot start interactive session: cannot visit`)
	c.Assert(httpbakery.IsInteractionError(errgo.Cause(err)), gc.Equals, true)
	ierr, ok := errgo.Cause(err).(*httpbakery.InteractionError)
	c.Assert(ok, gc.Equals, true)
	c.Assert(ierr.Reason, gc.Equals, errCannotVisit)
	c.Assert(resp, gc.IsNil)
}

var dischargeWithVisitURLErrorTests = []struct {
	about       string
	respond     func(http.ResponseWriter)
	expectError string
}{{
	about: "error message",
	respond: func(w http.ResponseWriter) {
		httprequest.ErrorMapper(httpbakery.ErrorToResponse).WriteError(w, fmt.Errorf("an error"))
	},
	expectError: `cannot get discharge from ".*": failed to acquire macaroon after waiting: third party refused discharge: an error`,
}, {
	about: "non-JSON error",
	respond: func(w http.ResponseWriter) {
		w.Write([]byte("bad response"))
	},
	// TODO fix this unhelpful error message
	expectError: `cannot get discharge from ".*": cannot unmarshal wait response: invalid character 'b' looking for beginning of value`,
}}

func (s *ClientSuite) TestDischargeWithVisitURLError(c *gc.C) {
	visitor := newVisitHandler(nil)
	visitSrv := httptest.NewServer(visitor)
	defer visitSrv.Close()

	d := bakerytest.NewDischarger(nil, bakerytest.ConditionParser(func(cond, arg string) ([]checkers.Caveat, error) {
		return nil, &httpbakery.Error{
			Code:    httpbakery.ErrInteractionRequired,
			Message: "interaction required",
			Info: &httpbakery.ErrorInfo{
				VisitURL: visitSrv.URL + "/visit",
				WaitURL:  visitSrv.URL + "/wait",
			},
		}
	}))
	defer d.Close()

	// Create a target service.
	svc := newService("loc", d, nil)
	ts := httptest.NewServer(serverHandler(serverHandlerParams{
		service:      svc,
		authLocation: d.Location(),
	}))
	defer ts.Close()

	for i, test := range dischargeWithVisitURLErrorTests {
		c.Logf("test %d: %s", i, test.about)
		visitor.respond = test.respond

		client := httpbakery.NewClient()
		client.VisitWebPage = func(u *url.URL) error {
			resp, err := http.Get(u.String())
			if err != nil {
				return err
			}
			resp.Body.Close()
			return nil
		}

		// Create a client request.
		req, err := http.NewRequest("GET", ts.URL, nil)
		c.Assert(err, gc.IsNil)

		// Make the request to the server.
		_, err = client.Do(req)
		c.Assert(err, gc.ErrorMatches, test.expectError)
	}
}

func (s *ClientSuite) TestMacaroonsForURL(c *gc.C) {
	// Create a target service.
	svc := newService("loc", nil, nil)

	m1, err := svc.NewMacaroon(bakery.LatestVersion, nil)
	c.Assert(err, gc.IsNil)
	m2, err := svc.NewMacaroon(bakery.LatestVersion, nil)
	c.Assert(err, gc.IsNil)

	u1 := mustParseURL("http://0.1.2.3/")
	u2 := mustParseURL("http://0.1.2.3/x/")

	// Create some cookies with different cookie paths.
	jar, err := cookiejar.New(nil)
	c.Assert(err, gc.IsNil)
	httpbakery.SetCookie(jar, u1, nil, macaroon.Slice{m1})
	httpbakery.SetCookie(jar, u2, nil, macaroon.Slice{m2})
	jar.SetCookies(u1, []*http.Cookie{{
		Name:  "foo",
		Path:  "/",
		Value: "ignored",
	}, {
		Name:  "bar",
		Path:  "/x/",
		Value: "ignored",
	}})

	// Check that MacaroonsForURL behaves correctly
	// with both single and multiple cookies.

	mss := httpbakery.MacaroonsForURL(jar, u1)
	c.Assert(mss, gc.HasLen, 1)
	c.Assert(mss[0], gc.HasLen, 1)
	c.Assert(mss[0][0].Id(), jc.DeepEquals, m1.Id())

	mss = httpbakery.MacaroonsForURL(jar, u2)

	checked := make(map[string]int)
	for _, ms := range mss {
		checked[string(ms[0].Id())]++
		err := svc.Check(context.Background(), ms)
		c.Assert(err, gc.IsNil)
	}
	c.Assert(checked, jc.DeepEquals, map[string]int{
		string(m1.Id()): 1,
		string(m2.Id()): 1,
	})
}

func (s *ClientSuite) TestDoWithBodyAndCustomError(c *gc.C) {
	d := bakerytest.NewDischarger(nil, nil)
	defer d.Close()

	// Create a target service.
	svc := newService("loc", d, nil)

	type customError struct {
		CustomError *httpbakery.Error
	}
	callCount := 0
	handler := func(w http.ResponseWriter, req *http.Request) {
		callCount++
		if _, _, checkErr := httpbakery.CheckRequest(context.Background(), svc, req, nil); checkErr != nil {
			httprequest.WriteJSON(w, http.StatusTeapot, customError{
				CustomError: newDischargeRequiredError(serverHandlerParams{
					service:      svc,
					authLocation: d.Location(),
				}, checkErr, req).(*httpbakery.Error),
			})
			return
		}
		fmt.Fprintf(w, "hello there")
	}
	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	req, err := http.NewRequest("GET", srv.URL, nil)
	c.Assert(err, gc.IsNil)

	// First check that a normal request fails.
	resp, err := httpbakery.NewClient().Do(req)
	c.Assert(err, gc.IsNil)
	defer resp.Body.Close()
	c.Assert(resp.StatusCode, gc.Equals, http.StatusTeapot)
	c.Assert(callCount, gc.Equals, 1)
	callCount = 0

	// Then check that a request with a custom error getter succeeds.
	errorGetter := func(resp *http.Response) error {
		if resp.StatusCode != http.StatusTeapot {
			return nil
		}
		data, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			panic(err)
		}
		var respErr customError
		if err := json.Unmarshal(data, &respErr); err != nil {
			panic(err)
		}
		return respErr.CustomError
	}

	resp, err = httpbakery.NewClient().DoWithBodyAndCustomError(req, nil, errorGetter)
	c.Assert(err, gc.IsNil)

	data, err := ioutil.ReadAll(resp.Body)
	c.Assert(err, gc.IsNil)
	c.Assert(string(data), gc.Equals, "hello there")
	c.Assert(callCount, gc.Equals, 2)
}

func (s *ClientSuite) TestHandleError(c *gc.C) {
	d := bakerytest.NewDischarger(nil, nil)
	defer d.Close()

	// Create a target service.
	svc := newService("loc", d, nil)

	srv := httptest.NewServer(serverHandler(serverHandlerParams{
		service:      svc,
		authLocation: "unknown",
		mutateError:  nil,
	}))
	defer srv.Close()

	m, err := svc.NewMacaroon(bakery.LatestVersion, []checkers.Caveat{{
		Location:  d.Location(),
		Condition: "something",
	}})

	c.Assert(err, gc.IsNil)

	u, err := url.Parse(srv.URL + "/bar")
	c.Assert(err, gc.IsNil)

	respErr := &httpbakery.Error{
		Message: "an error",
		Code:    httpbakery.ErrDischargeRequired,
		Info: &httpbakery.ErrorInfo{
			Macaroon:     m,
			MacaroonPath: "/foo",
		},
	}
	client := httpbakery.NewClient()
	err = client.HandleError(u, respErr)
	c.Assert(err, gc.Equals, nil)
	// No cookies at the original location.
	c.Assert(client.Client.Jar.Cookies(u), gc.HasLen, 0)

	u.Path = "/foo"
	cookies := client.Client.Jar.Cookies(u)
	c.Assert(cookies, gc.HasLen, 1)

	// Check that we can actually make a request
	// with the newly acquired macaroon cookies.

	req, err := http.NewRequest("GET", srv.URL+"/foo", nil)
	c.Assert(err, gc.IsNil)

	resp, err := client.Do(req)
	c.Assert(err, gc.IsNil)
	resp.Body.Close()
	c.Assert(resp.StatusCode, gc.Equals, http.StatusOK)
}

func (s *ClientSuite) TestNewClientOldServer(c *gc.C) {
	d := bakerytest.NewDischarger(nil, nil)
	defer d.Close()

	// Create a target service.
	svc := newService("loc", d, nil)

	srv := httptest.NewServer(serverHandler(serverHandlerParams{
		service:      svc,
		authLocation: d.Location(),
	}))
	defer srv.Close()

	// Make the request to the server.
	client := httpbakery.NewClient()
	req, err := http.NewRequest("GET", srv.URL, nil)
	c.Assert(err, gc.IsNil)
	resp, err := client.Do(req)
	c.Assert(err, gc.IsNil)
	defer resp.Body.Close()
	assertResponse(c, resp, "done")
}

func (s *ClientSuite) TestHandleErrorDifferentError(c *gc.C) {
	berr := &httpbakery.Error{
		Message: "an error",
		Code:    "another code",
	}
	client := httpbakery.NewClient()
	err := client.HandleError(&url.URL{}, berr)
	c.Assert(err, gc.Equals, berr)
}

func (s *ClientSuite) TestNewCookieExpires(c *gc.C) {
	t := time.Now().Add(24 * time.Hour)
	svc := newService("loc", nil, nil)
	m, err := svc.NewMacaroon(bakery.LatestVersion, []checkers.Caveat{
		checkers.TimeBeforeCaveat(t),
	})

	c.Assert(err, gc.IsNil)
	cookie, err := httpbakery.NewCookie(nil, macaroon.Slice{m})
	c.Assert(err, gc.IsNil)
	c.Assert(cookie.Expires.Equal(t), gc.Equals, true, gc.Commentf("obtained: %s, expected: %s", cookie.Expires, t))
}

func mustParseURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

type visitHandler struct {
	mux     *http.ServeMux
	rendez  chan struct{}
	respond func(w http.ResponseWriter)
}

func newVisitHandler(respond func(http.ResponseWriter)) *visitHandler {
	h := &visitHandler{
		rendez:  make(chan struct{}, 1),
		respond: respond,
		mux:     http.NewServeMux(),
	}
	h.mux.HandleFunc("/visit", h.serveVisit)
	h.mux.HandleFunc("/wait", h.serveWait)
	return h
}

func (h *visitHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	h.mux.ServeHTTP(w, req)
}

func (h *visitHandler) serveVisit(w http.ResponseWriter, req *http.Request) {
	h.rendez <- struct{}{}
}

func (h *visitHandler) serveWait(w http.ResponseWriter, req *http.Request) {
	<-h.rendez
	h.respond(w)
}

// assertResponse asserts that the given response is OK and contains
// the expected body text.
func assertResponse(c *gc.C, resp *http.Response, expectBody string) {
	body, err := ioutil.ReadAll(resp.Body)
	c.Assert(err, gc.IsNil)
	c.Assert(resp.StatusCode, gc.Equals, http.StatusOK, gc.Commentf("body %q", body))
	c.Assert(string(body), gc.DeepEquals, expectBody)
}

func noVisit(*url.URL) error {
	return fmt.Errorf("should not be visiting")
}

type readCounter struct {
	io.ReadSeeker
	byteCount int
}

func (r *readCounter) Read(buf []byte) (int, error) {
	n, err := r.ReadSeeker.Read(buf)
	r.byteCount += n
	return n, err
}

func newService(location string, locator bakery.ThirdPartyLocator, checker bakery.FirstPartyCaveatChecker) *bakery.Service {
	if checker == nil {
		c := checkers.New(nil)
		c.Namespace().Register("testns", "")
		c.Register("is", "testns", checkIsSomething)
		checker = c
	}
	svc, err := bakery.NewService(bakery.NewServiceParams{
		Location: location,
		Locator:  locator,
		Checker:  checker,
	})
	if err != nil {
		panic(err)
	}
	return svc
}

func clientRequestWithCookies(c *gc.C, u string, macaroons macaroon.Slice) *http.Client {
	client := httpbakery.NewHTTPClient()
	url, err := url.Parse(u)
	c.Assert(err, gc.IsNil)
	err = httpbakery.SetCookie(client.Jar, url, nil, macaroons)
	c.Assert(err, gc.IsNil)
	return client
}

var handleErrors = httprequest.ErrorMapper(httpbakery.ErrorToResponse).HandleErrors

type serverHandlerParams struct {
	// service holds the service that will be used to check incoming
	// requests.
	service *bakery.Service

	// checker is used to check first party caveats in macaroons.
	// If it is nil, isChecker("something") will be used.
	checker bakery.FirstPartyCaveatChecker

	// authLocation holds the location of any 3rd party authorizer.
	// If this is non-empty, a 3rd party caveat will be added
	// addressed to this location.
	authLocation string

	// When authLocation is non-empty and thirdPartyCondition
	// is non-zero, it will be called to determine the condition
	// to address to he third party.
	thirdPartyCondition func() string

	// mutateError, if non-zero, will be called with any
	// discharge-required error before responding
	// to the client.
	mutateError func(*httpbakery.Error)

	// If caveats is non-nil, it is called to get caveats to
	// add to the returned macaroon.
	caveats func() []checkers.Caveat

	// alwaysReadBody specifies whether the handler should always read
	// the entire request body before returning.
	alwaysReadBody bool

	// version holds the version of the bakery that the
	// server will purport to serve. If zero, bakery.LatestVersion
	// will be assumed.
	version bakery.Version
}

// serverHandler returns an HTTP handler that checks macaroon authorization
// and, if that succeeds, writes the string "done" followed by all the
// data read from the request body.
// It recognises the single first party caveat "is something".
func serverHandler(hp serverHandlerParams) http.Handler {
	h := handleErrors(func(p httprequest.Params) error {
		if hp.alwaysReadBody {
			defer ioutil.ReadAll(p.Request.Body)
		}
		if _, _, checkErr := httpbakery.CheckRequest(context.Background(), hp.service, p.Request, nil); checkErr != nil {
			return newDischargeRequiredError(hp, checkErr, p.Request)
		}
		fmt.Fprintf(p.Response, "done")
		// Special case: the no-body path doesn't return the body.
		if p.Request.URL.Path == "/no-body" {
			return nil
		}
		data, err := ioutil.ReadAll(p.Request.Body)
		if err != nil {
			panic(fmt.Errorf("cannot read body: %v", err))
		}
		if len(data) > 0 {
			fmt.Fprintf(p.Response, " %s", data)
		}
		return nil
	})
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		h(w, req, nil)
	})
}

// newDischargeRequiredError returns a discharge-required error holding
// a newly minted macaroon referencing the original check error
// checkErr. If hp.authLocation is non-empty, the issued macaroon will
// contain an "is-ok" third party caveat addressed to that location.
//
// If req is non-nil, it will be used to pass to NewDischargeRequiredErrorForRequest,
// otherwise the old protocol (triggered by NewDischargeRequiredError) will be used.
func newDischargeRequiredError(hp serverHandlerParams, checkErr error, req *http.Request) error {
	if hp.version == 0 {
		hp.version = bakery.LatestVersion
	}
	if req != nil {
		if v := httpbakery.RequestVersion(req); v < bakery.LatestVersion {
			hp.version = v
		}
	}
	var caveats []checkers.Caveat
	if hp.authLocation != "" {
		caveats = []checkers.Caveat{{
			Location:  hp.authLocation,
			Condition: "is-ok",
		}}
	}
	if hp.caveats != nil {
		caveats = append(caveats, hp.caveats()...)
	}
	m, err := hp.service.NewMacaroon(hp.version, caveats)
	if err != nil {
		panic(fmt.Errorf("cannot make new macaroon: %v", err))
	}
	err = httpbakery.NewDischargeRequiredErrorWithVersion(m, "", checkErr, hp.version)
	if hp.mutateError != nil {
		hp.mutateError(err.(*httpbakery.Error))
	}
	return err
}

func checkIsSomething(ctxt context.Context, _, arg string) error {
	if arg != "something" {
		return fmt.Errorf(`%v doesn't match "something"`, arg)
	}
	return nil
}

func noCaveatChecker(_ *http.Request, cond, arg string) ([]checkers.Caveat, error) {
	return nil, nil
}
