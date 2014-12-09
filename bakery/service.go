// The bakery package layers on top of the macaroon package, providing
// a transport and storage-agnostic way of using macaroons to assert
// client capabilities.
//
package bakery

import (
	"crypto/rand"
	"fmt"
	"log"
	"sync"

	"gopkg.in/macaroon.v1"
)

const debug = false

func logf(f string, a ...interface{}) {
	if debug {
		log.Printf(f, a...)
	}
}

// Service represents a service which can use macaroons
// to check authorization.
type Service struct {
	location string
	store    storage
	checker  FirstPartyChecker
	encoder  *boxEncoder
	key      *KeyPair
}

// NewServiceParams holds the parameters for a NewService call.
type NewServiceParams struct {
	// Location will be set as the location of any macaroons
	// minted by the service.
	Location string

	// Store will be used to store macaroon
	// information locally. If it is nil,
	// an in-memory storage will be used.
	Store Storage

	// Key is the public key pair used by the service for
	// third-party caveat encryption.
	// It may be nil, in which case a new key pair
	// will be generated.
	Key *KeyPair

	// Locator provides public keys for third-party services by location when
	// adding a third-party caveat.
	// It may be nil, in which case, no third-party caveats can be created.
	Locator PublicKeyLocator
}

// NewService returns a new service that can mint new
// macaroons and store their associated root keys.
func NewService(p NewServiceParams) (*Service, error) {
	if p.Store == nil {
		p.Store = NewMemStorage()
	}
	svc := &Service{
		location: p.Location,
		store:    storage{p.Store},
	}

	var err error
	if p.Key == nil {
		p.Key, err = GenerateKey()
		if err != nil {
			return nil, err
		}
	}
	if p.Locator == nil {
		p.Locator = PublicKeyLocatorMap(nil)
	}
	svc.key = p.Key
	svc.encoder = newBoxEncoder(p.Locator, p.Key)
	return svc, nil
}

// Store returns the store used by the service.
func (svc *Service) Store() Storage {
	return svc.store.store
}

// Location returns the service's configured macaroon location.
func (svc *Service) Location() string {
	return svc.location
}

// PublicKey returns the service's public key.
func (svc *Service) PublicKey() *PublicKey {
	return &svc.key.Public
}

// Caveat represents a condition that must be true for a check to
// complete successfully. If Location is non-empty, the caveat must be
// discharged by a third party at the given location.
// This differs from macaroon.Caveat in that the condition
// is not encrypted.
type Caveat struct {
	Location  string
	Condition string
}

// Request represents a request made to a service
// by a client. The request may be long-lived. It holds a set
// of macaroons that the client wishes to be taken
// into account.
//
// Methods on a Request may be called concurrently
// with each other.
type Request struct {
	svc     *Service
	checker FirstPartyChecker

	// mu guards the fields following it.
	mu sync.Mutex

	// macaroons holds the set of macaroons currently associated
	// with the request.
	macaroons []*macaroon.Macaroon

	// inStorage maps from macaroon id
	// to the storage associated with that macaroon
	// for all elements in macaroons.
	inStorage map[*macaroon.Macaroon]*storageItem
}

// NewRequest returns a new client request object that uses checker to
// verify caveats.
func (svc *Service) NewRequest(checker FirstPartyChecker) *Request {
	return &Request{
		svc:       svc,
		checker:   checker,
		inStorage: make(map[*macaroon.Macaroon]*storageItem),
	}
}

// AddClientMacaroon associates the given macaroon  with
// the request. The macaroon will be taken into account when req.Check
// is called.
//
// TODO(rog) provide a way of deleting client macaroons?
func (req *Request) AddClientMacaroon(m *macaroon.Macaroon) {
	req.mu.Lock()
	defer req.mu.Unlock()

	req.macaroons = append(req.macaroons, m)
	if req.inStorage[m] != nil {
		return
	}
	// TODO(rog) perhaps defer doing this until Check time,
	// when we could fetch all the ids at once. We'd
	// want to change Storage.Get to take a slice of ids.
	item, err := req.svc.store.Get(m.Id())
	if err == ErrNotFound {
		return
	}
	if err != nil {
		log.Printf("warning: failed to read storage: %v", err)
		return
	}
	req.inStorage[m] = item
}

// Macaroons returns all the client macaroons that
// have been added to the request.
func (req *Request) Macaroons() []*macaroon.Macaroon {
	req.mu.Lock()
	defer req.mu.Unlock()
	ms := make([]*macaroon.Macaroon, len(req.macaroons))
	copy(ms, req.macaroons)
	return ms
}

// Check checks that the macaroons presented by the client verify
// correctly. If the verification fails in a way which might be
// remediable (for example by the addition of additional dicharge
// macaroons), it returns a VerificationError that describes the error.
func (req *Request) Check() error {
	req.mu.Lock()
	defer req.mu.Unlock()
	if len(req.macaroons) == 0 {
		return &VerificationError{
			Reason: fmt.Errorf("no possible macaroons found"),
		}
	}
	var anError error
	for _, m := range req.macaroons {
		item := req.inStorage[m]
		if item == nil {
			continue
		}
		err := m.Verify(item.RootKey, req.checker.CheckFirstPartyCaveat, req.macaroons)
		if err == nil {
			return nil
		}
		anError = err
	}
	return &VerificationError{
		Reason: anError,
	}
}

