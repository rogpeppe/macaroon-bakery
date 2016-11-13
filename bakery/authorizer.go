package bakery

import (
	"golang.org/x/net/context"
	"gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery/checkers"
)

const Everyone = "everyone"

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

var (
	// OpenAuthorizer is an Authorizer implementation that will authorize all operations without question.
	OpenAuthorizer openAuthorizer

	// ClosedAuthorizer is an Authorizer implementation that will return ErrPermissionDenied
	// on all authorization requests.
	ClosedAuthorizer closedAuthorizer
)

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

type authInfoKey struct{}

func ContextWithAuthInfo(ctxt context.Context, authInfo *AuthInfo) context.Context {
	return context.WithValue(ctxt, authInfoKey{}, authInfo)
}

func AuthInfoFromContext(ctxt context.Context) *AuthInfo {
	authInfo, _ := ctxt.Value(authInfoKey{}).(*AuthInfo)
	return authInfo
}

// ACLAuthorizer is an Authorizer implementation that will check ACL membership
// of users. It uses GetACLs to find out the ACLs that apply to the requested
// operations and will authorize an operation if an ACL contains the
// group "everyone" or if the context contains an AuthInfo (see
// ContextWithAuthInfo) that holds an Identity that implements
// ACLIdentity and its Allow method returns true for the ACL.
type ACLAuthorizer struct {
	// If AllowPublic is true and an ACL contains "everyone",
	// then authorization will be granted even if there is
	// no logged in user.
	AllowPublic bool

	// GetACLs returns the ACL that applies to each of the given
	// operations. It should return a slice with each element
	// holding the ACL for the corresponding operation in the
	// argument slice.
	//
	// If an entity cannot be found or the action is not recognised,
	// GetACLs should return an empty ACL entry for that operation.
	GetACLs func(ctxt context.Context, ops []Op) ([][]string, error)
}

// ACLIdentity may be implemented by Identity implementions
// to report group membership information.
// See ACLAuthorizer for details.
type ACLIdentity interface {
	Identity

	// Allow reports whether the user should be allowed to access
	// any of the users or groups in the given ACL slice.
	Allow(ctxt context.Context, acl []string) (bool, error)
}

func (a ACLAuthorizer) Authorize(ctxt context.Context, id Identity, ops []Op) (allowed []bool, caveats []checkers.Caveat, err error) {
	if ops == nil {
		// Anyone is allowed to do nothing.
		return nil, nil, nil
	}
	var ident ACLIdentity
	authInfo := AuthInfoFromContext(ctxt)
	if authInfo != nil {
		ident, _ = authInfo.Identity.(ACLIdentity)
	}
	acls, err := a.GetACLs(ctxt, ops)
	if err != nil {
		return nil, nil, errgo.Notef(err, "cannot retrieve ACLs")
	}
	if len(acls) != len(ops) {
		return nil, nil, errgo.Notef(err, "mismatched ACLs %q for requested operations %#v", acls, ops)
	}
	allowed = make([]bool, len(acls))
	for i, acl := range acls {
		if ident != nil {
			allowed[i], err = ident.Allow(ctxt, acl)
			if err != nil {
				return nil, nil, errgo.Notef(err, "cannot check permissions")
			}
		} else {
			allowed[i] = a.AllowPublic && isPublicACL(acl)
		}
	}
	return allowed, nil, nil
}

func isPublicACL(acl []string) bool {
	for _, g := range acl {
		if g == Everyone {
			return true
		}
	}
	return false
}
