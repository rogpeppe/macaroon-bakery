package bakery

/*

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
	// used for root keys associated with the given entity name.
	//
	// If this is nil, NewMemRootKeyStore will be used to create
	// a new store to be used for all entities.
	RootKeyStoreForEntity func(entity string) RootKeyStore

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

// NewMacaroon takes a macaroon from the oven, using the given version
// and attaching the given caveats.
func (o *Oven) NewMacaroon(version Version, caveats []checkers.Caveat) (*macaroon.Macaroon, error) {
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
*/
