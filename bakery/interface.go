package bakery

import (
	"golang.org/x/net/context"
	"gopkg.in/macaroon.v2-unstable"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery/checkers"
)

// Op holds an entity and action to be authorized on that entity.
type Op struct {
	// Action holds the action to perform on the entity, such as "read"
	// or "delete". It is up to the service using a checker to define
	// a set of operations and keep them consistent over time.
	Action string

	// Entity holds the name of the entity to be authorized.
	// Entity names should not contain spaces and should
	// not start with the prefix "login" or "multi-" (conventionally,
	// entity names will be prefixed with the entity type followed
	// by a hyphen.
	Entity string
}

// IdentityClient represents an abstract identity manager. A client can
// use this to create a third party caveat requesting authentication and
// to decode the authentication information from the resulting discharge
// macaroon caveats and then to find information about the resulting
// identity.
type IdentityClient interface {
	// IdentityCaveats encodes identity caveats addressed to the identity
	// service that request it to authenticate the user.
	IdentityCaveats() []checkers.Caveat

	// DeclaredIdentity parses the identity declaration from the given
	// declared attributes.
	DeclaredIdentity(declared map[string]string) (Identity, error)
}

// Identity holds identity information declared in a first party caveat
// added when discharging a third party caveat.
type Identity interface {
	// Id returns the id of the user, which may be an
	// opaque blob with no human meaning.
	// An id is only considered to be unique
	// with a given domain.
	Id() string

	// Domain holds the domain of the user. This
	// will be empty if the user was authenticated
	// directly with the identity provider.
	Domain() string
}

// MacaroonStore defines persistent storage for macaroon root keys.
type MacaroonStore interface {
	// MacaroonInfo verifies the signature of the given macaroon and returns
	// information on its associated operations, and all the first party
	// caveat conditions that need to be checked.
	//
	// This method should not check first party caveats itself.
	// TODO define some error type so we can distinguish storage errors
	// from bad ids and macaroon-not-found errors.
	MacaroonInfo(ctxt context.Context, ms macaroon.Slice) ([]Op, []string, error)
}

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
