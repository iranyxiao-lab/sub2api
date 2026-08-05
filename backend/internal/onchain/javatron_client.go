package onchain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultJavaTronMaxAttempts  = 3
	DefaultJavaTronRetryBackoff = 100 * time.Millisecond
)

type JavaTronNodeRole string

const (
	JavaTronFullNode     JavaTronNodeRole = "full_node"
	JavaTronSolidityNode JavaTronNodeRole = "solidity_node"
)

type JavaTronErrorKind string

const (
	JavaTronErrorRequestEncode    JavaTronErrorKind = "request_encode"
	JavaTronErrorTransport        JavaTronErrorKind = "transport"
	JavaTronErrorTimeout          JavaTronErrorKind = "timeout"
	JavaTronErrorHTTP             JavaTronErrorKind = "http_status"
	JavaTronErrorResponseTooLarge JavaTronErrorKind = "response_too_large"
	JavaTronErrorDecode           JavaTronErrorKind = "response_decode"
	JavaTronErrorInvalidResponse  JavaTronErrorKind = "invalid_response"
)

type JavaTronError struct {
	Kind       JavaTronErrorKind
	Node       JavaTronNodeRole
	Endpoint   string
	StatusCode int
	Attempt    int
	Retryable  bool
	Cause      error
}

func (e *JavaTronError) Error() string {
	message := fmt.Sprintf("java-tron %s %s failed (%s, attempt %d)", e.Node, e.Endpoint, e.Kind, e.Attempt)
	if e.StatusCode != 0 {
		message += fmt.Sprintf(" with HTTP %d", e.StatusCode)
	}
	if e.Cause != nil {
		message += ": " + e.Cause.Error()
	}
	return message
}

func (e *JavaTronError) Unwrap() error { return e.Cause }

type JavaTronClientOptions struct {
	FullNodeURL      string
	SolidityNodeURL  string
	Timeout          time.Duration
	ResponseMaxBytes int64
	MaxAttempts      int
	RetryBackoff     time.Duration
	HTTPClient       *http.Client
}

type JavaTronClient struct {
	fullNodeURL      *url.URL
	solidityNodeURL  *url.URL
	timeout          time.Duration
	responseMaxBytes int64
	maxAttempts      int
	retryBackoff     time.Duration
	httpClient       *http.Client
}

func NewJavaTronClient(options JavaTronClientOptions) (*JavaTronClient, error) {
	fullNodeURL, err := parseJavaTronBaseURL(options.FullNodeURL)
	if err != nil {
		return nil, fmt.Errorf("full node URL: %w", err)
	}
	solidityNodeURL, err := parseJavaTronBaseURL(options.SolidityNodeURL)
	if err != nil {
		return nil, fmt.Errorf("solidity node URL: %w", err)
	}
	if options.Timeout <= 0 {
		return nil, fmt.Errorf("java-tron timeout must be positive")
	}
	if options.ResponseMaxBytes <= 0 {
		return nil, fmt.Errorf("java-tron response limit must be positive")
	}
	if options.MaxAttempts == 0 {
		options.MaxAttempts = DefaultJavaTronMaxAttempts
	}
	if options.MaxAttempts < 1 || options.MaxAttempts > 5 {
		return nil, fmt.Errorf("java-tron max attempts must be between 1 and 5")
	}
	if options.RetryBackoff == 0 {
		options.RetryBackoff = DefaultJavaTronRetryBackoff
	}
	if options.RetryBackoff < 0 {
		return nil, fmt.Errorf("java-tron retry backoff must be non-negative")
	}
	if options.HTTPClient == nil {
		options.HTTPClient = &http.Client{}
	}
	return &JavaTronClient{
		fullNodeURL:      fullNodeURL,
		solidityNodeURL:  solidityNodeURL,
		timeout:          options.Timeout,
		responseMaxBytes: options.ResponseMaxBytes,
		maxAttempts:      options.MaxAttempts,
		retryBackoff:     options.RetryBackoff,
		httpClient:       options.HTTPClient,
	}, nil
}

