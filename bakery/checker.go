package bakery

import (
	"sort"
	"sync"
	"time"

	"golang.org/x/net/context"
	errgo "gopkg.in/errgo.v1"
	macaroon "gopkg.in/macaroon.v2-unstable"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery/checkers"
)

// TODO think about a consistent approach to error reporting for macaroons.

// TODO should we really pass in explicit expiry times on each call to Allow?

var LoginOp = Op{
	Entity: "login",
	Action: "login",
}

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

var ErrPermissionDenied = errgo.New("permission denied")

type CheckerParams struct {
	// CaveatChecker is used to check first party caveats when authorizing.
	// If this is nil NewChecker will use checkers.New(nil).
	Checker FirstPartyCaveatChecker

	// Authorizer is used to check whether an authenticated user is
	// allowed to perform operations. If it is nil, NewChecker will
	// use ClosedAuthorizer.
	//
	// The identity parameter passed to Authorizer.Allow will
	// always have been obtained from a call to
	// IdentityClient.DeclaredIdentity.
	Authorizer Authorizer

	// IdentityClient is used for interactions with the external
	// identity service used for authentication.
	IdentityClient IdentityClient

	// MacaroonOps is used to retrieve macaroon root keys
	// and other associated information.
	MacaroonOpStore MacaroonOpStore
}

// AuthInfo information about an authorization decision.
type AuthInfo struct {
	// Identity holds information on the authenticated user as returned
	// from IdentityClient. It may be nil after a
	// successful authorization if LoginOp access was not required.
	Identity Identity

	// Macaroons holds all the macaroons that were used for the
	// authorization. Macaroons that were invalid or unnecessary are
	// not included.
	Macaroons []macaroon.Slice

	// TODO add information on user ids that have contributed
	// to the authorization:
	// After a successful call to Authorize or Capability,
	// AuthorizingUserIds returns the user ids that were used to
	// create the capability macaroons used to authorize the call.
	// Note that this is distinct from UserId, as there can only be
	// one authenticated user associated with the checker.
	// AuthorizingUserIds []string
}

// Checker wraps a FirstPartyCaveatChecker and adds authentication and authorization checks.
//
// It relies on a third party (the identity service) to authenticate
// users and define group membership.
//
// It uses macaroons as authorization tokens but it is not itself responsible for
// creating the macaroons - see the Oven type for one way of doing that.
//
// Identity and entities
//
// An Identity represents some user (or agent) authenticated by a third party.
//
// TODO
//
// Operations and authorization and capabilities
//
// An operation defines some requested action on an entity. For example,
// if file system server defines an entity for every file in the
// server, an operation to read a file might look like:
//
//     Op{
//		Entity: "/foo",
//		Action: "write",
//	}
//
// The exact set of entities and actions is up to the caller, but should
// be kept stable over time because authorization tokens will contain
// these names.
//
// To authorize some request on behalf of a remote user, first find out
// what operations that request needs to perform. For example, if the
// user tries to delete a file, the entity might be the path to the
// file's directory and the action might be "write". It may often be
// possible to determine the operations required by a request without
// reference to anything external, when the request itself contains all
// the necessary information.
//
// TODO update this.
//
// Third party caveats
//
// TODO.
type Checker struct {
	FirstPartyCaveatChecker
	p CheckerParams
}

// NewChecker returns a new Checker using the given parameters.
// If p.CaveatChecker is nil, it will be initialized to checkers.New(nil).
func NewChecker(p CheckerParams) *Checker {
	if p.Checker == nil {
		p.Checker = checkers.New(nil)
	}
	if p.Authorizer == nil {
		p.Authorizer = ClosedAuthorizer
	}
	return &Checker{
		FirstPartyCaveatChecker: p.Checker,
		p: p,
	}
}

// Auth makes a new AuthChecker instance using the
// given macaroons to inform authorization decisions.
func (c *Checker) Auth(mss ...macaroon.Slice) *AuthChecker {
	return &AuthChecker{
		Checker:   c,
		macaroons: mss,
	}
}

