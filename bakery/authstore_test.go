package bakery_test

import (
	"encoding/json"

	"golang.org/x/net/context"
	errgo "gopkg.in/errgo.v1"
	"gopkg.in/macaroon.v2-unstable"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
	"gopkg.in/macaroon-bakery.v2-unstable/bakery/checkers"
	"gopkg.in/macaroon-bakery.v2-unstable/httpbakery"
)

type macaroonStore struct {
	store bakery.Storage

	key *bakery.KeyPair

	locator bakery.ThirdPartyLocator
}

func newMacaroonStore() *macaroonStore {
	key, err := bakery.GenerateKey()
	if err != nil {
		panic(err)
	}
	locator := httpbakery.NewThirdPartyLocator(nil, nil)
	locator.AllowInsecure()
	return &macaroonStore{
		store:   bakery.NewMemStorage(),
		key:     key,
		locator: locator,
	}
}

type macaroonId struct {
	Id  []byte
	Ops []bakery.Op
}

func (s *macaroonStore) NewMacaroon(ops []bakery.Op, caveats []checkers.Caveat, ns *checkers.Namespace) (*macaroon.Macaroon, error) {
	rootKey, id, err := s.store.RootKey()
	if err != nil {
		return nil, errgo.Mask(err)
	}

	mid := macaroonId{
		Id:  id,
		Ops: ops,
	}
	data, _ := json.Marshal(mid)
	m, err := macaroon.New(rootKey, data, "", macaroon.LatestVersion)
	if err != nil {
		return nil, errgo.Mask(err)
	}
	for _, cav := range caveats {
		if err := bakery.AddCaveat(s.key, s.locator, m, cav, ns); err != nil {
			return nil, errgo.Notef(err, "cannot add caveat")
		}
	}
	return m, nil
}

func (s *macaroonStore) MacaroonOps(ctxt context.Context, ms macaroon.Slice) (ops []bakery.Op, conditions []string, err error) {
	if len(ms) == 0 {
		return nil, nil, errgo.Newf("no macaroons in slice")
	}
	id := ms[0].Id()
	var mid macaroonId
	if err := json.Unmarshal(id, &mid); err != nil {
		return nil, nil, errgo.Notef(err, "bad macaroon id")
	}
	rootKey, err := s.store.Get(mid.Id)
	if err != nil {
		return nil, nil, errgo.Notef(err, "cannot find root key")
	}
	conditions, err = ms[0].VerifiedConditions(rootKey, ms[1:])
	if err != nil {
		return nil, nil, errgo.Mask(err)
	}
	return mid.Ops, conditions, nil
}

func withoutLoginOp(ops []bakery.Op) []bakery.Op {
	// Remove LoginOp from the operations associated with the new macaroon.
	hasLoginOp := false
	for _, op := range ops {
		if op == bakery.LoginOp {
			hasLoginOp = true
			break
		}
	}
	if !hasLoginOp {
		return ops
	}
	newOps := make([]bakery.Op, 0, len(ops))
	for _, op := range ops {
		if op != bakery.LoginOp {
			newOps = append(newOps, op)
		}
	}
	return newOps
}
