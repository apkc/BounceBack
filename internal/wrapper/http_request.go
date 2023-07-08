package wrapper

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"

	"github.com/rs/zerolog/log"
)

// Request is a wrapper around http.Request implementing Entity interface.
// It's expected that Request.Body is already wrapped with BodyReader.
type HTTPRequest struct {
	Request *http.Request
}

func (r HTTPRequest) GetIP() netip.Addr {
	ap := netip.MustParseAddrPort(r.Request.RemoteAddr)
	return ap.Addr()
}

func (r HTTPRequest) GetRaw() ([]byte, error) {
	body, err := WrapHTTPBody(r.Request.Body)
	if err != nil {
		return nil, fmt.Errorf("can't create new body reader: %w", err)
	}

	// NOTE: httputil.DumpRequest recreates body ReaderCloser
	data, err := httputil.DumpRequest(r.Request, true)
	r.Request.Body = body
	if err != nil {
		return nil, fmt.Errorf("can't dump request: %w", err)
	}

	return data, nil
}

func (r HTTPRequest) GetBody() ([]byte, error) {
	defer r.resetBody()
	buf, err := io.ReadAll(r.Request.Body)
	if err != nil {
		return nil, fmt.Errorf("can't read body: %w", err)
	}
	return buf, nil
}

func (r HTTPRequest) GetCookies() ([]*http.Cookie, error) {
	return r.Request.Cookies(), nil
}

func (r HTTPRequest) GetHeaders() (map[string][]string, error) {
	return r.Request.Header, nil
}

func (r HTTPRequest) GetURL() (*url.URL, error) {
	return r.Request.URL, nil
}

func (r HTTPRequest) GetMethod() (string, error) {
	return r.Request.Method, nil
}

func (r HTTPRequest) resetBody() {
	if err := r.Request.Body.Close(); err != nil {
		log.Error().Err(err).Msg("Can't reset request body")
	}
}