// AuthChecker authorizes operations with respect to a user's request.
// The identity is authenticated only once, the first time any method
// of the AuthChecker is called, using the context passed in then.
//
// To find out any declared identity without requiring a login,
// use Allow(ctxt); to require authentication but no additional operations,
// use Allow(ctxt, LoginOp).
type AuthChecker struct {
	// Checker is used to check first party caveats.
	*Checker
	macaroons []macaroon.Slice
	// conditions holds the first party caveat conditions
	// that apply to each of the above macaroons.
	conditions      [][]string
	initOnce        sync.Once
	initError       error
	identity        Identity
	identityCaveats []checkers.Caveat
	// authIndexes holds for each potentially authorized operation
	// the indexes of the macaroons that authorize it.
	authIndexes map[Op][]int
}

func (a *AuthChecker) init(ctxt context.Context) error {
	a.initOnce.Do(func() {
		a.initError = a.initOnceFunc(ctxt)
	})
	return a.initError
}

func (a *AuthChecker) initOnceFunc(ctxt context.Context) error {
	a.authIndexes = make(map[Op][]int)
	a.conditions = make([][]string, len(a.macaroons))
	for i, ms := range a.macaroons {
		ops, conditions, err := a.p.MacaroonOpStore.MacaroonOps(ctxt, ms)
		if err != nil {
			logger.Infof("cannot get macaroon info for %q\n", ms[0].Id())
			// TODO log error - if it's a store error, return early here.
			continue
		}
		// It's a valid macaroon (in principle - we haven't checked first party caveats).
		if len(ops) == 1 && ops[0] == LoginOp {
			// It's an authn macaroon
			declared, err := a.checkConditions(ctxt, LoginOp, conditions)
			if err != nil {
				logger.Infof("caveat check failed, id %q: %v\n", ms[0].Id(), err)
				// TODO log error
				continue
			}
			if a.identity != nil {
				logger.Infof("duplicate authentication macaroon")
				// TODO log duplicate authn-macaroon error
				continue
			}
			identity, err := a.p.IdentityClient.DeclaredIdentity(declared)
			if err != nil {
				logger.Infof("cannot decode declared identity: %v", err)
				// TODO log user-decode error
				continue
			}
			a.identity = identity
		}
		a.conditions[i] = conditions
		for _, op := range ops {
			a.authIndexes[op] = append(a.authIndexes[op], i)
		}
	}
	if a.identity == nil {
		// No identity yet, so try to get one based on the context.
		identity, caveats, err := a.p.IdentityClient.IdentityFromContext(ctxt)
		if err != nil {
			return errgo.Notef(err, "could not determine identity")
		}
		a.identity, a.identityCaveats = identity, caveats
	}
	logger.Infof("after init, identity: %#v, authIndexes %v", a.identity, a.authIndexes)
	return nil
}

// Allow checks that the authorizer's request is authorized to
// perform all the given operations. Note that Allow does not check
// first party caveats - if there is more than one macaroon that may
// authorize the request, it will choose the first one that does regardless
//
// If all the operations are allowed, an AuthInfo is returned holding
// details of the decision and any first party caveats that must be
// checked before actually executing any operation.
//
// If operations include LoginOp, the request must contain an
// authentication macaroon proving the client's identity. Once an
// authentication macaroon is chosen, it will be used for all other
// authorization requests.
//
// If an operation was not allowed, an error will be returned which may
// be *DischargeRequiredError holding the operations that remain to
// be authorized in order to allow authorization to
// proceed.
func (a *AuthChecker) Allow(ctxt context.Context, ops ...Op) (*AuthInfo, error) {
	authInfo, _, err := a.AllowAny(ctxt, ops...)
	if err != nil {
		return nil, err
	}
	return authInfo, nil
}

