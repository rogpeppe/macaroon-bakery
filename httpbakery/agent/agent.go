// Package agent enables non-interactive (agent) login using macaroons.
// To enable agent authorization with a given httpbakery.Client c against
// a given third party discharge server URL u:
//
// 	SetUpAuth(c, u, agentUsername)
//
package agent

import (
	"errors"
	"log"
	"net/url"

	"github.com/juju/httprequest"
	"github.com/juju/loggo"
	"golang.org/x/net/context"
	"gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
	"gopkg.in/macaroon-bakery.v2-unstable/httpbakery"
)

var logger = loggo.GetLogger("httpbakery.agent")

// AuthInfo holds the serialized form of a Visitor - it is
// used by the JSON and YAML marshal and unmarshal
// methods to serialize and deserialize a Visitor.
// Note that any agents with a key pair that matches
// Key will be serialized with empty keys.
type AuthInfo struct {
	Key    *bakery.KeyPair `json:"key,omitempty" yaml:"key,omitempty"`
	Agents []Agent         `json:"agents" yaml:"agents"`
}

// Agent represents an agent that can be used for agent authentication.
type Agent struct {
	// URL holds the URL associated with the agent.
	URL string `json:"url" yaml:"url"`
	// Username holds the username to use for the agent.
	Username string `json:"username" yaml:"username"`
}

// SetUpAuth sets up agent authentication on the given client,
func SetUpAuth(client *httpbakery.Client, authInfo *AuthInfo) error {
	if client.Key != nil {
		return errgo.Newf("client already has key set up")
	}
	if authInfo.Key == nil {
		return errgo.Newf("no key in auth info")
	}
	for _, agent := range authInfo.Agents {
		u, err := url.Parse(agent.URL)
		if err != nil {
			return errgo.Notef(err, "invalid URL for agent %q", agent.Username)
		}
		setCookie(client.Jar, u, agent.Username, &authInfo.Key.Public)
	}
	client.Key = authInfo.Key
	client.AddInteractor(interactor{})
	return nil
}

// InteractionParams holds the information expected in
// the agent interaction entry in an interaction-required
// error.
type InteractionParams struct {
	// Macaroon holds the discharge macaroon
	// with with a self-addressed
	// third party caveat that can be discharged to
	// discharge the original third party caveat.
	Macaroon *bakery.Macaroon
}

// interactor is a httpbakery.Interactor that performs interaction using the
// agent login protocol. A Visitor may be encoded as JSON or YAML
// so that agent information can be stored persistently.
type interactor struct{}

func (i interactor) Kind() string {
	return "agent"
}

func (i interactor) Interact(ctx context.Context, client *httpbakery.Client, location string, interactionRequiredErr *httpbakery.Error) (*bakery.Macaroon, error) {
	var p InteractionParams
	err := interactionRequiredErr.InteractionMethod("agent", &p)
	if err != nil {
		return nil, errgo.Mask(err)
	}
	// The discharge macaroon will have a local third party caveat
	// can be discharged by the usual client discharge logic.
	return p.Macaroon, nil
}

// LegacyAgentResponse contains the response to a
// legacy agent login attempt.
type LegacyAgentResponse struct {
	AgentLogin bool `json:"agent_login"`
}

func (i interactor) LegacyInteract(ctx context.Context, client *httpbakery.Client, visitURL *url.URL) error {
	log.Printf("interactor.LegacyInteract, visitURL %q; client.Key: %v {", visitURL, client.Key)
	defer log.Printf("}")
	c := &httprequest.Client{
		Doer: client,
	}
	var resp LegacyAgentResponse
	if err := c.Get(ctx, visitURL.String(), &resp); err != nil {
		return errgo.Mask(err)
	}
	if !resp.AgentLogin {
		return errors.New("agent login failed")
	}
	return nil
}
