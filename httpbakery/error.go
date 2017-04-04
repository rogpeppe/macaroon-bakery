package httpbakery

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/juju/httprequest"
	"golang.org/x/net/context"
	"gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
)

// ErrorCode holds an error code that classifies
// an error returned from a bakery HTTP handler.
type ErrorCode string

func (e ErrorCode) Error() string {
	return string(e)
}

func (e ErrorCode) ErrorCode() ErrorCode {
	return e
}

const (
	ErrBadRequest                = ErrorCode("bad request")
	ErrDischargeRequired         = ErrorCode("macaroon discharge required")
	ErrInteractionRequired       = ErrorCode("interaction required")
	ErrInteractionMethodNotFound = ErrorCode("discharger does not provide an supported interaction method")
)

var (
	httpReqServer = httprequest.Server{
		ErrorMapper: ErrorToResponse,
	}
	handleJSON = httpReqServer.HandleJSON
	writeError = httpReqServer.WriteError
)

// Error holds the type of a response from an httpbakery HTTP request,
// marshaled as JSON.
//
// Note: Do not construct Error values with ErrDischargeRequired or
// ErrInteractionRequired codes directly - use the
// NewDischargeRequiredErrorForRequest or NewInteractionRequiredError
// functions instead.
type Error struct {
	Code    ErrorCode  `json:",omitempty"`
	Message string     `json:",omitempty"`
	Info    *ErrorInfo `json:",omitempty"`

	// version holds the protocol version that was used
	// to create the error (see NewDischargeRequiredErrorWithVersion).
	version bakery.Version
}

// ErrorInfo holds additional information provided
// by an error.
type ErrorInfo struct {
	// Macaroon may hold a macaroon that, when
	// discharged, may allow access to a service.
	// This field is associated with the ErrDischargeRequired
	// error code.
	Macaroon *bakery.Macaroon `json:",omitempty"`

	// MacaroonPath holds the URL path to be associated
	// with the macaroon. The macaroon is potentially
	// valid for all URLs under the given path.
	// If it is empty, the macaroon will be associated with
	// the original URL from which the error was returned.
	MacaroonPath string `json:",omitempty"`

	// CookieNameSuffix holds the desired cookie name suffix to be
	// associated with the macaroon. The actual name used will be
	// ("macaroon-" + CookieName). Clients may ignore this field -
	// older clients will always use ("macaroon-" +
	// macaroon.Signature() in hex).
	CookieNameSuffix string `json:",omitempty"`

	// The following fields are associated with the
	// ErrInteractionRequired error code.

	// InteractionMethods holds the set of methods that the
	// third party supports for completing the discharge.
	// See InteractionMethod for a more convenient
	// accessor method.
	InteractionMethods map[string]*json.RawMessage `json:",omitempty"`

	// VisitURL holds a URL that the client should visit
	// in a web browser to authenticate themselves.
	// This is deprecated - it is superceded by the InteractionMethods
	// field.
	VisitURL string `json:",omitempty"`

	// WaitURL holds a URL that the client should visit
	// to acquire the discharge macaroon. A GET on
	// this URL will block until the client has authenticated,
	// and then it will return the discharge macaroon.
	// This is deprecated - it is superceded by the InteractionMethods
	// field.
	WaitURL string `json:",omitempty"`
}

// SetInteraction sets the information for a particular
// interaction kind to x.. The error should be an interaction-required
// error. This method will panic if x cannot be JSON-marshaled.
// In general, this method should not be used directly - higher
// level SetInteraction methods implemented by specific
// implementations should be used instead.
func (e *Error) SetInteraction(kind string, x interface{}) {
	if e.Info == nil {
		e.Info = new(ErrorInfo)
	}
	if e.Info.InteractionMethods == nil {
		e.Info.InteractionMethods = make(map[string]*json.RawMessage)
	}
	data, err := json.Marshal(x)
	if err != nil {
		panic(err)
	}
	m := json.RawMessage(data)
	e.Info.InteractionMethods[kind] = &m
}