func (c *JavaTronClient) postJSON(ctx context.Context, node JavaTronNodeRole, endpoint string, request, response any) error {
	body, err := json.Marshal(request)
	if err != nil {
		return &JavaTronError{Kind: JavaTronErrorRequestEncode, Node: node, Endpoint: endpoint, Attempt: 1, Cause: err}
	}
	baseURL, err := c.nodeURL(node)
	if err != nil {
		return err
	}
	requestURL, err := url.JoinPath(baseURL.String(), endpoint)
	if err != nil {
		return &JavaTronError{Kind: JavaTronErrorRequestEncode, Node: node, Endpoint: endpoint, Attempt: 1, Cause: err}
	}

	var lastErr error
	for attempt := 1; attempt <= c.maxAttempts; attempt++ {
		lastErr = c.postJSONAttempt(ctx, node, endpoint, requestURL, body, response, attempt)
		if lastErr == nil {
			return nil
		}
		var structured *JavaTronError
		if !errors.As(lastErr, &structured) || !structured.Retryable || attempt == c.maxAttempts {
			return lastErr
		}
		if err := waitForJavaTronRetry(ctx, c.retryBackoff, attempt); err != nil {
			return err
		}
	}
	return lastErr
}

func (c *JavaTronClient) postJSONAttempt(ctx context.Context, node JavaTronNodeRole, endpoint, requestURL string, body []byte, response any, attempt int) error {
	requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return &JavaTronError{Kind: JavaTronErrorRequestEncode, Node: node, Endpoint: endpoint, Attempt: attempt, Cause: err}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		kind, retryable := classifyJavaTronTransportError(ctx, requestCtx, err)
		return &JavaTronError{Kind: kind, Node: node, Endpoint: endpoint, Attempt: attempt, Retryable: retryable, Cause: err}
	}
	defer resp.Body.Close()

	payload, readErr := io.ReadAll(io.LimitReader(resp.Body, c.responseMaxBytes+1))
	if readErr != nil {
		return &JavaTronError{Kind: JavaTronErrorTransport, Node: node, Endpoint: endpoint, StatusCode: resp.StatusCode, Attempt: attempt, Retryable: true, Cause: readErr}
	}
	if int64(len(payload)) > c.responseMaxBytes {
		return &JavaTronError{Kind: JavaTronErrorResponseTooLarge, Node: node, Endpoint: endpoint, StatusCode: resp.StatusCode, Attempt: attempt}
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return &JavaTronError{
			Kind: JavaTronErrorHTTP, Node: node, Endpoint: endpoint, StatusCode: resp.StatusCode,
			Attempt: attempt, Retryable: isRetryableJavaTronStatus(resp.StatusCode),
			Cause: errors.New(http.StatusText(resp.StatusCode)),
		}
	}
	if response == nil || len(bytes.TrimSpace(payload)) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, response); err != nil {
		return &JavaTronError{Kind: JavaTronErrorDecode, Node: node, Endpoint: endpoint, StatusCode: resp.StatusCode, Attempt: attempt, Cause: err}
	}
	return nil
}

func (c *JavaTronClient) nodeURL(role JavaTronNodeRole) (*url.URL, error) {
	switch role {
	case JavaTronFullNode:
		return c.fullNodeURL, nil
	case JavaTronSolidityNode:
		return c.solidityNodeURL, nil
	default:
		return nil, &JavaTronError{Kind: JavaTronErrorRequestEncode, Node: role, Attempt: 1, Cause: fmt.Errorf("unknown node role %q", role)}
	}
}

func parseJavaTronBaseURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("must be an absolute HTTP(S) URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("must not contain credentials, query, or fragment")
	}
	return parsed, nil
}

func classifyJavaTronTransportError(parent, request context.Context, err error) (JavaTronErrorKind, bool) {
	if parent.Err() != nil {
		return JavaTronErrorTransport, false
	}
	if errors.Is(request.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return JavaTronErrorTimeout, true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return JavaTronErrorTransport, netErr.Timeout() || netErr.Temporary()
	}
	return JavaTronErrorTransport, true
}

func isRetryableJavaTronStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooEarly || status == http.StatusTooManyRequests || status >= 500
}

func waitForJavaTronRetry(ctx context.Context, base time.Duration, attempt int) error {
	if base == 0 {
		return nil
	}
	delay := base * time.Duration(1<<(attempt-1))
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
