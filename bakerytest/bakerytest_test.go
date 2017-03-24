package bakerytest_test

import (
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"golang.org/x/net/context"
	gc "gopkg.in/check.v1"
	"github.com/julienschmidt/httprouter"
	"gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
	"gopkg.in/macaroon-bakery.v2-unstable/bakery/checkers"
	"gopkg.in/macaroon-bakery.v2-unstable/bakerytest"
	"gopkg.in/macaroon-bakery.v2-unstable/httpbakery"
)

type suite struct {
	client *httpbakery.Client
}

func (s *suite) SetUpTest(c *gc.C) {
	s.client = httpbakery.NewClient()
}

var _ = gc.Suite(&suite{})

var (
	ages        = time.Now().Add(time.Hour)
	testContext = context.Background()
	dischargeOp = bakery.Op{"thirdparty", "x"}
)

func (s *suite) TestDischargerSimple(c *gc.C) {
	d := bakerytest.NewDischarger(nil)
	defer d.Close()

	b := bakery.New(bakery.BakeryParams{
		Location: "here",
		Locator:  d,
		Key:      mustGenerateKey(),
	})
	m, err := b.Oven.NewMacaroon(context.Background(), bakery.LatestVersion, ages, []checkers.Caveat{{
		Location:  d.Location(),
		Condition: "something",
	}}, dischargeOp)

	c.Assert(err, gc.IsNil)
	ms, err := s.client.DischargeAll(context.Background(), m)
	c.Assert(err, gc.IsNil)
	c.Assert(ms, gc.HasLen, 2)

	_, err = b.Checker.Auth(ms).Allow(context.Background(), dischargeOp)
	c.Assert(err, gc.IsNil)
}

func (s *suite) TestDischargerTwoLevels(c *gc.C) {
	d1checker := func(cond, arg string) ([]checkers.Caveat, error) {
		if cond != "xtrue" {
			return nil, fmt.Errorf("caveat refused")
		}
		return nil, nil
	}
	d1 := bakerytest.NewDischarger(nil)
	d1.Checker = bakerytest.ConditionParser(d1checker)
	defer d1.Close()
	d2checker := func(cond, arg string) ([]checkers.Caveat, error) {
		return []checkers.Caveat{{
			Location:  d1.Location(),
			Condition: "x" + cond,
		}}, nil
	}
	d2 := bakerytest.NewDischarger(d1)
	d2.Checker = bakerytest.ConditionParser(d2checker)
	defer d2.Close()
	locator := bakery.NewThirdPartyStore()
	locator.AddInfo(d1.Location(), bakery.ThirdPartyInfo{
		PublicKey: d1.Key.Public,
		Version:   bakery.LatestVersion,
	})
	locator.AddInfo(d2.Location(), bakery.ThirdPartyInfo{
		PublicKey: d2.Key.Public,
		Version:   bakery.LatestVersion,
	})
	b := bakery.New(bakery.BakeryParams{
		Location: "here",
		Locator:  locator,
		Key:      mustGenerateKey(),
	})
	m, err := b.Oven.NewMacaroon(context.Background(), bakery.LatestVersion, ages, []checkers.Caveat{{
		Location:  d2.Location(),
		Condition: "true",
	}}, dischargeOp)

	c.Assert(err, gc.IsNil)

	ms, err := s.client.DischargeAll(context.Background(), m)
	c.Assert(err, gc.IsNil)
	c.Assert(ms, gc.HasLen, 3)

	_, err = b.Checker.Auth(ms).Allow(context.Background(), dischargeOp)
	c.Assert(err, gc.IsNil)

	err = b.Oven.AddCaveat(context.Background(), m, checkers.Caveat{
		Location:  d2.Location(),
		Condition: "nope",
	})
	c.Assert(err, gc.IsNil)

	ms, err = s.client.DischargeAll(context.Background(), m)
	c.Assert(err, gc.ErrorMatches, `cannot get discharge from "https://[^"]*": third party refused discharge: cannot discharge: caveat refused`)
	c.Assert(ms, gc.HasLen, 0)
}

func (s *suite) TestInsecureSkipVerifyRestoration(c *gc.C) {
	d1 := bakerytest.NewDischarger(nil)
	d2 := bakerytest.NewDischarger(nil)
	d2.Close()
	c.Assert(http.DefaultTransport.(*http.Transport).TLSClientConfig.InsecureSkipVerify, gc.Equals, true)
	d1.Close()
	c.Assert(http.DefaultTransport.(*http.Transport).TLSClientConfig.InsecureSkipVerify, gc.Equals, false)

	// When InsecureSkipVerify is already true, it should not
	// be restored to false.
	http.DefaultTransport.(*http.Transport).TLSClientConfig.InsecureSkipVerify = true
	d3 := bakerytest.NewDischarger(nil)
	d3.Close()

	c.Assert(http.DefaultTransport.(*http.Transport).TLSClientConfig.InsecureSkipVerify, gc.Equals, true)
}

func (s *suite) TestConcurrentDischargers(c *gc.C) {
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			d := bakerytest.NewDischarger(nil)
			d.Close()
			wg.Done()
		}()
	}
	wg.Wait()
	c.Assert(http.DefaultTransport.(*http.Transport).TLSClientConfig.InsecureSkipVerify, gc.Equals, false)
}

