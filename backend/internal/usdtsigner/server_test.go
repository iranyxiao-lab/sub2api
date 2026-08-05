package usdtsigner_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	signerv1 "github.com/Wei-Shaw/sub2api/internal/signerapi/v1"
	"github.com/Wei-Shaw/sub2api/internal/usdtsigner"
	"github.com/Wei-Shaw/sub2api/internal/usdtsigner/custody"
	"github.com/stretchr/testify/require"
)

const (
	serverIdentity       = "spiffe://sub2api.internal/service/usdt-signer"
	authorizedIdentity   = "spiffe://sub2api.internal/workload/sweep-worker"
	unauthorizedIdentity = "spiffe://sub2api.internal/workload/admin-api"
)

type fakeOperations struct {
	tronCalls atomic.Int32
	gasCalls  atomic.Int32
	ercCalls  atomic.Int32
}

func (f *fakeOperations) SweepTRC20(_ context.Context, request signerv1.SweepTRC20Request) (*signerv1.OperationResponse, error) {
	f.tronCalls.Add(1)
	return successResponse(request.TaskID, request.IdempotencyKey), nil
}

func (f *fakeOperations) FundERC20Gas(_ context.Context, request signerv1.FundERC20GasRequest) (*signerv1.OperationResponse, error) {
	f.gasCalls.Add(1)
	return successResponse(request.TaskID, request.IdempotencyKey), nil
}

func (f *fakeOperations) SweepERC20(_ context.Context, request signerv1.SweepERC20Request) (*signerv1.OperationResponse, error) {
	f.ercCalls.Add(1)
	return successResponse(request.TaskID, request.IdempotencyKey), nil
}

func successResponse(taskID, idempotencyKey string) *signerv1.OperationResponse {
	return &signerv1.OperationResponse{
		TaskID: taskID, IdempotencyKey: idempotencyKey, Status: "accepted", AuditID: "audit-1",
	}
}

func TestHandlerRejectsInjectedTransactionFields(t *testing.T) {
	operations := &fakeOperations{}
	handler := newAuthorizedHandler(t, operations)
	for _, injectedField := range []string{"network", "contract", "destination", "method", "calldata", "raw_transaction"} {
		t.Run(injectedField, func(t *testing.T) {
			body := `{"task_id":"sweep-1","derivation_index":1,"raw_amount":"50000000","idempotency_key":"sweep:1","` + injectedField + `":"injected"}`
			request := authorizedRequest(t, http.MethodPost, signerv1.SweepTRC20Path, body, authorizedIdentity)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			require.Equal(t, http.StatusBadRequest, response.Code)
		})
	}
	require.Zero(t, operations.tronCalls.Load())
}

func TestHandlerExposesOnlyVersionedHighLevelRoutes(t *testing.T) {
	operations := &fakeOperations{}
	handler := newAuthorizedHandler(t, operations)

	tests := []struct {
		path string
		body string
	}{
		{signerv1.SweepTRC20Path, `{"task_id":"tron-1","derivation_index":1,"raw_amount":"50000000","idempotency_key":"tron:1"}`},
		{signerv1.FundERC20GasPath, `{"task_id":"gas-1","derivation_index":2,"raw_amount":"1000000000000000","idempotency_key":"gas:1"}`},
		{signerv1.SweepERC20Path, `{"task_id":"erc-1","derivation_index":3,"raw_amount":"200000000","idempotency_key":"erc:1"}`},
	}
	for _, test := range tests {
		request := authorizedRequest(t, http.MethodPost, test.path, test.body, authorizedIdentity)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		require.Equal(t, http.StatusOK, response.Code, test.path)
	}
	require.EqualValues(t, 1, operations.tronCalls.Load())
	require.EqualValues(t, 1, operations.gasCalls.Load())
	require.EqualValues(t, 1, operations.ercCalls.Load())

	for _, forbiddenPath := range []string{
		"/sign", "/admin", "/api/v1/sign", "/internal/usdt-signer/v1/sign", "/internal/usdt-signer/v1/raw-transaction",
	} {
		request := authorizedRequest(t, http.MethodPost, forbiddenPath, `{}`, authorizedIdentity)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		require.Equal(t, http.StatusNotFound, response.Code, forbiddenPath)
	}
}

