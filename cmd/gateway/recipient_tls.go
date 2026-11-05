package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"os"
	"time"
	cfg "utxo/cmd/internal/config"
)

func recipientHTTP(c cfg.TLS) (*http.Client, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	if c.Certificate == "" && c.Key == "" && c.CA == "" {
		return client, nil
	}
	if c.Certificate == "" || c.Key == "" || c.CA == "" {
		return nil, errors.New("RecipientTLS requires Certificate, Key and CA")
	}
	pair, e := tls.LoadX509KeyPair(c.Certificate, c.Key)
	if e != nil {
		return nil, e
	}
	b, e := os.ReadFile(c.CA)
	if e != nil {
		return nil, e
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(b) {
		return nil, errors.New("invalid recipient CA")
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{pair}}
	client.Transport = tr
	return client, nil
}
