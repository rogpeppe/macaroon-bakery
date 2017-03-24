package httpbakery

import (
	"fmt"
	"net/url"
	"os"

	"github.com/juju/webbrowser"
	"golang.org/x/net/context"
	"gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
)

const BrowserWindowInteractionKind = "browser-window"

// OpenWebBrowser opens a web browser at the
// given URL. If the OS is not recognised, the URL
// is just printed to standard output.
func OpenWebBrowser(url *url.URL) error {
	err := webbrowser.Open(url)
	if err == nil {
		fmt.Fprintf(os.Stderr, "Opening an authorization web page in your browser.\n")
		fmt.Fprintf(os.Stderr, "If it does not open, please open this URL:\n%s\n", url)
		return nil
	}
	if err == webbrowser.ErrNoBrowser {
		fmt.Fprintf(os.Stderr, "Please open this URL in your browser to authorize:\n%s\n", url)
		return nil
	}
	return err
}

type WebBrowserInteractor struct {
	// OpenWebBrowser is used to visit a page in
	// the user's web browser. If it's nil, the
	// OpenWebBrowser function will be used.
	OpenWebBrowser func(*url.URL) error
}

func (WebBrowserInteractor) Kind() string {
	return BrowserWindowInteractionKind
}

// SetInteraction sets interaction information on the given error.
// The visitURL parameter holds a URL that should be
// visited by the user in a web browser; the waitURL parameter
// holds a URL that can be long-polled to acquire the resulting
// discharge macaroon.
func (i WebBrowserInteractor) SetInteraction(e *Error, visitURL, waitURL string) {
	e.SetInteraction(i.Kind(), visitWaitParams{
		VisitURL: visitURL,
		WaitURL:  waitURL,
	})
	// Set the visit and wait URLs for legacy clients too.
	e.Info.VisitURL = visitURL
	e.Info.WaitURL = waitURL
}

type visitWaitParams struct {
	VisitURL string
	WaitURL  string
}

// Interact implements Interactor.Interact by opening a new web page.
func (wi WebBrowserInteractor) Interact(ctx context.Context, client *Client, location string, irErr *Error) (*bakery.Macaroon, error) {
	var p visitWaitParams
	if err := irErr.InteractionMethod(wi.Kind(), &p); err != nil {
		return nil, errgo.Mask(err)
	}
	visitURL, err := relativeURL(location, irErr.Info.VisitURL)
	if err != nil {
		return nil, errgo.Notef(err, "cannot make relative visit URL")
	}
	waitURL, err := relativeURL(location, irErr.Info.WaitURL)
	if err != nil {
		return nil, errgo.Notef(err, "cannot make relative wait URL")
	}
	open := wi.OpenWebBrowser
	if open == nil {
		open = OpenWebBrowser
	}
	if err := open(visitURL); err != nil {
		return nil, errgo.Mask(err)
	}
	return waitForMacaroon(ctx, client, waitURL)
}

// LegacyInteract implements LegacyInteractor by opening a web browser page.
func (wi WebBrowserInteractor) LegacyInteract(ctx context.Context, client *Client, visitURL *url.URL) error {
	return OpenWebBrowser(visitURL)
}
