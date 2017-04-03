package agent

/*
PROTOCOL

The agent protocol is initiated when attempting to perform a
discharge. It works as follows:

        Agent                            Login Service
          |                                    |
          | POST discharge with agent cookie   |
          |----------------------------------->|
          |                                    |
          |         Interaction Required Error |
          |     containing macaroon with local |
          |                 third-party caveat |
          |<-----------------------------------|
          |                                    |

The agent cookie is a cookie named "agent-login" holding a base64
encoded JSON object with the following structure.

{
    "username": <username>,
    "public_key": <public_key>
}

The username parameter is a string containing the username of the user
performing the login. The public_key parameter is the base64 encoded
public key of the user.

When a discharge service that supports agent authentication sees a cookie
with a username and public key that matches an agent on the service, the
interaction required response will include an "agent" interaction method.
The parameters for this interaction method will be a JSON object like
the following:

{
    "macaroon": <macaroon>
}

Where macaroon contains the discharge macaroon for the original
discharge with a "local" third-party caveat on it
using the public key specified in the agent-login cookie. Interaction
is completed by the client discharging that macaroon locally.

A local third-party caveat is a third party caveat with the location
set to "local" and the caveat encrypted with the public key declared
in the agent cookie. The httpbakery.Client automatically discharges
the local third-party caveat.

LEGACY PROTOCOL

The legacy agent protocol is used by services that don't yet
implement the new protocol. Once a discharge has
failed with an interaction required error, an agent login works
as follows:

        Agent                            Login Service
          |                                    |
          | GET visitURL with agent cookie     |
          |----------------------------------->|
          |                                    |
          |    Macaroon with local third-party |
          |                             caveat |
          |<-----------------------------------|
          |                                    |
          | GET visitURL with agent cookie &   |
          | discharged macaroon                |
          |----------------------------------->|
          |                                    |
          |               Agent login response |
          |<-----------------------------------|
          |                                    |

The agent cookie is a cookie in the same form described in the
PROTOCOL section above.

On success the response is the following JSON object:

{
    "agent_login": "true"
}

If an error occurs then the response should be a JSON object that
unmarshals to an httpbakery.Error.
*/
