package auth

import (
	errgo "gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery/checkers"
)

// DischargeRequiredError is returned when authorization has failed and a
// discharged macaroon might fix it.
//
// A caller should grant the user the ability to authorize by minting a
// macaroon associated with Ops (see MacaroonStore.MacaroonIdInfo for
// how the associated operations are retrieved) and adding Caveats. If
// the user succeeds in discharging the caveats, the authorization will
// be granted.
type DischargeRequiredError struct {
	// Message holds some reason why the authorization was denied.
	// TODO this is insufficient (and maybe unnecessary) because we
	// can have multiple errors.
	Message string

	// Ops holds all the operations that were not authorized.
	// If Ops contains a single LoginOp member, the macaroon
	// should be treated as an login token. Login tokens (also
	// known as authentication macaroons) usually have a longer
	// life span than other macaroons.
	Ops []Op

	// Caveats holds the caveats that must be added
	// to macaroons that authorize the above operations.
	Caveats []checkers.Caveat
}

func (e *DischargeRequiredError) Error() string {
	return "macaroon discharge required: " + e.Message
}

func isDischargeRequiredError(err error) bool {
	_, ok := err.(*DischargeRequiredError)
	return ok
}

type verificationError struct {
	error
}

func isVerificationError(err error) bool {
	_, ok := err.(*verificationError)
	return ok
}

var (
	ErrNotFound            = errgo.New("not found")
	ErrCaveatResultUnknown = errgo.New("caveat result not known")
)
