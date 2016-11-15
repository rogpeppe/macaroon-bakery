package bakery_test

import (
	"fmt"

	"golang.org/x/net/context"
	"gopkg.in/macaroon.v2-unstable"
	gc "gopkg.in/check.v1"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
	"gopkg.in/macaroon-bakery.v2-unstable/bakery/checkers"
)

var testChecker = func() *checkers.Checker {
	c := checkers.New(nil)
	c.Namespace().Register("testns", "")
	c.Register("str", "testns", strCheck)
	return c
}()

type ovenChecker struct {
	Oven    *bakery.Oven
	Checker *bakery.Checker
}

func newOvenChecker(location string, locator bakery.ThirdPartyLocator) ovenChecker {
	key, err := bakery.GenerateKey()
	if err != nil {
		panic(err)
	}
	oven := bakery.NewOven(bakery.OvenParams{
		Key:      key,
		Namespace: testChecker.Namespace(),
		Location: location,
	})
	if locator != nil {
		locator.AddInfo(location, bakery.ThirdPartyInfo{
			PublicKey: key.Public,
			Version:   bakery.LatestVersion,
		})
	}
	checker := bakery.NewChecker(bakery.CheckerParams{
		MacaroonOpStore: oven,
		IdentityClient:  noIdentities{},
		Checker: testChecker,
	})
	return ovenChecker{oven, checker}
}

func noDischarge(c *gc.C) func(macaroon.Caveat) (*macaroon.Macaroon, error) {
	return func(macaroon.Caveat) (*macaroon.Macaroon, error) {
		c.Errorf("getDischarge called unexpectedly")
		return nil, fmt.Errorf("nothing")
	}
}

type noIdentities struct{}

func (noIdentities) IdentityFromContext(ctxt context.Context) (bakery.Identity, []checkers.Caveat, error) {
	return nil, nil, nil
}

func (noIdentities) DeclaredIdentity(declared map[string]string) (bakery.Identity, error) {
	return noone{}, nil
}

type noone struct{}

func (noone) Id() string {
	return "noone"
}

func (noone) Domain() string {
	return ""
}

type strKey struct{}

func strContext(s string) context.Context {
	return context.WithValue(context.Background(), strKey{}, s)
}

func strCaveat(s string) checkers.Caveat {
	return checkers.Caveat{
		Condition: "str " + s,
		Namespace: "testns",
	}
}

// strCheck checks that the string value in the context
// matches the argument to the condition.
func strCheck(ctxt context.Context, cond, args string) error {
	expect, _ := ctxt.Value(strKey{}).(string)
	if args != expect {
		return fmt.Errorf("%s doesn't match %s", cond, expect)
	}
	return nil
}
