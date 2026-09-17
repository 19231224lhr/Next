package config

import (
	"bytes"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	cmted "github.com/cometbft/cometbft/crypto/ed25519"
	ct "github.com/cometbft/cometbft/types"
	"utxo/finality"
	"utxo/internal/committee"
	"utxo/internal/rules"
	"utxo/internal/state"
	"utxo/protocol"
)

type Network struct {
	Members       map[protocol.Hash][4]string
	CommitteeURLs [4]string
	ChainID       string
	GenesisTime   time.Time
	Organizations []protocol.OrgConfig
	Committee     [4]protocol.PublicKey
	Schedule      rules.Schedule
	Genesis       state.Genesis
	Accounts      []committee.GenesisAccount
}

func (n Network) Trust() (finality.Trust, error) {
	vals := make([]*ct.Validator, 4)
	for i, p := range n.Committee {
		vals[i] = ct.NewValidator(cmted.PubKey(bytes.Clone(p[:])), 1)
	}
	trust := finality.Trust{ChainID: n.ChainID, Network: protocol.Digest("NETWORK", []byte(n.ChainID)), Validators: ct.NewValidatorSet(vals)}
	if e := trust.Validate(); e != nil {
		return trust, e
	}
	if trust.Network != n.Genesis.Network {
		return trust, protocol.ErrRule
	}
	return trust, nil
}
func (n Network) Engine() committee.EngineConfig {
	return committee.EngineConfig{Network: n.Genesis.Network, Organizations: n.Organizations, Schedule: n.Schedule, Genesis: n.Genesis, Accounts: n.Accounts}
}
func (n Network) Organization(id protocol.Hash) (protocol.OrgConfig, error) {
	for _, o := range n.Organizations {
		if o.Org == id {
			return o, nil
		}
	}
	return protocol.OrgConfig{}, protocol.ErrAuth
}
func Read(path string, dst any) error {
	file, e := os.Open(path)
	if e != nil {
		return e
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 16<<20))
	decoder.DisallowUnknownFields()
	if e = decoder.Decode(dst); e != nil {
		return e
	}
	var extra any
	if e = decoder.Decode(&extra); e != io.EOF {
		return errors.New("config contains trailing data")
	}
	return nil
}
func Write(path string, value any) error {
	b, e := json.MarshalIndent(value, "", "  ")
	if e != nil {
		return e
	}
	return os.WriteFile(path, append(b, '\n'), 0600)
}
func PrivateKey(path string) (ed25519.PrivateKey, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	if len(b) != ed25519.PrivateKeySize {
		return nil, errors.New("expected a 64-byte Ed25519 private key file")
	}
	key := ed25519.PrivateKey(b)
	if !bytes.Equal(ed25519.NewKeyFromSeed(key.Seed()), key) {
		return nil, errors.New("invalid Ed25519 key")
	}
	return key, nil
}

type TLS struct{ Certificate, Key, CA string }

func (t TLS) Server(listen string) (*tls.Config, error) {
	if t.Certificate == "" && t.Key == "" && t.CA == "" {
		host, _, e := net.SplitHostPort(listen)
		if e != nil {
			return nil, e
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return nil, errors.New("non-loopback HTTP requires mutual TLS")
		}
		return nil, nil
	}
	cert, e := tls.LoadX509KeyPair(t.Certificate, t.Key)
	if e != nil {
		return nil, e
	}
	raw, e := os.ReadFile(t.CA)
	if e != nil {
		return nil, e
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(raw) {
		return nil, errors.New("invalid client CA")
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, ClientCAs: pool, ClientAuth: tls.RequireAndVerifyClientCert}, nil
}
func HTTP(listen string, handler http.Handler, t TLS) (*http.Server, error) {
	secure, e := t.Server(listen)
	if e != nil {
		return nil, e
	}
	return &http.Server{Addr: listen, Handler: handler, TLSConfig: secure, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}, nil
}
func Serve(server *http.Server) error {
	var e error
	if server.TLSConfig != nil {
		e = server.ListenAndServeTLS("", "")
	} else {
		e = server.ListenAndServe()
	}
	if errors.Is(e, http.ErrServerClosed) {
		return nil
	}
	return fmt.Errorf("HTTP service: %w", e)
}