// InteractionMethod checks whether the error is an InteractionRequired error
// that implements the method with the given name, and JSON-unmarshals the
// method-specific data into x.
func (e *Error) InteractionMethod(kind string, x interface{}) error {
	if e.Info == nil || e.Code != ErrInteractionRequired {
		return errgo.Newf("not an interaction-required error (code %v)", e.Code)
	}
	var data []byte
	if e.Info.InteractionMethods == nil && e.Info.VisitURL != "" {
		// Legacy error - pretend we had the more recent
		// data and use that.
		data1, err := json.Marshal(visitWaitParams{
			VisitURL: e.Info.VisitURL,
			WaitURL:  e.Info.WaitURL,
		})
		if err != nil {
			return errgo.Mask(err)
		}
		data = data1
	} else if m := e.Info.InteractionMethods[kind]; m != nil {
		data = *m
	} else {
		return errgo.WithCausef(nil, ErrInteractionMethodNotFound, "interaction method %q not found", kind)
	}
	if err := json.Unmarshal(data, x); err != nil {
		return errgo.Notef(err, "cannot unmarshal data for interaction method %q", kind)
	}
	return nil
}

func (e *Error) Error() string {
	return e.Message
}

func (e *Error) ErrorCode() ErrorCode {
	return e.Code
}

// ErrorInfo returns additional information
// about the error.
// TODO return interface{} here?
func (e *Error) ErrorInfo() *ErrorInfo {
	return e.Info
}

// ErrorToResponse returns the HTTP status and an error body to be
// marshaled as JSON for the given error. This allows a third party
// package to integrate bakery errors into their error responses when
// they encounter an error with a *bakery.Error cause.
func ErrorToResponse(ctx context.Context, err error) (int, interface{}) {
	errorBody := errorResponseBody(err)
	var body interface{} = errorBody
	status := http.StatusInternalServerError
	switch errorBody.Code {
	case ErrBadRequest:
		status = http.StatusBadRequest
	case ErrDischargeRequired, ErrInteractionRequired:
		switch errorBody.version {
		case bakery.Version0:
			status = http.StatusProxyAuthRequired
		case bakery.Version1, bakery.Version2, bakery.Version3:
			status = http.StatusUnauthorized
			body = httprequest.CustomHeader{
				Body:          body,
				SetHeaderFunc: setAuthenticateHeader,
			}
		default:
			panic(fmt.Sprintf("out of range version number %v", errorBody.version))
		}
	}
	return status, body
}

func setAuthenticateHeader(h http.Header) {
	h.Set("WWW-Authenticate", "Macaroon")
}

type errorInfoer interface {
	ErrorInfo() *ErrorInfo
}

type errorCoder interface {
	ErrorCode() ErrorCode
}

// errorResponse returns an appropriate error
// response for the provided error.
func errorResponseBody(err error) *Error {
	var errResp Error
	cause := errgo.Cause(err)
	if cause, ok := cause.(*Error); ok {
		// It's an Error already. Preserve the wrapped
		// error message but copy everything else.
		errResp = *cause
		errResp.Message = err.Error()
		return &errResp
	}
	// It's not an error. Preserve as much info as
	// we can find.
	errResp.Message = err.Error()
	if coder, ok := cause.(errorCoder); ok {
		errResp.Code = coder.ErrorCode()
	}
	if infoer, ok := cause.(errorInfoer); ok {
		errResp.Info = infoer.ErrorInfo()
	}
	return &errResp
}

func badRequestErrorf(f string, a ...interface{}) error {
	return errgo.WithCausef(nil, ErrBadRequest, f, a...)
}

// WriteDischargeRequiredError creates an error using
// NewDischargeRequiredError and writes it to the given response writer,
// indicating that the client should discharge the macaroon to allow the
// original request to be accepted.
func WriteDischargeRequiredError(w http.ResponseWriter, m *bakery.Macaroon, path string, originalErr error) {
	writeError(context.Background(), w, NewDischargeRequiredError(m, path, originalErr))
}

// WriteDischargeRequiredErrorForRequest is like NewDischargeRequiredError
// but uses the given request to determine the protocol version appropriate
// for the client.
//
// This function should always be used in preference to
// WriteDischargeRequiredError, because it enables
// in-browser macaroon discharge.
func WriteDischargeRequiredErrorForRequest(w http.ResponseWriter, m *bakery.Macaroon, path string, originalErr error, req *http.Request) {
	writeError(context.Background(), w, NewDischargeRequiredErrorForRequest(m, path, originalErr, req))
}

