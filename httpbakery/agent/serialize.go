package agent

import (
	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
)

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

// TODO add Validate method?
