package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Wei-Shaw/sub2api/internal/usdtsigner"
	"github.com/Wei-Shaw/sub2api/internal/usdtsigner/custody"
)

var (
	Version = "0.0.0-dev"
	Commit  = "unknown"
	Date    = "unknown"
)

func main() {
	showVersion := flag.Bool("version", false, "show version information")
	flag.Parse()
	if *showVersion {
		fmt.Printf("usdt-signer %s (commit: %s, built: %s)\n", Version, Commit, Date)
		return
	}

	cfg, err := usdtsigner.LoadConfigFromEnv()
	if err != nil {
		slog.Error("invalid signer configuration", "error", err)
		os.Exit(1)
	}
	var operations usdtsigner.Operations
	if strings.EqualFold(strings.TrimSpace(cfg.RuntimeMode), usdtsigner.RuntimeModeMock) {
		mock := usdtsigner.NewMockSigner()
		operations = usdtsigner.Operations{TRC20Sweeper: mock, ERC20GasFunder: mock, ERC20Sweeper: mock}
	} else {
		keySet, keyConfig, keyErr := loadSignerKeySet(&cfg)
		if keyErr != nil {
			slog.Error("failed to initialize signer key carriers", "error", keyErr)
			os.Exit(1)
		}
		defer keySet.Close()
		journal, journalErr := usdtsigner.OpenOperationJournal(cfg.OperationJournalFile)
		if journalErr != nil {
			slog.Error("failed to open signer operation journal", "error", journalErr)
			os.Exit(1)
		}
		defer journal.Close()
		policy, policyErr := usdtsigner.NewPolicyEngine(cfg.Policy, journal)
		if policyErr != nil {
			slog.Error("failed to initialize signer policy", "error", policyErr)
			os.Exit(1)
		}
		tronNode, tronErr := usdtsigner.NewJavaTronSignerClient(cfg.TRONOperation, nil)
		if tronErr != nil {
			slog.Error("failed to initialize signer TRON node client", "error", tronErr)
			os.Exit(1)
		}
		startupCtx, startupCancel := context.WithTimeout(context.Background(), cfg.EthereumOperation.Timeout)
		ethereumNode, ethereumErr := usdtsigner.DialEthereumSignerNode(startupCtx, cfg.EthereumOperation, nil)
		startupCancel()
		if ethereumErr != nil {
			slog.Error("failed to initialize signer Ethereum node client", "error", ethereumErr)
			os.Exit(1)
		}
		defer ethereumNode.Close()
		service, serviceErr := usdtsigner.NewService(usdtsigner.ServiceOptions{
			Keys: keySet, KeyConfig: keyConfig, TRONConfig: cfg.TRONOperation,
			EthereumConfig: cfg.EthereumOperation, TRONNode: tronNode, EthereumNode: ethereumNode,
			Policy: policy, Journal: journal,
		})
		if serviceErr != nil {
			slog.Error("failed to initialize signer operations", "error", serviceErr)
			os.Exit(1)
		}
		operations = usdtsigner.Operations{TRC20Sweeper: service, ERC20GasFunder: service, ERC20Sweeper: service}
	}
	tlsConfig, err := usdtsigner.LoadServerTLSConfig(cfg)
	if err != nil {
		slog.Error("failed to initialize signer mTLS", "error", err)
		os.Exit(1)
	}
	server, err := usdtsigner.NewHTTPServer(cfg, tlsConfig, operations)
	if err != nil {
		slog.Error("failed to initialize signer server", "error", err)
		os.Exit(1)
	}
	listener, err := net.Listen("tcp", cfg.ListenAddress)
	if err != nil {
		slog.Error("failed to bind signer private listener", "error", err)
		os.Exit(1)
	}
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.ServeTLS(listener, "", "")
	}()
	slog.Info("USDT signer boundary started", "address", cfg.ListenAddress, "deployment_mode", cfg.DeploymentMode, "runtime_mode", cfg.RuntimeMode)

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	select {
	case signal := <-signals:
		slog.Info("shutting down USDT signer", "signal", signal.String())
	case err := <-serveErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("USDT signer stopped unexpectedly", "error", err)
			os.Exit(1)
		}
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		slog.Error("USDT signer shutdown failed", "error", err)
		os.Exit(1)
	}
}

func loadSignerKeySet(cfg *usdtsigner.Config) (*custody.KeySet, custody.KeySetConfig, error) {
	mode := strings.ToLower(strings.TrimSpace(cfg.RuntimeMode))
	switch mode {
	case usdtsigner.RuntimeModeTPM:
		theTPM, err := custody.OpenTPM(cfg.TPMDevice)
		if err != nil {
			return nil, custody.KeySetConfig{}, fmt.Errorf("open signer TPM: %w", err)
		}
		keySet, loadErr := custody.LoadKeySet(cfg.KeySet, custody.TPMDataKeyUnsealer{TPM: theTPM})
		closeErr := theTPM.Close()
		if loadErr != nil {
			return nil, custody.KeySetConfig{}, loadErr
		}
		if closeErr != nil {
			keySet.Close()
			return nil, custody.KeySetConfig{}, fmt.Errorf("close signer TPM transport: %w", closeErr)
		}
		return keySet, cfg.KeySet, nil
	case usdtsigner.RuntimeModeTestSeed:
		seed, err := cfg.ConsumeTestSeed()
		if err != nil {
			return nil, custody.KeySetConfig{}, err
		}
		defer zeroBytes(seed)
		return custody.NewEphemeralTestKeySet(seed, string(cfg.TRONOperation.Network), string(cfg.EthereumOperation.Network))
	default:
		return nil, custody.KeySetConfig{}, fmt.Errorf("runtime mode %q does not use signer key carriers", cfg.RuntimeMode)
	}
}

func zeroBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
