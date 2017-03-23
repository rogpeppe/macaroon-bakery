// Package bakerytest provides test helper functions for
// the bakery.
package bakerytest

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"github.com/juju/httprequest"
	"golang.org/x/net/context"
	"gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
	"gopkg.in/macaroon-bakery.v2-unstable/bakery/checkers"
	"gopkg.in/macaroon-bakery.v2-unstable/httpbakery"
)

// Checker is used to check third party caveats.
// It's the same as httpbakery.ThirdPartyCaveatCheckerFunc
// except for the extra interactionKind argument which
// can be used to determine what kind of result
// to return. For the initial discharge request, interactionKind
// will be empty.
type Checker func(ctx context.Context, req *http.Request, cav *bakery.ThirdPartyCaveatInfo, interactionKind string) ([]checkers.Caveat, error)

type Discharger struct {
	Key     *bakery.KeyPair
	Locator bakery.ThirdPartyLocator
	// Checker is called to check third party caveats
	// when they're discharged. If it's nil, caveats
	// will be discharged unconditionally.
	Checker Checker

	server      *httptest.Server
	interactors []InteractionHandler

	mu      sync.Mutex
	id      int
	waiting map[string]discharge
}

func NewDischarger(
	locator bakery.ThirdPartyLocator,
) *Discharger {
	mux := http.NewServeMux()
	server := httptest.NewTLSServer(mux)
	key, err := bakery.GenerateKey()
	if err != nil {
		panic(err)
	}
	d := &Discharger{
		Key:     key,
		Locator: locator,
		server:  server,
		waiting: make(map[string]discharge),
	}
	bd := httpbakery.NewDischarger(httpbakery.DischargerParams{
		Key:     key,
		Locator: locator,
		Checker: d,
	})
	bd.AddMuxHandlers(mux, "/")
	startSkipVerify()
	return d
}

// ConditionParser adapts the given function into a Checker.
// It parses the caveat's condition and calls the function with the result.
func ConditionParser(check func(cond, arg string) ([]checkers.Caveat, error)) Checker {
	return func(ctx context.Context, req *http.Request, cav *bakery.ThirdPartyCaveatInfo, kind string) ([]checkers.Caveat, error) {
		cond, arg, err := checkers.ParseCaveat(string(cav.Condition))
		if err != nil {
			return nil, err
		}
		return check(cond, arg)
	}
}

// Close shuts down the server. It may be called more than
// once on the same discharger.
func (d *Discharger) Close() {
	if d.server == nil {
		return
	}
	d.server.Close()
	stopSkipVerify()
	d.server = nil
}

// Location returns the location of the discharger, suitable
// for setting as the location in a third party caveat.
// This will be the URL of the server.
func (d *Discharger) Location() string {
	return d.server.URL
}

// PublicKeyForLocation implements bakery.PublicKeyLocator.
func (d *Discharger) ThirdPartyInfo(ctx context.Context, loc string) (bakery.ThirdPartyInfo, error) {
	if loc == d.Location() {
		return bakery.ThirdPartyInfo{
			PublicKey: d.Key.Public,
			Version:   bakery.LatestVersion,
		}, nil
	}
	return bakery.ThirdPartyInfo{}, bakery.ErrNotFound
}

var skipVerify struct {
	mu            sync.Mutex
	refCount      int
	oldSkipVerify bool
}

func startSkipVerify() {
	v := &skipVerify
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.refCount++; v.refCount > 1 {
		return
	}
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return
	}
	if transport.TLSClientConfig != nil {
		v.oldSkipVerify = transport.TLSClientConfig.InsecureSkipVerify
		transport.TLSClientConfig.InsecureSkipVerify = true
	} else {
		v.oldSkipVerify = false
		transport.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true,
		}
	}
}