// NewMacaroon mints a new macaroon with the given id and caveats.
// If the id is empty, a random id will be used.
// If rootKey is nil, a random root key will be used.
// The macaroon will be stored in the service's storage.
func (svc *Service) NewMacaroon(id string, rootKey []byte, caveats []Caveat) (*macaroon.Macaroon, error) {
	if rootKey == nil {
		newRootKey, err := randomBytes(24)
		if err != nil {
			return nil, fmt.Errorf("cannot generate root key for new macaroon: %v", err)
		}
		rootKey = newRootKey
	}
	if id == "" {
		idBytes, err := randomBytes(24)
		if err != nil {
			return nil, fmt.Errorf("cannot generate id for new macaroon: %v", err)
		}
		id = fmt.Sprintf("%x", idBytes)
	}
	m, err := macaroon.New(rootKey, id, svc.location)
	if err != nil {
		return nil, fmt.Errorf("cannot bake macaroon: %v", err)
	}

	// TODO look at the caveats for expiry time and associate
	// that with the storage item so that the storage can
	// garbage collect it at an appropriate time.
	if err := svc.store.Put(m.Id(), &storageItem{
		RootKey: rootKey,
	}); err != nil {
		return nil, fmt.Errorf("cannot save macaroon to store: %v", err)
	}
	for _, cav := range caveats {
		if err := svc.AddCaveat(m, cav); err != nil {
			if err := svc.store.store.Del(m.Id()); err != nil {
				log.Printf("failed to remove macaroon from storage: %v", err)
			}
			return nil, err
		}
	}
	return m, nil
}

// AddCaveat adds a caveat to the given macaroon.
//
// If it's a third-party caveat, it uses the service's caveat-id encoder
// to create the id of the new caveat.
func (svc *Service) AddCaveat(m *macaroon.Macaroon, cav Caveat) error {
	logf("Service.AddCaveat id %q; cav %#v", m.Id(), cav)
	if cav.Location == "" {
		m.AddFirstPartyCaveat(cav.Condition)
		return nil
	}
	rootKey, err := randomBytes(24)
	if err != nil {
		return fmt.Errorf("cannot generate third party secret: %v", err)
	}
	id, err := svc.encoder.encodeCaveatId(cav, rootKey)
	if err != nil {
		return fmt.Errorf("cannot create third party caveat id at %q: %v", cav.Location, err)
	}
	if err := m.AddThirdPartyCaveat(rootKey, id, cav.Location); err != nil {
		return fmt.Errorf("cannot add third party caveat: %v", err)
	}
	return nil
}

// Discharge creates a macaroon that discharges the third party caveat with the
// given id. The id should have been created earlier by a Service.  The
// condition implicit in the id is checked for validity using checker, and
// then if valid, a new macaroon is minted which discharges the caveat, and can
// eventually be associated with a client request using AddClientMacaroon.
func (svc *Service) Discharge(checker ThirdPartyChecker, id string) (*macaroon.Macaroon, error) {
	decoder := newBoxDecoder(svc.encoder.key)

	logf("server attempting to discharge %q", id)
	rootKey, condition, err := decoder.decodeCaveatId(id)
	if err != nil {
		return nil, fmt.Errorf("discharger cannot decode caveat id: %v", err)
	}
	caveats, err := checker.CheckThirdPartyCaveat(id, condition)
	if err != nil {
		return nil, err
	}
	return svc.NewMacaroon(id, rootKey, caveats)
}

func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	if err != nil {
		return nil, fmt.Errorf("cannot generate %d random bytes: %v", n, err)
	}
	return b, nil
}

type CaveatNotRecognizedError struct {
	Caveat string
}

func (e *CaveatNotRecognizedError) Error() string {
	return fmt.Sprintf("caveat %q not recognized", e.Caveat)
}

type VerificationError struct {
	Reason error
}

func (e *VerificationError) Error() string {
	return fmt.Sprintf("verification failed: %v", e.Reason)
}

// TODO(rog) consider possible options for checkers:
// - first and third party checkers could be merged, but
// then there would have to be a runtime check
// that when used to check first-party caveats, the
// checker does not return third-party caveats.

// ThirdPartyChecker holds a function that checks
// third party caveats for validity. If the
// caveat is valid, it returns a nil error and
// optionally a slice of extra caveats that
// will be added to the discharge macaroon.
// The caveatId parameter holds the still-encoded
// id of the caveat.
//
// If the caveat kind was not recognised, the checker
// should return ErrCaveatNotRecognised.
type ThirdPartyChecker interface {
	CheckThirdPartyCaveat(caveatId, caveat string) ([]Caveat, error)
}

type ThirdPartyCheckerFunc func(caveatId, caveat string) ([]Caveat, error)

func (c ThirdPartyCheckerFunc) CheckThirdPartyCaveat(caveatId, caveat string) ([]Caveat, error) {
	return c(caveatId, caveat)
}

// FirstPartyChecker holds a function that checks
// first party caveats for validity.
//
// If the caveat kind was not recognised, the checker
// should return ErrCaveatNotRecognised.
type FirstPartyChecker interface {
	CheckFirstPartyCaveat(caveat string) error
}

type FirstPartyCheckerFunc func(caveat string) error

func (c FirstPartyCheckerFunc) CheckFirstPartyCaveat(caveat string) error {
	return c(caveat)
}
