package httpbakery

import (
	"net/http"
	"net/url"

	"github.com/juju/httprequest"
	"golang.org/x/net/context"
	"gopkg.in/errgo.v1"
)

// TODO(rog) rename this file.

// legacyGetInteractionMethods queries a URL as found in an
// ErrInteractionRequired VisitURL field to find available interaction
// methods.
//
// It does this by sending a GET request to the URL with the Accept
// header set to "application/json" and parsing the resulting
// response as a map[string]string.
//
// It uses the given Doer to execute the HTTP GET request.
func legacyGetInteractionMethods(ctx context.Context, client httprequest.Doer, u *url.URL) (map[string]*url.URL, error) {
	httpReqClient := &httprequest.Client{
		Doer: client,
	}
	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, errgo.Notef(err, "cannot create request")
	}
	req.Header.Set("Accept", "application/json")
	var methodURLStrs map[string]string
	if err := httpReqClient.Do(ctx, req, &methodURLStrs); err != nil {
		return nil, errgo.Mask(err)
	}
	// Make all the URLs relative to the request URL.
	methodURLs := make(map[string]*url.URL)
	for m, urlStr := range methodURLStrs {
		relURL, err := url.Parse(urlStr)
		if err != nil {
			return nil, errgo.Notef(err, "invalid URL for interaction method %q", m)
		}
		methodURLs[m] = u.ResolveReference(relURL)
	}
	return methodURLs, nil
}