func stopSkipVerify() {
	v := &skipVerify
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.refCount--; v.refCount > 0 {
		return
	}
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return
	}
	// technically this doesn't return us to the original state,
	// as TLSClientConfig may have been nil before but won't
	// be now, but that should be equivalent.
	transport.TLSClientConfig.InsecureSkipVerify = v.oldSkipVerify
}

type dischargeResult struct {
	caveats []checkers.Caveat
	err     error
}

type discharge struct {
	caveatInfo *bakery.ThirdPartyCaveatInfo
	c          chan dischargeResult
}

// AddInteractor adds the given interaction handler to the discharger.
// When NewInteractionRequiredError is called, the AddInteraction
// method will be used to add interaction information to the
// error.
func (d *Discharger) AddInteractor(i InteractionHandler) {
	d.interactors = append(d.interactors, i)
}

// NewInteractionRequiredError returns an error suitable for returning
// from a third-party caveat checker that requires some interaction from
// the client. The given caveat provides information about the caveat
// that's being discharged. The returned error will include information
// from all the InteractionHandler instances added with d.AddInteractor.
func (d *Discharger) NewInteractionRequiredError(cav *bakery.ThirdPartyCaveatInfo, req *http.Request) *httpbakery.Error {
	d.mu.Lock()
	dischargeId := fmt.Sprintf("%d", d.id)
	d.id++
	d.waiting[dischargeId] = discharge{
		caveatInfo: cav,
		c:          make(chan dischargeResult, 1),
	}
	d.mu.Unlock()

	err := httpbakery.NewInteractionRequiredError(nil, req)
	for _, i := range d.interactors {
		i.SetInteraction(err, dischargeId)
	}
	return err
}

type InteractionHandler interface {
	// SetInteraction adds information to the given error
	// that will tell the client how to interact with the given
	// discharge id.
	SetInteraction(err *httpbakery.Error, dischargeId string)

	// Handlers returns any additional HTTP handlers required by
	// the interaction.
	Handlers() []httprequest.Handler
}

// CheckThirdPartyCaveat implements httpbakery.ThirdPartyCaveatChecker.
// If d.AddInteractor has been called, it will always return
// an interaction-required error; otherwise if d.Checker
// is non-nil, it will call that; otherwise it will
// discharge the caveat unconditionally.
func (d *Discharger) CheckThirdPartyCaveat(ctx context.Context, req *http.Request, cav *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
	if d.Checker == nil {
		return nil, nil
	}
	return d.Checker(ctx, req, cav, "")
}

// DischargeParams returns the discharge parameters for the
// given discharge id, suitable for passing to bakery.Discharge.
// The Checker field will unconditionally discharge any caveat.
//
// DischargeParams panics if the discharge id is not found.
func (d *Discharger) DischargeParams(dischargeId string, req *http.Request, interactionKind string) bakery.DischargeParams {
	d.mu.Lock()
	discharge, ok := d.waiting[dischargeId]
	d.mu.Unlock()
	if !ok {
		panic(errgo.Newf("discharge id %s not found", dischargeId))
	}
	return bakery.DischargeParams{
		Id:     discharge.caveatInfo.Id,
		Caveat: discharge.caveatInfo.Caveat,
		Key:    d.Key,
		Checker: bakery.ThirdPartyCaveatCheckerFunc(func(ctx context.Context, cav *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
			if d.Checker == nil {
				return nil, nil
			}
			return d.Checker(ctx, req, cav, interactionKind)
		}),
		Locator: d.Locator,
	}
}

func NewVisitWaitHandler(d *Discharger) *VisitWaitHandler {
	return &VisitWaitHandler{
		discharger: d,
	}
}

// VisitWaitHandler represents an interaction which involves
// the client doing a GET of a "visit" URL and of a "wait" URL
// which returns the discharged macaroon.
type VisitWaitHandler struct {
	discharger *Discharger

	// Visit is called when a GET request is made to the /visit endpoint.
	// The discharge parameters can be passed to bakery.Discharge
	// to create the discharge macaroon.
	//
	// The returned macaroon is returned as the result of the
	// /wait endpoint.
	//
	// If Visit is nil, the macaroon will be discharged.
	// using the checker that the discharger was created with.
	Visit func(w http.ResponseWriter, req *http.Request, dischargeId string) error
}