func (s *suite) TestInteractiveDischarger(c *gc.C) {
	d := bakerytest.NewDischarger(nil)
	defer d.Close()
	d.Checker = httpbakery.ThirdPartyCaveatCheckerFunc(func(ctx context.Context, req *http.Request, cav *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
		return nil, d.NewInteractionRequiredError(cav, req)
	})
	d.AddInteractor(bakerytest.NewVisitWaitHandler(
		d,
		httpbakery.ThirdPartyCaveatCheckerFunc(
			func(ctx context.Context, req *http.Request, cav *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
				if string(cav.Condition) != "something" {
					return nil, errgo.Newf("wrong condition")
				}
				return []checkers.Caveat{{
					Condition: "test pass",
				}}, nil
			},
		),
	))

	var r recordingChecker
	b := bakery.New(bakery.BakeryParams{
		Location: "here",
		Locator:  d,
		Checker:  &r,
		Key:      mustGenerateKey(),
	})
	m, err := b.Oven.NewMacaroon(context.Background(), bakery.LatestVersion, ages, []checkers.Caveat{{
		Location:  d.Location(),
		Condition: "something",
	}}, dischargeOp)

	c.Assert(err, gc.IsNil)
	client := httpbakery.NewClient()
	client.AddInteractor(newTestInteractor())
	ms, err := client.DischargeAll(context.Background(), m)
	c.Assert(err, gc.IsNil)
	c.Assert(ms, gc.HasLen, 2)

	_, err = b.Checker.Auth(ms).Allow(context.Background(), dischargeOp)
	c.Assert(err, gc.IsNil)
	// First caveat is time-before caveat added by NewMacaroon.
	// Second is the one added by the discharger above.
	c.Assert(r.caveats, gc.HasLen, 2)
	c.Assert(r.caveats[1], gc.Equals, "test pass")
}

func (s *suite) TestLoginDischargerError(c *gc.C) {
	d := bakerytest.NewDischarger(nil)
	defer d.Close()
	d.Checker = httpbakery.ThirdPartyCaveatCheckerFunc(func(ctx context.Context, req *http.Request, cav *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
		return nil, d.NewInteractionRequiredError(cav, req)
	})
	d.AddInteractor(bakerytest.NewVisitWaitHandler(
		d,
		httpbakery.ThirdPartyCaveatCheckerFunc(
			func(ctx context.Context, req *http.Request, cav *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
				return nil, errgo.New("test error")
			},
		),
	))

	b := bakery.New(bakery.BakeryParams{
		Location: "here",
		Locator:  d,
		Key:      mustGenerateKey(),
	})
	m, err := b.Oven.NewMacaroon(context.Background(), bakery.LatestVersion, ages, []checkers.Caveat{{
		Location:  d.Location(),
		Condition: "something",
	}}, dischargeOp)

	c.Assert(err, gc.IsNil)
	client := httpbakery.NewClient()
	client.AddInteractor(newTestInteractor())
	_, err = client.DischargeAll(context.Background(), m)
	c.Assert(err, gc.ErrorMatches, `cannot get discharge from ".*": failed to acquire macaroon after waiting: third party refused discharge: test error`)
}

func (s *suite) TestInteractiveDischargerFinishInteraction(c *gc.C) {
	d := bakerytest.NewDischarger(nil)
	defer d.Close()
	d.Checker = httpbakery.ThirdPartyCaveatCheckerFunc(func(ctx context.Context, req *http.Request, cav *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
		return nil, d.NewInteractionRequiredError(cav, req)
	})
	interactor := bakerytest.NewVisitWaitHandler(
		d,
		httpbakery.ThirdPartyCaveatCheckerFunc(
			func(ctx context.Context, req *http.Request, cav *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
				return []checkers.Caveat{{
					Condition: "condition1",
				}}, nil
			},
		),
	)
	d.AddInteractor(interactor)
	interactor.Visit = func(w http.ResponseWriter, req *http.Request, dischargeId string) error {
		http.Redirect(w, req,
			d.Location() + "/redirect?dischargeid=" + dischargeId,
			http.StatusFound,
		)
		return nil
	}
	d.Mux.GET("/redirect", func(w http.ResponseWriter, req *http.Request, p httprouter.Params) {
		req.ParseForm()
		interactor.FinishInteraction(req.Form.Get("dischargeid"), []checkers.Caveat{{
			Condition: "condition2",
		}}, nil)
	})
	var r recordingChecker
	b := bakery.New(bakery.BakeryParams{
		Location: "here",
		Locator:  d,
		Key:      mustGenerateKey(),
		Checker: &r,
	})
	m, err := b.Oven.NewMacaroon(context.Background(), bakery.LatestVersion, ages, []checkers.Caveat{{
		Location:  d.Location(),
		Condition: "something",
	}}, dischargeOp)

	c.Assert(err, gc.IsNil)
	client := httpbakery.NewClient()
	client.AddInteractor(newTestInteractor())

	ms, err := client.DischargeAll(context.Background(), m)
	c.Assert(err, gc.IsNil)
	c.Assert(ms, gc.HasLen, 2)

	_, err = b.Checker.Auth(ms).Allow(context.Background(), dischargeOp)
	c.Assert(err, gc.IsNil)

	// Note: the first caveat is the "time-before" caveat
	// that's always created.
	c.Assert(r.caveats[1:], gc.DeepEquals, []string{"condition1", "condition2"})
}

type recordingChecker struct {
	caveats []string
}

func (c *recordingChecker) CheckFirstPartyCaveat(ctx context.Context, caveat string) error {
	c.caveats = append(c.caveats, caveat)
	return nil
}

func (c *recordingChecker) Namespace() *checkers.Namespace {
	return nil
}

func mustGenerateKey() *bakery.KeyPair {
	key, err := bakery.GenerateKey()
	if err != nil {
		panic(err)
	}
	return key
}

func newTestInteractor() httpbakery.WebBrowserInteractor {
	return httpbakery.WebBrowserInteractor{
		OpenWebBrowser: func(u *url.URL) error {
			resp, err := http.Get(u.String())
			if err != nil {
				return errgo.Mask(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return errgo.Newf("unexpected status %q", resp.Status)
			}
			return nil
		},
	}
}
