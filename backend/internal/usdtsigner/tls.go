package usdtsigner

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

func LoadServerTLSConfig(cfg Config) (*tls.Config, error) {
	certificate, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load signer server certificate: %w", err)
	}
	caPEM, err := os.ReadFile(cfg.ClientCAFile)
	if err != nil {
		return nil, fmt.Errorf("read signer client CA: %w", err)
	}
	clientCAs := x509.NewCertPool()
	if !clientCAs.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("signer client CA contains no certificates")
	}
	authorized := make(map[string]struct{}, len(cfg.AuthorizedClientURIs))
	for _, identity := range cfg.AuthorizedClientURIs {
		normalized, err := parseSPIFFEIdentity(identity)
		if err != nil {
			return nil, err
		}
		authorized[normalized] = struct{}{}
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{certificate},
		ClientCAs:    clientCAs,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		NextProtos:   []string{"h2", "http/1.1"},
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 {
				return fmt.Errorf("signer client certificate was not verified")
			}
			if _, ok := authorized[certificateIdentity(state.PeerCertificates[0])]; !ok {
				return fmt.Errorf("signer client service identity is not authorized")
			}
			return nil
		},
	}, nil
}

func certificateIdentity(certificate *x509.Certificate) string {
	for _, identity := range certificate.URIs {
		if normalized, err := parseSPIFFEIdentity(identity.String()); err == nil {
			return normalized
		}
	}
	return ""
}