// Handlers implements InteractionHandler.Handlers by returning
// the /wait and /visit endpoints.
func (i *VisitWaitHandler) Handlers() []httprequest.Handler {
	return reqServer.Handlers(func(p httprequest.Params) (*visitWaitHandlers, context.Context, error) {
		return &visitWaitHandlers{i}, p.Context, nil
	})
}

// SetInteraction implements InteractionHandler.SetInteraction.
func (v *VisitWaitHandler) SetInteraction(err *httpbakery.Error, dischargeId string) {
	httpbakery.WebBrowserWindowInteractor.SetInteraction(err,
		"/visit?dischargeid="+dischargeId,
		"/wait?dischargeid="+dischargeId,
	)
}

// FinishInteraction signals to the InteractiveDischarger that a
// particular interaction is complete and should return a response
// to the waiter. If err is nil, the discharge will be completed
// by calling the discharger's Checker function and adding
// the provided caveats to the discharge macaroon, otherwise
// an error will be returned.
func (i *VisitWaitHandler) FinishInteraction(dischargeId string, cavs []checkers.Caveat, err error) error {
	i.discharger.mu.Lock()
	discharge, ok := i.discharger.waiting[dischargeId]
	i.discharger.mu.Unlock()
	if !ok {
		return errgo.Newf("invalid wait id %q", dischargeId)
	}
	select {
	case discharge.c <- dischargeResult{caveats: cavs, err: err}:
	default:
		return errgo.Newf("cannot finish interaction %q", dischargeId)
	}
	return nil
}

var reqServer = httprequest.Server{
	ErrorMapper: httpbakery.ErrorToResponse,
}

type visitWaitHandlers struct {
	interactor *VisitWaitHandler
}

type visitRequest struct {
	httprequest.Route `httprequest:"GET /visit"`
	DischargeId       string `httprequest:"dischargeid,form"`
}

func (h *visitWaitHandlers) Visit(p httprequest.Params, r visitRequest) error {
	if h.interactor.Visit != nil {
		err := h.interactor.Visit(p.Response, p.Request, r.DischargeId)
		return errgo.Mask(err)
	}
	if err := h.interactor.FinishInteraction(r.DischargeId, nil, nil); err != nil {
		return errgo.Mask(err)
	}
	return nil
}

type waitRequest struct {
	httprequest.Route `httprequest:"GET /wait"`
	DischargeId       string `httprequest:"dischargeid,form"`
}

func (h *visitWaitHandlers) Wait(p httprequest.Params, r waitRequest) (*httpbakery.WaitResponse, error) {
	d := h.interactor.discharger
	d.mu.Lock()
	discharge, ok := d.waiting[r.DischargeId]
	d.mu.Unlock()
	if !ok {
		return nil, errgo.Newf("invalid wait id %q", r.DischargeId)
	}
	select {
	case res := <-discharge.c:
		if res.err != nil {
			return nil, errgo.Mask(res.err, errgo.Any)
		}
		dp := h.interactor.discharger.DischargeParams(
			r.DischargeId,
			p.Request,
			httpbakery.WebBrowserWindowInteractor.Kind(),
		)
		m, err := bakery.Discharge(p.Context, dp)
		if err != nil {
			return nil, errgo.Mask(err, errgo.Any)
		}
		// Add any caveats that were passed to FinishInteraction.
		err = m.AddCaveats(p.Context, res.caveats, h.interactor.discharger.Key, h.interactor.discharger)
		if err != nil {
			return nil, errgo.Mask(err)
		}
		return &httpbakery.WaitResponse{
			Macaroon: m,
		}, nil
	case <-time.After(5 * time.Second):
		return nil, errgo.New("timeout waiting for interaction to complete")
	}
}
