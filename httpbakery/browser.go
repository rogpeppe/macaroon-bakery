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

var WebBrowserWindowInteractor Interactor = webBrowserInteractor{}

type webBrowserInteractor struct{}

func (wi webBrowserInteractor) Kind() string {
	return BrowserWindowInteractionKind
}

type visitWaitParams struct {
	VisitURL string
	WaitURL  string
}

// Interact implements Interactor.Interact by opening a new web page.
func (wi webBrowserInteractor) Interact(ctx context.Context, client *Client, location string, irErr *Error) (*bakery.Macaroon, error) {
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
	if err := OpenWebBrowser(visitURL); err != nil {
		return nil, errgo.Mask(err)
	}
	return waitForMacaroon(ctx, client, waitURL)
}

// LegacyInteract implements LegacyInteractor by opening a web browser page.
func (wi webBrowserInteractor) LegacyInteract(ctx context.Context, client *Client, visitURL *url.URL) error {
	return OpenWebBrowser(visitURL)
}