// NewDischargeRequiredError returns an error of type *Error that
// reports the given original error and includes the given macaroon.
//
// The returned macaroon will be declared as valid for the given URL
// path and may be relative. When the client stores the discharged
// macaroon as a cookie this will be the path associated with the
// cookie. See ErrorInfo.MacaroonPath for more information.
func NewDischargeRequiredError(m *bakery.Macaroon, path string, originalErr error) error {
	return NewDischargeRequiredErrorWithVersion(m, path, originalErr, bakery.Version0)
}

// NewInteractionRequiredError returns an error of type *Error
// that requests an interaction from the client in response
// to the given request. The originalErr value describes the original
// error - if it is nil, a default message will be provided.
//
// This function should be used in preference to creating the Error value
// directly, as it sets the bakery protocol version correctly in the error.
//
// The returned error does not support any interaction kinds.
// Use kind-specific SetInteraction methods (for example
// WebBrowserInteractor.SetInteraction) to add supported
// interaction kinds.
//
// Note that WebBrowserInteractor.SetInteraction should always be called
// for legacy clients to maintain backwards compatibility.
func NewInteractionRequiredError(originalErr error, req *http.Request) *Error {
	if originalErr == nil {
		originalErr = ErrInteractionRequired
	}
	return &Error{
		Message: originalErr.Error(),
		version: RequestVersion(req),
		Code:    ErrInteractionRequired,
	}
}

// NewDischargeRequiredErrorForRequest is like NewDischargeRequiredError
// except that it determines the client's bakery protocol version from
// the request and returns an error response appropriate for that.
//
// This function should always be used in preference to
// NewDischargeRequiredError, because it enables in-browser macaroon
// discharge.
//
// To request a particular cookie name:
//
//	err := NewDischargeRequiredErrorForRequest(...)
//	err.(*httpbakery.Error).Info.CookieNameSuffix = cookieName
func NewDischargeRequiredErrorForRequest(m *bakery.Macaroon, path string, originalErr error, req *http.Request) error {
	v := RequestVersion(req)
	return NewDischargeRequiredErrorWithVersion(m, path, originalErr, v)
}

// NewDischargeRequiredErrorWithVersion is like NewDischargeRequiredErrorForRequest
// except that instead of inferring the client version from
// the request, the version is explicit.
func NewDischargeRequiredErrorWithVersion(m *bakery.Macaroon, path string, originalErr error, v bakery.Version) error {
	if originalErr == nil {
		originalErr = ErrDischargeRequired
	}
	logger.Infof("creating discharge-required error for version %v", v)
	return &Error{
		Message: originalErr.Error(),
		version: v,
		Code:    ErrDischargeRequired,
		Info: &ErrorInfo{
			Macaroon:     m,
			MacaroonPath: path,
		},
	}
}

// BakeryProtocolHeader is the header that HTTP clients should set
// to determine the bakery protocol version. If it is 0 or missing,
// a discharge-required error response will be returned with HTTP status 407;
// if it is 1, the response will have status 401 with the WWW-Authenticate
// header set to "Macaroon".
const BakeryProtocolHeader = "Bakery-Protocol-Version"

// RequestVersion determines the bakery protocol version from a client
// request. If the protocol cannot be determined, or is invalid, the
// original version of the protocol is used. If a later version is
// found, the latest known version is used, which is OK because versions
// are backwardly compatible.
//
// TODO as there are no known version 0 clients, default to version 1
// instead.
func RequestVersion(req *http.Request) bakery.Version {
	vs := req.Header.Get(BakeryProtocolHeader)
	if vs == "" {
		// No header - use backward compatibility mode.
		return bakery.Version0
	}
	x, err := strconv.Atoi(vs)
	if err != nil || x < 0 {
		// Badly formed header - use backward compatibility mode.
		return bakery.Version0
	}
	v := bakery.Version(x)
	if v > bakery.LatestVersion {
		// Later version than we know about - use the
		// latest version that we can.
		return bakery.LatestVersion
	}
	return v
}

func isDischargeRequiredError(err error) bool {
	respErr, ok := errgo.Cause(err).(*Error)
	if !ok {
		return false
	}
	return respErr.Code == ErrDischargeRequired
}