func TestHandlerRejectsPublicPeersAndUnauthorizedIdentities(t *testing.T) {
	operations := &fakeOperations{}
	handler := newAuthorizedHandler(t, operations)

	publicRequest := authorizedRequest(t, http.MethodPost, signerv1.SweepTRC20Path, `{}`, authorizedIdentity)
	publicRequest.RemoteAddr = "203.0.113.5:4242"
	publicResponse := httptest.NewRecorder()
	handler.ServeHTTP(publicResponse, publicRequest)
	require.Equal(t, http.StatusForbidden, publicResponse.Code)

	unauthorizedRequest := authorizedRequest(t, http.MethodPost, signerv1.SweepTRC20Path, `{}`, unauthorizedIdentity)
	unauthorizedResponse := httptest.NewRecorder()
	handler.ServeHTTP(unauthorizedResponse, unauthorizedRequest)
	require.Equal(t, http.StatusForbidden, unauthorizedResponse.Code)
	require.Zero(t, operations.tronCalls.Load())
}

func TestMutualTLSAuthenticatesBothServiceIdentities(t *testing.T) {
	files := createPKI(t)
	cfg := testConfig(files.serverCert, files.serverKey, files.caCert)
	operations := &fakeOperations{}
	handler, err := usdtsigner.NewHandler(cfg, usdtsigner.Operations{
		TRC20Sweeper: operations, ERC20GasFunder: operations, ERC20Sweeper: operations,
	})
	require.NoError(t, err)
	serverTLS, err := usdtsigner.LoadServerTLSConfig(cfg)
	require.NoError(t, err)

	server := httptest.NewUnstartedServer(handler)
	server.TLS = serverTLS
	server.StartTLS()
	t.Cleanup(server.Close)

	client, err := signerv1.NewClient(signerv1.ClientConfig{
		BaseURL:           server.URL,
		ClientCertFile:    files.clientCert,
		ClientKeyFile:     files.clientKey,
		ServerCAFile:      files.caCert,
		ServerIdentityURI: serverIdentity,
	})
	require.NoError(t, err)
	result, err := client.SweepTRC20(context.Background(), signerv1.SweepTRC20Request{
		TaskID: "tron-1", DerivationIndex: 1, RawAmount: "50000000", IdempotencyKey: "tron:1",
	})
	require.NoError(t, err)
	require.Equal(t, signerv1.APIVersion, result.Version)
	require.EqualValues(t, 1, operations.tronCalls.Load())

	unauthorizedClient, err := signerv1.NewClient(signerv1.ClientConfig{
		BaseURL:           server.URL,
		ClientCertFile:    files.unauthorizedCert,
		ClientKeyFile:     files.unauthorizedKey,
		ServerCAFile:      files.caCert,
		ServerIdentityURI: serverIdentity,
	})
	require.NoError(t, err)
	_, err = unauthorizedClient.SweepTRC20(context.Background(), signerv1.SweepTRC20Request{
		TaskID: "tron-2", DerivationIndex: 2, RawAmount: "50000000", IdempotencyKey: "tron:2",
	})
	require.ErrorContains(t, err, "call signer")

	wrongServerIdentityClient, err := signerv1.NewClient(signerv1.ClientConfig{
		BaseURL:           server.URL,
		ClientCertFile:    files.clientCert,
		ClientKeyFile:     files.clientKey,
		ServerCAFile:      files.caCert,
		ServerIdentityURI: "spiffe://sub2api.internal/service/not-the-signer",
	})
	require.NoError(t, err)
	_, err = wrongServerIdentityClient.SweepTRC20(context.Background(), signerv1.SweepTRC20Request{
		TaskID: "tron-3", DerivationIndex: 3, RawAmount: "50000000", IdempotencyKey: "tron:3",
	})
	require.ErrorContains(t, err, "call signer")
}

func newAuthorizedHandler(t *testing.T, operations *fakeOperations) http.Handler {
	t.Helper()
	cfg := testConfig("server.crt", "server.key", "client-ca.crt")
	handler, err := usdtsigner.NewHandler(cfg, usdtsigner.Operations{
		TRC20Sweeper: operations, ERC20GasFunder: operations, ERC20Sweeper: operations,
	})
	require.NoError(t, err)
	return handler
}

