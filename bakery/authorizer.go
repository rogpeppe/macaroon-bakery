package bakery

import (
	"golang.org/x/net/context"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery/checkers"
)

// Authorizer is used to check whether a given user is allowed
// to perform a set of operations.
type Authorizer interface {
	// Allow checks whether the given identity (which will be nil
	// when there is no authenticated user) is allowed to perform
	// the given operations. It should return an error only when
	// some underlying database operation has failed, not when the
	// user has been denied access.
	//
	// On success, each element of allowed holds whether the respective
	// element of ops has been allowed, and caveats holds any additional
	// third party caveats that apply.
	Authorize(ctxt context.Context, id Identity, ops []Op) (allowed []bool, caveats []checkers.Caveat, err error)
}

// OpenAuthorizer is an authorizer implementation that will authorize all operations without question.
var OpenAuthorizer openAuthorizer

// ClosedAuthorizer is an authorizer implementation that will return ErrPermissionDenied
// on all authorization requests.
var ClosedAuthorizer closedAuthorizer

type openAuthorizer struct{}

func (openAuthorizer) Authorize(ctxt context.Context, id Identity, ops []Op) (allowed []bool, caveats []checkers.Caveat, err error) {
	allowed = make([]bool, len(ops))
	for i := range allowed {
		allowed[i] = true
	}
	return allowed, nil, nil
}

type closedAuthorizer struct{}

func (closedAuthorizer) Authorize(ctxt context.Context, id Identity, ops []Op) (allowed []bool, caveats []checkers.Caveat, err error) {
	return make([]bool, len(ops)), nil, ErrPermissionDenied
}
