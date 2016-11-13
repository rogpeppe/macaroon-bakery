package bakery

/*
import (
	"crypto/sha256"
	"fmt"
	"sort"
)

// Oven bakes macaroons. They emerge sweet and delicious
// and ready for use in Checker.
//
// All macaroons are associated with one or more operations (see
// the Op type) which define the capabilities of the macaroon.
//
// There is one special operation, "login" (defined by LoginOp)
// which grants the capability to speak for a particular user.
// The login capability will never be mixed with other capabilities.
//
// It is up to the caller to decide on semantics for all other operations.
type Oven struct {
}

type OvenParams {
	// Namespace holds the namespace to use when adding first party caveats.
	Namespace *checkers.Namespace

	// RootKeyStoreForEntity returns the macaroon storage to be
	// used for root keys associated with macaroons created
	// wth NewMacaroon.
	//
	// If this is nil, NewMemRootKeyStore will be used to create
	// a new store to be used for all entities.
	RootKeyStoreForOps func(ctxt context.Context, ops []Op) RootKeyStore

	// MultiOpStore is used to persistently store the association of
	// multi-op entities with their associated operations and
	// caveats when NewMacaroon is called with multiple operations.
	//
	// If this is nil, embed the operations will be stored directly in the macaroon id,
	// which which can make the macaroons large.
	//
	// When this is in use, operation entities with the prefix "multi-" are
	// reserved - a "multi-"-prefixed entity represents a set of operations
	// stored in the MultiOpStore.
	MultiOpStore MultiOpStore

	// Key holds the private key pair of the service. It is used to
	// decrypt user information found in third party authentication
	// declarations and to encrypt third party caveats.
	Key *bakery.KeyPair

	// Locator is used to find out information on third parties when
	// adding third party caveats.
	Locator bakery.ThirdPartyLocator

	// TODO max macaroon or macaroon id size?
}

// MacaroonOps implements MacaroonOpStore.MacaroonOps, making Oven
// an instance of MacaroonOpStore.
func (o *Oven) MacaroonOps(ctxt context.Context, ms macaroon.Slice) ([]Op, []string, error) {
}

// NewMacaroon takes a macaroon with the given version from the oven, associates it with the given operations
// and attaches the given caveats. There must be at least one operation specified.
func (o *Oven) NewMacaroon(version Version, ops []Op, caveats []checkers.Caveat) (*macaroon.Macaroon, error) {
	if len(ops) == 0 {
		return nil, errgo.Newf("cannot mint a macaroon associated with no operations")
	}
	entity = a.req.Ops[0].Entity
	for _, op := range a.req.Ops[1:] {
		if op.Entity != entity {
			entity = ""
			break
		}
	}
	if entity != "" {
		// There's only one entity involved. Target the macaroon to that
		// entity and allow only the operations specified.
		// TODO if the entity id or the number of operations is huge,
		// we should use a multi-op entity anyway.
		actions := make([]string, len(a.req.Ops))
		for i, op := range a.req.Ops {
			actions[i] = op.Action
		}
		return entity, actions, nil
	}
}

// AddCaveat adds a caveat to the given macaroon.
//
// It uses the oven's key pair and locator to call
// the AddCaveat function.
func (o *Oven) AddCaveat(m *macaroon.Macaroon, cav checkers.Caveat) error



	// Location will be set as the location of any macaroons
	// taken out of the oven.
	Location string

	// Key is the public key pair used by the service for
	// third-party caveat encryption.
	Key *KeyPair

	// Locator provides public keys for third-party services by location when
	// adding a third-party caveat.
	Locator ThirdPartyLocator

	// Namespace holds the first party caveat namespace
	// used when adding caveats.
	Namespace *checkers.Namespace

	StoreForOps func(ops
}

	// Store will be used to store macaroon
	// information locally.
	Store Storage

// multiOpEntityPrefix is the prefix used for entities that represent
// a set of operations. The actual operations
// associated with the entity will be stored in the MultiKeyStore.
const multiOpEntityPrefix = "multi"

// newMultiEntity returns a new multi-op entity name that represents
// all the given operations and caveats. It returns the same value regardless
// of the ordering of the operations. It also sorts the caveats and returns
// the operations sorted with duplicates removed.
//
// An unattenuated macaroon that has an id with a given multi-op key
// can be used to authorize any or all of the operations, assuming
// the value can be found in the MultiOpStore.
func newMultiOpEntity(ops []Op) (string, []Op) {
	sort.Sort(opsByValue(ops))
	// Hash the operations, removing duplicates as we go.
	h := sha256.New()
	var prevOp Op
	var data []byte
	j := 0
	for i, op := range ops {
		if i > 0 && op == prevOp {
			// It's a duplicate - ignore.
			continue
		}
		data = data[:0]
		data = append(data, op.Action...)
		data = append(data, '\n')
		data = append(data, op.Entity...)
		data = append(data, '\n')
		h.Write(data)
		ops[j] = op
		j++
		prevOp = op
	}

	return fmt.Sprintf("%s-%x", MultiOpEntityPrefix, h.Sum(data[:0])), ops[0:j]
}

func macaroonIdOps(ops []Op) []*authstore.Op {
	idOps := make([]authstore.Op, 0, len(ops))
	idOps = append(idOps, authstore.Op{
		Entity: ops[0].Entity,
		Action: []string{ops[0].Action},
	})
	i := 0
	idOp := &idOps[0]
	for _, op := range ops[1:] {
		if op.Entity != idOp.Entity {
			idOps = append(idOps, authstorstore.Op{
				Entity: op.Entity,
				Action: []string{op.Action},
			})
			i++
			idOp = &idOps[i]
			continue
		}
		if op.Action == idOp.Action {
			continue
		}
		idOp.Action = append(idOp.Action, op.Action)
	})
	idOpPtrs := make([]*authstore.Op, 0, len(idOps))
	for i := range idOps {
		idOpPtrs[i] = &idOps[i]
	}
	return idOpPtrs
}


type opsByValue []Op

func (o opsByValue) Less(i, j int) bool {
	o0, o1 := o[i], o[j]
	if o0.Entity != o1.Entity {
		return o0.Entity < o1.Entity
	}
	return o0.Action < o1.Action
}

func (o opsByValue) Swap(i, j int) {
	o[i], o[j] = o[j], o[i]
}

func (o opsByValue) Len() int {
	return len(o)
}
*/
