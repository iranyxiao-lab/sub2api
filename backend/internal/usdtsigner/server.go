package usdtsigner

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"

	signerv1 "github.com/Wei-Shaw/sub2api/internal/signerapi/v1"
)

const maxRequestBytes = 4 * 1024

func NewHTTPServer(cfg Config, tlsConfig *tls.Config, operations Operations) (*http.Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if tlsConfig == nil {
		return nil, fmt.Errorf("signer TLS configuration is required")
	}
	handler, err := NewHandler(cfg, operations)
	if err != nil {
		return nil, err
	}
	return &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           handler,
		TLSConfig:         tlsConfig,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		MaxHeaderBytes:    16 * 1024,
	}, nil
}

func NewHandler(cfg Config, operations Operations) (http.Handler, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if operations.TRC20Sweeper == nil || operations.ERC20GasFunder == nil || operations.ERC20Sweeper == nil {
		return nil, fmt.Errorf("all isolated signer operation implementations are required")
	}

	authorized := make(map[string]struct{}, len(cfg.AuthorizedClientURIs))
	for _, identity := range cfg.AuthorizedClientURIs {
		normalized, _ := parseSPIFFEIdentity(identity)
		authorized[normalized] = struct{}{}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+signerv1.HealthPath, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, signerv1.HealthResponse{Status: "ok", Version: signerv1.APIVersion})
	})
	mux.HandleFunc("POST "+signerv1.SweepTRC20Path, operationHandler(
		func() requestValidator { return &signerv1.SweepTRC20Request{} },
		func(r *http.Request, request requestValidator) (*signerv1.OperationResponse, error) {
			return operations.TRC20Sweeper.SweepTRC20(r.Context(), *request.(*signerv1.SweepTRC20Request))
		},
	))
	mux.HandleFunc("POST "+signerv1.FundERC20GasPath, operationHandler(
		func() requestValidator { return &signerv1.FundERC20GasRequest{} },
		func(r *http.Request, request requestValidator) (*signerv1.OperationResponse, error) {
			return operations.ERC20GasFunder.FundERC20Gas(r.Context(), *request.(*signerv1.FundERC20GasRequest))
		},
	))
	mux.HandleFunc("POST "+signerv1.SweepERC20Path, operationHandler(
		func() requestValidator { return &signerv1.SweepERC20Request{} },
		func(r *http.Request, request requestValidator) (*signerv1.OperationResponse, error) {
			return operations.ERC20Sweeper.SweepERC20(r.Context(), *request.(*signerv1.SweepERC20Request))
		},
	))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peerIP, ok := privatePeerIP(r.RemoteAddr)
		if !ok {
			slog.Warn("signer rejected non-private peer", "peer_ip", peerIP)
			writeError(w, http.StatusForbidden, "NETWORK_FORBIDDEN", "request did not originate from the private signer network")
			return
		}
		identity := verifiedClientIdentity(r)
		if _, ok := authorized[identity]; !ok {
			slog.Warn("signer rejected unauthorized service identity", "peer_ip", peerIP, "identity", identity)
			writeError(w, http.StatusForbidden, "IDENTITY_FORBIDDEN", "client service identity is not authorized")
			return
		}
		mux.ServeHTTP(w, r)
	}), nil
}

type requestValidator interface {
	Validate() error
}

func operationHandler(
	newRequest func() requestValidator,
	execute func(*http.Request, requestValidator) (*signerv1.OperationResponse, error),
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if contentType := r.Header.Get("Content-Type"); contentType != "application/json" {
			writeError(w, http.StatusUnsupportedMediaType, "CONTENT_TYPE_INVALID", "Content-Type must be application/json")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
		request := newRequest()
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(request); err != nil {
			writeError(w, http.StatusBadRequest, "REQUEST_INVALID", "request must match the operation-specific schema")
			return
		}
		if err := ensureJSONEOF(decoder); err != nil {
			writeError(w, http.StatusBadRequest, "REQUEST_INVALID", "request must contain exactly one JSON object")
			return
		}
		if err := request.Validate(); err != nil {
			writeError(w, http.StatusBadRequest, "REQUEST_INVALID", err.Error())
			return
		}
		response, err := execute(r, request)
		if err != nil {
			if errors.Is(err, ErrOperationUnavailable) {
				writeError(w, http.StatusServiceUnavailable, "OPERATION_UNAVAILABLE", "signer operation is not available")
				return
			}
			slog.Error("signer operation failed", "error", err)
			writeError(w, http.StatusInternalServerError, "OPERATION_FAILED", "signer operation failed")
			return
		}
		if response == nil {
			writeError(w, http.StatusInternalServerError, "OPERATION_FAILED", "signer operation returned no result")
			return
		}
		response.Version = signerv1.APIVersion
		writeJSON(w, http.StatusOK, response)
	}
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("unexpected trailing JSON")
	}
	return nil
}

func privatePeerIP(remoteAddress string) (string, bool) {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddress))
	if err != nil {
		return remoteAddress, false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return host, false
	}
	return ip.String(), ip.IsPrivate() || ip.IsLoopback()
}

func verifiedClientIdentity(r *http.Request) string {
	if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 {
		return ""
	}
	return certificateIdentity(r.TLS.PeerCertificates[0])
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, signerv1.ErrorResponse{Code: code, Message: message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