// AllowAny is like Allow except that it will authorize as many of the
// operations as possible without requiring any to be authorized. If all
// the operations succeeded, the returned error and slice will be nil.
//
// If any the operations failed, the returned error will be the same
// that Allow would return and each element in the returned slice will
// hold whether its respective operation was allowed.
//
// If all the operations succeeded, the returned slice will be nil.
//
// The returned *AuthInfo will always be non-nil.
//
// The LoginOp operation is treated specially - it is always required if
// present in ops.
func (a *AuthChecker) AllowAny(ctxt context.Context, ops ...Op) (*AuthInfo, []bool, error) {
	authed, used, err := a.allowAny(ctxt, ops)
	return a.newAuthInfo(used), authed, err
}

func (a *AuthChecker) newAuthInfo(used []bool) *AuthInfo {
	info := &AuthInfo{
		Identity:  a.identity,
		Macaroons: make([]macaroon.Slice, 0, len(a.macaroons)),
	}
	for i, isUsed := range used {
		if isUsed {
			info.Macaroons = append(info.Macaroons, a.macaroons[i])
		}
	}
	return info
}

// allowAny is the internal version of AllowAny. Instead of returning an
// authInfo struct, it returns a slice describing which operations have
// been successfully authorized and a slice describing which macaroons
// have been used in the authorization.
func (a *AuthChecker) allowAny(ctxt context.Context, ops []Op) (authed, used []bool, err error) {
	if err := a.init(ctxt); err != nil {
		return nil, nil, errgo.Mask(err)
	}
	logger.Infof("after authorizer init, identity %#v", a.identity)
	used = make([]bool, len(a.macaroons))
	authed = make([]bool, len(ops))
	numAuthed := 0
	for i, op := range ops {
		if op == LoginOp && len(ops) > 1 {
			// LoginOp cannot be combined with other operations in the
			// same macaroon, so ignore it if it is.
			continue
		}
		for _, mindex := range a.authIndexes[op] {
			_, err := a.checkConditions(ctxt, op, a.conditions[mindex])
			if err != nil {
				logger.Infof("caveat check failed: %v", err)
				// log error?
				continue
			}
			authed[i] = true
			numAuthed++
			used[mindex] = true
			break
		}
	}
	if a.identity != nil {
		// We've authenticated as a user, so even if the operations didn't
		// specifically require it, we add the authn macaroon and its
		// conditions to the macaroons used and its con
		indexes := a.authIndexes[LoginOp]
		if len(indexes) == 0 {
			// Should never happen because init ensures it's there.
			panic("no macaroon info found for login op")
		}
		// Note: because we never issue a macaroon which combines LoginOp
		// with other operations, if the login op macaroon is used, we
		// know that it's already checked out successfully with LoginOp,
		// so no need to check again.
		used[indexes[0]] = true
	}
	if numAuthed == len(ops) {
		// All operations allowed.
		return nil, used, nil
	}
	// There are some unauthorized operations.
	need := make([]Op, 0, len(ops)-numAuthed)
	needIndex := make([]int, cap(need))
	for i, ok := range authed {
		if !ok {
			needIndex[len(need)] = i
			need = append(need, ops[i])
		}
	}
	logger.Infof("operations needed after authz macaroons: %#v", need)

	// Try to authorize the operations even even if we haven't got an authenticated user.
	oks, caveats, err := a.p.Authorizer.Authorize(ctxt, a.identity, need)
	if err != nil {
		return authed, used, errgo.Notef(err, "cannot check permissions")
	}
	if len(oks) != len(need) {
		return authed, used, errgo.Newf("unexpected slice length returned from Allow (got %d; want %d)", len(oks), len(need))
	}

	stillNeed := make([]Op, 0, len(need))
	for i, ok := range oks {
		if ok {
			authed[needIndex[i]] = true
		} else {
			stillNeed = append(stillNeed, ops[needIndex[i]])
		}
	}
	if len(stillNeed) == 0 && len(caveats) == 0 {
		// No more ops need to be authenticated and no caveats to be discharged.
		return authed, used, nil
	}
	logger.Infof("operations still needed after auth check: %#v", stillNeed)
	if a.identity == nil && len(a.identityCaveats) > 0 {
		return authed, used, &DischargeRequiredError{
			Message: "authentication required",
			Ops:     []Op{LoginOp},
			Caveats: a.identityCaveats,
		}
	}
	if len(caveats) == 0 {
		return authed, used, ErrPermissionDenied
	}
	return authed, used, &DischargeRequiredError{
		Message: "some operations have extra caveats",
		Ops:     ops,
		Caveats: caveats,
	}
}

