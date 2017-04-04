// Package bakerytest provides test helper functions for
// the bakery.
package bakerytest

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"log"
	"time"

	"github.com/juju/httprequest"
	"github.com/julienschmidt/httprouter"
	"golang.org/x/net/context"
	"gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
	"gopkg.in/macaroon-bakery.v2-unstable/bakery/checkers"
	"gopkg.in/macaroon-bakery.v2-unstable/httpbakery"
)

type Discharger struct {
	server *httptest.Server

	Mux     *httprouter.Router
	Key     *bakery.KeyPair
	Locator bakery.ThirdPartyLocator

	// Checker is called to check third party caveats
	// when they're discharged. If it's nil, caveats
	// will be discharged unconditionally.
	Checker httpbakery.ThirdPartyCaveatChecker

	interactors []InteractionHandler

	mu         sync.Mutex
	maxId      int
	discharges map[string]*bakery.ThirdPartyCaveatInfo
}

func NewDischarger(locator bakery.ThirdPartyLocator) *Discharger {
	key, err := bakery.GenerateKey()
	if err != nil {
		panic(err)
	}
	d := &Discharger{
		Mux:        httprouter.New(),
		Key:        key,
		Locator:    locator,
		discharges: make(map[string]*bakery.ThirdPartyCaveatInfo),
	}
	d.server = httptest.NewTLSServer(d.Mux)
	bd := httpbakery.NewDischarger(httpbakery.DischargerParams{
		Key:     key,
		Locator: locator,
		Checker: d,
	})
	addHandlers(d.Mux, bd.Handlers())
	startSkipVerify()
	return d
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

// AddInteractor adds the given interaction handler to the discharger.
// When NewInteractionRequiredError is called, the AddInteraction
// method will be used to add interaction information to the
// error.
func (d *Discharger) AddInteractor(i InteractionHandler) {
	addHandlers(d.Mux, i.Handlers())
	d.interactors = append(d.interactors, i)
}

// NewInteractionRequiredError returns an error suitable for returning
// from a third-party caveat checker that requires some interaction from
// the client. The given caveat provides information about the caveat
// that's being discharged. The returned error will include information
// from all the InteractionHandler instances added with d.AddInteractor.
func (d *Discharger) NewInteractionRequiredError(cav *bakery.ThirdPartyCaveatInfo, req *http.Request) *httpbakery.Error {
	d.mu.Lock()
	dischargeId := fmt.Sprintf("%d", d.maxId)
	d.maxId++
	d.discharges[dischargeId] = cav
	d.mu.Unlock()

	err := httpbakery.NewInteractionRequiredError(nil, req)
	for _, i := range d.interactors {
		i.SetInteraction(err, req, dischargeId)
	}
	return err
}

// CompleteDischarge completes the discharge with the
// given id by creating a discharge macaroon.
// If uses the given checker to check the caveat. If
// checker is nil, the caveat will be discharged unconditionally.
func (d *Discharger) CompleteDischarge(
	ctx context.Context,
	dischargeId string,
	checker bakery.ThirdPartyCaveatChecker,
) (*bakery.Macaroon, error) {
	if checker == nil {
		checker = bakery.ThirdPartyCaveatCheckerFunc(func(ctx context.Context, cav *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
			return nil, nil
		})
	}
	cav := d.DischargeInfo(dischargeId)
	return bakery.Discharge(ctx, bakery.DischargeParams{
		Id:      cav.Id,
		Caveat:  cav.Caveat,
		Key:     d.Key,
		Checker: checker,
		Locator: d.Locator,
	})
}

type InteractionHandler interface {
	// SetInteraction adds information to the given error
	// that will tell the client how to interact with the given
	// discharge id.
	// The request is the request that the interaction
	// error is being returned in response to.
	SetInteraction(err *httpbakery.Error, req *http.Request, dischargeId string)

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
	caveats, err := d.Checker.CheckThirdPartyCaveat(ctx, req, cav)
	if err != nil {
		return nil, errgo.Mask(err, errgo.Any)
	}
	return caveats, nil
}

// DischargeInfo returns the information associated with
// the given discharge id. It panics if the discharge id isn't
// found.
func (d *Discharger) DischargeInfo(dischargeId string) *bakery.ThirdPartyCaveatInfo {
	d.mu.Lock()
	cav, ok := d.discharges[dischargeId]
	d.mu.Unlock()
	if !ok {
		panic(errgo.Newf("discharge id %s not found", dischargeId))
	}
	return cav
}

type DischargeCreator interface {
	Discharge(dischargeId string, req *http.Request, checker httpbakery.ThirdPartyCaveatChecker) (*bakery.Macaroon, error)
}

// VisitWaitHandler represents an interaction which involves
// the client doing a GET of a "visit" URL and of a "wait" URL
// which returns the discharged macaroon.
type VisitWaitHandler struct {
	discharger *Discharger
	checker    httpbakery.ThirdPartyCaveatChecker

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

	mu      sync.Mutex
	waiting map[string]*dischargeFuture
}

type dischargeFuture struct {
	caveats []checkers.Caveat
	err     error
	done    chan struct{}
}

// NewVisitWaitHandler returns a new VisitWaitHandler that
// processes visit/wait-style interactions using the given
// discharger, and that uses checker to check third party
// caveats that use it.
//
// Once created, it can be added to the discharger with AddInteractor.
func NewVisitWaitHandler(d *Discharger, checker httpbakery.ThirdPartyCaveatChecker) *VisitWaitHandler {
	return &VisitWaitHandler{
		discharger: d,
		checker:    checker,
		waiting:    make(map[string]*dischargeFuture),
	}
}

// Handlers implements InteractionHandler.Handlers by returning
// the /wait and /visit endpoints.
func (i *VisitWaitHandler) Handlers() []httprequest.Handler {
	return reqServer.Handlers(func(p httprequest.Params) (*visitWaitHandlers, context.Context, error) {
		return &visitWaitHandlers{i}, p.Context, nil
	})
}

// SetInteraction implements InteractionHandler.SetInteraction.
func (v *VisitWaitHandler) SetInteraction(err *httpbakery.Error, _ *http.Request, dischargeId string) {
	v.waiting[dischargeId] = &dischargeFuture{
		done: make(chan struct{}),
	}
	httpbakery.WebBrowserInteractor{}.SetInteraction(err,
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
	i.mu.Lock()
	defer i.mu.Unlock()
	discharge, ok := i.waiting[dischargeId]
	if !ok {
		panic(errgo.Newf("invalid discharge id %q", dischargeId))
	}
	select {
	case <-discharge.done:
		return errgo.Newf("FinishInteraction called twice")
	default:
	}
	discharge.caveats, discharge.err = cavs, err
	close(discharge.done)
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

func (h *visitWaitHandlers) Visit(p httprequest.Params, r *visitRequest) error {
	log.Printf("visitWaitHandlers.Visit, visit func %p", h.interactor.Visit)
	if h.interactor.Visit != nil {
		err := h.interactor.Visit(p.Response, p.Request, r.DischargeId)
		return errgo.Mask(err, errgo.Any)
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

func (h *visitWaitHandlers) Wait(p httprequest.Params, r *waitRequest) (*httpbakery.WaitResponse, error) {
	d := h.interactor.discharger
	d.mu.Lock()
	discharge, ok := h.interactor.waiting[r.DischargeId]
	d.mu.Unlock()
	if !ok {
		return nil, errgo.Newf("invalid wait id %q", r.DischargeId)
	}
	select {
	case <-discharge.done:
	case <-time.After(5 * time.Second):
		return nil, errgo.New("timeout waiting for interaction to complete")
	}
	// Wrap h.interactor.checker to add the caveats provided to FinishInteraction.
	checker := func(ctx context.Context, cav *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
		if h.interactor.checker == nil {
			return discharge.caveats, nil
		}
		caveats, err := h.interactor.checker.CheckThirdPartyCaveat(ctx, p.Request, cav)
		if err != nil {
			return nil, errgo.Mask(err, errgo.Any)
		}
		return append(caveats, discharge.caveats...), nil
	}
	m, err := h.interactor.discharger.CompleteDischarge(p.Context, r.DischargeId, bakery.ThirdPartyCaveatCheckerFunc(checker))
	if err != nil {
		return nil, errgo.Mask(err, errgo.Any)
	}
	return &httpbakery.WaitResponse{
		Macaroon: m,
	}, nil
}

// ConditionParser adapts the given function into an httpbakery.ThirdPartyCaveatChecker.
// It parses the caveat's condition and calls the function with the result.
func ConditionParser(check func(cond, arg string) ([]checkers.Caveat, error)) httpbakery.ThirdPartyCaveatChecker {
	f := func(ctx context.Context, req *http.Request, cav *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
		cond, arg, err := checkers.ParseCaveat(string(cav.Condition))
		if err != nil {
			return nil, err
		}
		return check(cond, arg)
	}
	return httpbakery.ThirdPartyCaveatCheckerFunc(f)
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

func addHandlers(mux *httprouter.Router, hs []httprequest.Handler) {
	for _, h := range hs {
		mux.Handle(h.Method, h.Path, h.Handle)
	}
}
