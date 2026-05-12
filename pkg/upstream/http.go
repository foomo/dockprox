package upstream

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"

	"github.com/foomo/dockprox/pkg/config"
	"github.com/pkg/errors"
)

// HTTPDialer dials targets through an HTTP CONNECT upstream proxy.
type HTTPDialer struct {
	name string
	url  *url.URL
	cfg  config.Upstream
}

// NewHTTP returns an HTTPDialer.
func NewHTTP(name string, u config.Upstream) (*HTTPDialer, error) {
	parsed, err := url.Parse(u.URL)
	if err != nil {
		return nil, errors.Wrap(err, "parse url")
	}

	if parsed.Host == "" {
		return nil, errors.New("url: host required")
	}

	return &HTTPDialer{name: name, url: parsed, cfg: u}, nil
}

// Name implements Dialer.
func (d *HTTPDialer) Name() string { return d.name }

// Dial implements Dialer.
func (d *HTTPDialer) Dial(ctx context.Context, hostPort string) (net.Conn, error) {
	dialer := &net.Dialer{}

	var (
		c   net.Conn
		err error
	)

	if d.url.Scheme == "https" {
		tlsCfg, e := d.tlsConfig()
		if e != nil {
			return nil, e
		}

		c, err = (&tls.Dialer{NetDialer: dialer, Config: tlsCfg}).DialContext(ctx, "tcp", d.url.Host)
	} else {
		c, err = dialer.DialContext(ctx, "tcp", d.url.Host)
	}

	if err != nil {
		return nil, errors.Wrap(err, "dial upstream")
	}

	ok := false
	defer func() {
		if !ok {
			_ = c.Close()
		}
	}()

	req := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", hostPort, hostPort)

	if d.cfg.Auth != nil {
		token := base64.StdEncoding.EncodeToString(
			[]byte(d.cfg.Auth.Username + ":" + d.cfg.Auth.Password))
		req += "Proxy-Authorization: Basic " + token + "\r\n"
	}

	req += "\r\n"
	if _, err := c.Write([]byte(req)); err != nil {
		return nil, err
	}

	resp, err := http.ReadResponse(bufio.NewReader(c), &http.Request{Method: http.MethodConnect})
	if err != nil {
		return nil, errors.Wrap(err, "read response")
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return nil, errors.Errorf("upstream CONNECT failed: %s", resp.Status)
	}

	ok = true

	return c, nil
}

func (d *HTTPDialer) tlsConfig() (*tls.Config, error) {
	cfg := &tls.Config{ServerName: d.url.Hostname()}
	if d.cfg.TLS != nil {
		cfg.InsecureSkipVerify = d.cfg.TLS.InsecureSkipVerify
		if d.cfg.TLS.CAFile != "" {
			pem, err := os.ReadFile(d.cfg.TLS.CAFile)
			if err != nil {
				return nil, errors.Wrap(err, "read ca")
			}

			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(pem) {
				return nil, errors.New("invalid CA pem")
			}

			cfg.RootCAs = pool
		}
	}

	return cfg, nil
}
