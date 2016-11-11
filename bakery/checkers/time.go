package checkers

import (
	"fmt"
	"time"

	"golang.org/x/net/context"
	"gopkg.in/errgo.v1"
	"gopkg.in/macaroon.v2-unstable"
)

// Clock represents a clock that can be faked for testing purposes.
type Clock interface {
	Now() time.Time
}

type timeKey struct{}

func ContextWithClock(ctxt context.Context, clock Clock) context.Context {
	if clock == nil {
		return ctxt
	}
	return context.WithValue(ctxt, timeKey{}, clock)
}

func clockFromContext(ctxt context.Context) Clock {
	c, _ := ctxt.Value(timeKey{}).(Clock)
	return c
}

func checkTimeBefore(ctxt context.Context, _, arg string) error {
	var now time.Time
	if clock := clockFromContext(ctxt); clock != nil {
		now = clock.Now()
	} else {
		now = time.Now()
	}
	t, err := time.Parse(time.RFC3339Nano, arg)
	if err != nil {
		return errgo.Mask(err)
	}
	if !now.Before(t) {
		return fmt.Errorf("macaroon has expired")
	}
	return nil
}

// TimeBeforeCaveat returns a caveat that specifies that
// the time that it is checked should be before t.
func TimeBeforeCaveat(t time.Time) Caveat {
	return firstParty(CondTimeBefore, t.UTC().Format(time.RFC3339Nano))
}

// ExpiryTime returns the minimum time of any time-before caveats found
// in the given slice and whether there were any such caveats found.
//
// The ns parameter is used to determine the standard namespace prefix - if
// the standard namespace is not found, the empty prefix is assumed.
func ExpiryTime(ns *Namespace, cavs []macaroon.Caveat) (time.Time, bool) {
	prefix, _ := ns.Resolve(StdNamespace)
	timeBeforeCond := WithPrefix(prefix, CondTimeBefore)
	var t time.Time
	var expires bool
	for _, cav := range cavs {
		cav := string(cav.Id)
		name, rest, _ := ParseCaveat(cav)
		if name != timeBeforeCond {
			continue
		}
		et, err := time.Parse(time.RFC3339Nano, rest)
		if err != nil {
			continue
		}
		if !expires || et.Before(t) {
			t = et
			expires = true
		}
	}
	return t, expires
}

// MacaroonsExpiryTime returns the minimum time of any time-before
// caveats found in the given macaroons and whether there were
// any such caveats found.
func MacaroonsExpiryTime(ns *Namespace, ms macaroon.Slice) (time.Time, bool) {
	var t time.Time
	var expires bool
	for _, m := range ms {
		if et, ex := ExpiryTime(ns, m.Caveats()); ex {
			if !expires || et.Before(t) {
				t = et
				expires = true
			}
		}
	}
	return t, expires
}
