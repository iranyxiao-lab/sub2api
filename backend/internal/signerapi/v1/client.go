package signerv1

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const maxResponseBytes = 64 * 1024

type ClientConfig struct {
	BaseURL           string
	ClientCertFile    string
	ClientKeyFile     string
	ServerCAFile      string
	ServerIdentityURI string
	Timeout           time.Duration
}

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(cfg ClientConfig) (*Client, error) {
	baseURL, err := url.Parse(strings.TrimSpace(cfg.BaseURL))
	if err != nil || baseURL.Scheme != "https" || baseURL.Host == "" {
		return nil, fmt.Errorf("signer base URL must be an absolute HTTPS URL")
	}
	if baseURL.User != nil || baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return nil, fmt.Errorf("signer base URL must not contain credentials, query, or fragment")
	}
	if baseURL.Path != "" && baseURL.Path != "/" {
		return nil, fmt.Errorf("signer base URL must not contain a path")
	}

	tlsConfig, err := loadClientTLSConfig(cfg, baseURL.Hostname())
	if err != nil {
		return nil, err
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	transport := &http.Transport{
		Proxy:             nil,
		TLSClientConfig:   tlsConfig,
		ForceAttemptHTTP2: true,
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL.String(), "/"),
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   timeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return errors.New("signer redirects are forbidden")
			},
		},
	}, nil
}

func (c *Client) SweepTRC20(ctx context.Context, request SweepTRC20Request) (*OperationResponse, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	return c.do(ctx, SweepTRC20Path, request)
}

func (c *Client) FundERC20Gas(ctx context.Context, request FundERC20GasRequest) (*OperationResponse, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	return c.do(ctx, FundERC20GasPath, request)
}

func (c *Client) SweepERC20(ctx context.Context, request SweepERC20Request) (*OperationResponse, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	return c.do(ctx, SweepERC20Path, request)
}

func (c *Client) Health(ctx context.Context) (*HealthResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+HealthPath, nil)
	if err != nil {
		return nil, fmt.Errorf("create signer health request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	payload, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	var result HealthResponse
	if err := json.Unmarshal(payload, &result); err != nil {
		return nil, fmt.Errorf("decode signer health response: %w", err)
	}
	if result.Version != APIVersion {
		return nil, fmt.Errorf("unexpected signer protocol version %q", result.Version)
	}
	if result.Status != "ok" {
		return nil, fmt.Errorf("signer reported unhealthy status %q", result.Status)
	}
	return &result, nil
}

func (c *Client) do(ctx context.Context, path string, request any) (*OperationResponse, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode signer request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create signer request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	payload, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	var result OperationResponse
	if err := json.Unmarshal(payload, &result); err != nil {
		return nil, fmt.Errorf("decode signer response: %w", err)
	}
	if result.Version != APIVersion {
		return nil, fmt.Errorf("unexpected signer protocol version %q", result.Version)
	}
	return &result, nil
}

func (c *Client) doRequest(req *http.Request) ([]byte, error) {
	response, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call signer: %w", err)
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, maxResponseBytes+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read signer response: %w", err)
	}
	if len(payload) > maxResponseBytes {
		return nil, fmt.Errorf("signer response exceeds %d bytes", maxResponseBytes)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var signerError ErrorResponse
		if json.Unmarshal(payload, &signerError) == nil && signerError.Code != "" {
			return nil, fmt.Errorf("signer rejected request: %s: %s", signerError.Code, signerError.Message)
		}
		return nil, fmt.Errorf("signer returned HTTP %d", response.StatusCode)
	}
	return payload, nil
}

func loadClientTLSConfig(cfg ClientConfig, serverName string) (*tls.Config, error) {
	if strings.TrimSpace(cfg.ClientCertFile) == "" || strings.TrimSpace(cfg.ClientKeyFile) == "" || strings.TrimSpace(cfg.ServerCAFile) == "" {
		return nil, fmt.Errorf("client certificate, key, and server CA files are required")
	}
	expectedURI, err := parseIdentityURI(cfg.ServerIdentityURI)
	if err != nil {
		return nil, fmt.Errorf("server identity URI: %w", err)
	}
	certificate, err := tls.LoadX509KeyPair(cfg.ClientCertFile, cfg.ClientKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load signer client certificate: %w", err)
	}
	caPEM, err := os.ReadFile(cfg.ServerCAFile)
	if err != nil {
		return nil, fmt.Errorf("read signer server CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("signer server CA contains no certificates")
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		ServerName:   serverName,
		RootCAs:      roots,
		Certificates: []tls.Certificate{certificate},
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 {
				return fmt.Errorf("signer server certificate was not verified")
			}
			if !certificateHasURI(state.PeerCertificates[0], expectedURI) {
				return fmt.Errorf("signer server identity is not authorized")
			}
			return nil
		},
	}, nil
}

func parseIdentityURI(raw string) (*url.URL, error) {
	identity, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || identity.Scheme != "spiffe" || identity.Host == "" || identity.Path == "" {
		return nil, fmt.Errorf("must be an absolute spiffe URI")
	}
	if identity.User != nil || identity.RawQuery != "" || identity.Fragment != "" {
		return nil, fmt.Errorf("must not contain credentials, query, or fragment")
	}
	return identity, nil
}

func certificateHasURI(certificate *x509.Certificate, expected *url.URL) bool {
	for _, identity := range certificate.URIs {
		if identity.String() == expected.String() {
			return true
		}
	}
	return false
}