func testConfig(serverCert, serverKey, clientCA string) usdtsigner.Config {
	return usdtsigner.Config{
		Environment:          usdtsigner.EnvironmentProduction,
		DeploymentMode:       usdtsigner.DeploymentModeStandalone,
		ListenAddress:        "127.0.0.1:9443",
		TLSCertFile:          serverCert,
		TLSKeyFile:           serverKey,
		ClientCAFile:         clientCA,
		AuthorizedClientURIs: []string{authorizedIdentity},
		ReadHeaderTimeout:    5 * time.Second,
		IdleTimeout:          30 * time.Second,
		ShutdownTimeout:      10 * time.Second,
		TPMDevice:            "windows-tbs",
		KeySet: custody.KeySetConfig{
			TRONRecharge: custody.CarrierConfig{
				SealedKeyFile: "tron-sealed.json", EncryptedKeyFile: "tron-encrypted.json",
				KeyID: "00112233445566778899aabbccddeeff", Role: custody.KeyRoleTRONRecharge,
				Network: "tron-mainnet", MaterialType: custody.MaterialBIP32Seed,
				WalletFingerprint: "sha256:00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff",
			},
			EthereumRecharge: custody.CarrierConfig{
				SealedKeyFile: "ethereum-sealed.json", EncryptedKeyFile: "ethereum-encrypted.json",
				KeyID: "11112233445566778899aabbccddeeff", Role: custody.KeyRoleEthereumRecharge,
				Network: "ethereum-mainnet", MaterialType: custody.MaterialBIP32Seed,
				WalletFingerprint: "sha256:11112233445566778899aabbccddeeff00112233445566778899aabbccddeeff",
			},
			EthereumGas: custody.CarrierConfig{
				SealedKeyFile: "gas-sealed.json", EncryptedKeyFile: "gas-encrypted.json",
				KeyID: "22112233445566778899aabbccddeeff", Role: custody.KeyRoleEthereumGas,
				Network: "ethereum-mainnet", MaterialType: custody.MaterialSecp256k1PrivateKey,
				WalletFingerprint: "sha256:22112233445566778899aabbccddeeff00112233445566778899aabbccddeeff",
			},
		},
	}
}

func authorizedRequest(t *testing.T, method, path, body, identity string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(method, "https://signer.internal"+path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.RemoteAddr = "10.0.0.8:4242"
	identityURL, err := url.Parse(identity)
	require.NoError(t, err)
	certificate := &x509.Certificate{URIs: []*url.URL{identityURL}}
	request.TLS = &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{certificate},
		VerifiedChains:   [][]*x509.Certificate{{certificate}},
	}
	return request
}

type pkiFiles struct {
	caCert           string
	serverCert       string
	serverKey        string
	clientCert       string
	clientKey        string
	unauthorizedCert string
	unauthorizedKey  string
}

func createPKI(t *testing.T) pkiFiles {
	t.Helper()
	directory := t.TempDir()
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "sub2api signer test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	require.NoError(t, err)
	caCertificate, err := x509.ParseCertificate(caDER)
	require.NoError(t, err)
	caPath := filepath.Join(directory, "ca.pem")
	writePEM(t, caPath, "CERTIFICATE", caDER)

	serverCert, serverKey := createLeaf(t, directory, "server", 2, caCertificate, caKey, serverIdentity, x509.ExtKeyUsageServerAuth, true)
	clientCert, clientKey := createLeaf(t, directory, "client", 3, caCertificate, caKey, authorizedIdentity, x509.ExtKeyUsageClientAuth, false)
	unauthorizedCert, unauthorizedKey := createLeaf(t, directory, "unauthorized", 4, caCertificate, caKey, unauthorizedIdentity, x509.ExtKeyUsageClientAuth, false)
	return pkiFiles{
		caCert: caPath, serverCert: serverCert, serverKey: serverKey,
		clientCert: clientCert, clientKey: clientKey,
		unauthorizedCert: unauthorizedCert, unauthorizedKey: unauthorizedKey,
	}
}

func createLeaf(
	t *testing.T,
	directory, name string,
	serial int64,
	ca *x509.Certificate,
	caKey *rsa.PrivateKey,
	identity string,
	usage x509.ExtKeyUsage,
	server bool,
) (string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	identityURL, err := url.Parse(identity)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
		URIs:         []*url.URL{identityURL},
	}
	if server {
		template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	require.NoError(t, err)
	certPath := filepath.Join(directory, name+".pem")
	keyPath := filepath.Join(directory, name+"-key.pem")
	writePEM(t, certPath, "CERTIFICATE", der)
	writePEM(t, keyPath, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key))
	return certPath, keyPath
}

func writePEM(t *testing.T, path, blockType string, bytes []byte) {
	t.Helper()
	encoded := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: bytes})
	require.NoError(t, os.WriteFile(path, encoded, 0o600))
}

func decodeError(t *testing.T, response *httptest.ResponseRecorder) signerv1.ErrorResponse {
	t.Helper()
	var result signerv1.ErrorResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	return result
}