// AllowCapability checks that the user is allowed to perform all the
// given operations. If not, the error will be as returned from Allow.
//
// If AllowCapability succeeds, it returns a list of first party caveat
// conditions that must be applied to any macaroon granting capability
// to execute the operations.
//
// If ops contains LoginOp, the user must have been authenticated with a
// macaroon associated with the single operation LoginOp only.
func (a *AuthChecker) AllowCapability(ctxt context.Context, ops ...Op) ([]string, error) {
	nops := 0
	for _, op := range ops {
		if op != LoginOp {
			nops++
		}
	}
	if nops == 0 {
		return nil, errgo.Newf("no non-login operations required in capability")
	}
	_, used, err := a.allowAny(ctxt, ops)
	if err != nil {
		logger.Infof("allowAny returned used %v; err %v", used, err)
		return nil, errgo.Mask(err, isDischargeRequiredError)
	}
	var squasher caveatSquasher
	for i, isUsed := range used {
		if !isUsed {
			continue
		}
		for _, cond := range a.conditions[i] {
			squasher.add(cond)
		}
	}
	return squasher.final(), nil
}

// caveatSquasher rationalizes first party caveats created for a capability
// by:
//	- including only the earliest time-before caveat.
//	- excluding allow and deny caveats (operations are checked by
//	virtue of the operations associated with the macaroon).
//	- removing declared caveats.
//	- removing duplicates.
type caveatSquasher struct {
	expiry time.Time
	conds  []string
}

func (c *caveatSquasher) add(cond string) {
	if c.add0(cond) {
		c.conds = append(c.conds, cond)
	}
}

func (c *caveatSquasher) add0(cond string) bool {
	cond, args, err := checkers.ParseCaveat(cond)
	if err != nil {
		// Be safe - if we can't parse the caveat, just leave it there.
		return true
	}
	switch cond {
	case checkers.CondTimeBefore:
		et, err := time.Parse(time.RFC3339Nano, args)
		if err != nil || et.IsZero() {
			// Again, if it doesn't seem valid, leave it alone.
			return true
		}
		if c.expiry.IsZero() || et.Before(c.expiry) {
			c.expiry = et
		}
		return false
	case checkers.CondAllow,
		checkers.CondDeny,
		checkers.CondDeclared:
		return false
	}
	return true
}

func (c *caveatSquasher) final() []string {
	if !c.expiry.IsZero() {
		c.conds = append(c.conds, checkers.TimeBeforeCaveat(c.expiry).Condition)
	}
	if len(c.conds) == 0 {
		return nil
	}
	// Make deterministic and eliminate duplicates.
	sort.Strings(c.conds)
	prev := c.conds[0]
	j := 1
	for _, cond := range c.conds[1:] {
		if cond != prev {
			c.conds[j] = cond
			prev = cond
			j++
		}
	}
	return c.conds
}

func (a *AuthChecker) checkConditions(ctxt context.Context, op Op, conds []string) (map[string]string, error) {
	logger.Infof("checking conditions %q", conds)
	declared := checkers.InferDeclaredFromConditions(a.Namespace(), conds)
	ctxt = checkers.ContextWithOperations(ctxt, op.Action)
	ctxt = checkers.ContextWithDeclared(ctxt, declared)
	for _, cond := range conds {
		if err := a.CheckFirstPartyCaveat(ctxt, cond); err != nil {
			return nil, errgo.Mask(err)
		}
	}
	return declared, nil
}
