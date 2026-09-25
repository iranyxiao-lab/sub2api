//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type lifecycleResultRepo struct {
	ScheduledTestResultRepository
	enqueued  chan *IntelligenceRun
	completed chan *ScheduledTestResult
}

func (r *lifecycleResultRepo) EnqueueIntelligenceRun(_ context.Context, id int64, _, _ string, _ *time.Time, timeoutSeconds int) (*IntelligenceRun, error) {
	run := &IntelligenceRun{ScheduledTestResult: ScheduledTestResult{ID: 1, PlanID: id, Status: "queued", ModelSnapshot: "gpt-5.4", PromptSnapshot: "Write HTML"}, AccountID: 1, MaxResults: 10, ExecutionTimeoutSeconds: timeoutSeconds}
	r.enqueued <- run
	return run, nil
}
func (r *lifecycleResultRepo) GetIntelligenceRun(context.Context, int64, int64) (*IntelligenceRun, error) {
	return &IntelligenceRun{ScheduledTestResult: ScheduledTestResult{ID: 1, Status: "running"}}, nil
}
func (r *lifecycleResultRepo) CompleteIntelligenceRun(ctx context.Context, _ *IntelligenceRun, result *ScheduledTestResult) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.completed <- result
	return nil
}
func (*lifecycleResultRepo) PruneOldResults(context.Context, int64, int) error { return nil }

type delayedIntelligenceUpstream struct {
	started            chan context.Context
	delay              time.Duration
	text               string
	cancelAfterPartial bool
}

func (u *delayedIntelligenceUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.respond(req)
}
func (u *delayedIntelligenceUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.respond(req)
}
func (u *delayedIntelligenceUpstream) respond(req *http.Request) (*http.Response, error) {
	r, w := io.Pipe()
	u.started <- req.Context()
	go func() {
		defer w.Close()
		// Responses SSE is parsed by the real account-test service.
		_, _ = io.WriteString(w, `data: {"type":"response.output_text.delta","delta":"`+u.text+`"}`+"\n\n")
		timer := time.NewTimer(u.delay)
		defer timer.Stop()
		select {
		case <-req.Context().Done():
			_ = w.CloseWithError(req.Context().Err())
		case <-timer.C:
			if u.cancelAfterPartial {
				_ = w.CloseWithError(context.Canceled)
				return
			}
			_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\"}\n\n")
		}
	}()
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: r}, nil
}

func intelligenceLifecycleService(upstream *delayedIntelligenceUpstream) (*ScheduledTestService, *lifecycleResultRepo) {
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Concurrency: 1, Credentials: map[string]any{"access_token": "test-only"}}
	repo := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{1: account}}}
	results := &lifecycleResultRepo{enqueued: make(chan *IntelligenceRun, 1), completed: make(chan *ScheduledTestResult, 1)}
	return &ScheduledTestService{resultRepo: results, accountTest: &AccountTestService{accountRepo: repo, httpUpstream: upstream, cfg: rawChatCompletionsTestConfig()}}, results
}

func TestIntelligenceRunSurvivesBrowserCancellationBeyond30Seconds(t *testing.T) {
	u := &delayedIntelligenceUpstream{started: make(chan context.Context, 1), delay: 31 * time.Second, text: "<h1>answer</h1>"}
	svc, repo := intelligenceLifecycleService(u)
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest()
	waitDone := make(chan error, 1)
	go func() {
		_, err := svc.waitForRun(requestCtx, &ScheduledTestPlan{ID: 1, TestKind: "intelligence"})
		waitDone <- err
	}()
	run := <-repo.enqueued
	workerCtx, stopWorker := context.WithCancel(context.Background())
	defer stopWorker()
	go svc.executeIntelligenceRun(workerCtx, run)
	upstreamCtx := <-u.started
	cancelRequest()
	require.ErrorIs(t, <-waitDone, context.Canceled)
	require.NoError(t, upstreamCtx.Err(), "browser must not own upstream context")
	select {
	case result := <-repo.completed:
		require.Equal(t, "success", result.Status)
		require.Equal(t, u.text, result.ResponseText)
		require.GreaterOrEqual(t, result.LatencyMs, int64(30000))
	case <-time.After(40 * time.Second):
		t.Fatal("accepted run did not finish")
	}
}

func TestIntelligenceRunPreservesPartialOutputOnCancellation(t *testing.T) {
	u := &delayedIntelligenceUpstream{started: make(chan context.Context, 1), text: "<p>partial</p>", cancelAfterPartial: true}
	svc, repo := intelligenceLifecycleService(u)
	svc.executeIntelligenceRun(context.Background(), &IntelligenceRun{ScheduledTestResult: ScheduledTestResult{ID: 1, ModelSnapshot: "gpt-5.4", PromptSnapshot: "Write HTML"}, AccountID: 1})
	result := <-repo.completed
	require.Equal(t, "failed", result.Status)
	require.Equal(t, "upstream_error", result.ErrorCode)
	require.Equal(t, u.text, result.ResponseText)
	require.Contains(t, result.ErrorMessage, "context canceled")
}

func TestIntelligenceRunOutputTruncationIsUTF8Safe(t *testing.T) {
	u := &delayedIntelligenceUpstream{started: make(chan context.Context, 1), text: strings.Repeat("答", 22000)}
	svc, repo := intelligenceLifecycleService(u)
	svc.executeIntelligenceRun(context.Background(), &IntelligenceRun{ScheduledTestResult: ScheduledTestResult{ID: 1, ModelSnapshot: "gpt-5.4", PromptSnapshot: "Write HTML"}, AccountID: 1})
	result := <-repo.completed
	require.Equal(t, "success", result.Status)
	require.True(t, result.OutputTruncated)
	require.True(t, utf8.ValidString(result.ResponseText))
	require.LessOrEqual(t, len(result.ResponseText), 65536)
	require.Greater(t, len(result.ResponseText), 65532)
}

func TestIntelligenceRunSurvivesOld120SecondDeadline(t *testing.T) {
	u := &delayedIntelligenceUpstream{started: make(chan context.Context, 1), delay: 121 * time.Second, text: "complete answer"}
	svc, repo := intelligenceLifecycleService(u)
	run, err := svc.EnqueueRun(context.Background(), &ScheduledTestPlan{ID: 1, TestKind: "intelligence"}, "long-run", "manual")
	require.NoError(t, err)
	require.Equal(t, 300, run.ExecutionTimeoutSeconds)
	svc.executeIntelligenceRun(context.Background(), run)
	result := <-repo.completed
	require.Equal(t, "success", result.Status)
	require.Equal(t, u.text, result.ResponseText)
	require.Greater(t, result.LatencyMs, int64(120000))
}

func TestIntelligenceRunUsesSnapshotDeadlineAndKeepsPartialOutput(t *testing.T) {
	u := &delayedIntelligenceUpstream{started: make(chan context.Context, 1), delay: 5 * time.Second, text: "partial answer"}
	svc, repo := intelligenceLifecycleService(u)
	// A short in-memory snapshot exercises expiry without waiting five minutes.
	// Persisted snapshots are validated at admission and by the database.
	svc.executeIntelligenceRun(context.Background(), &IntelligenceRun{ScheduledTestResult: ScheduledTestResult{ID: 1, ModelSnapshot: "gpt-5.4", PromptSnapshot: "Write HTML"}, AccountID: 1, ExecutionTimeoutSeconds: 1})
	result := <-repo.completed
	require.Equal(t, "failed", result.Status)
	require.Equal(t, "execution_timeout", result.ErrorCode)
	require.Equal(t, u.text, result.ResponseText)
	require.Contains(t, result.ErrorMessage, "context deadline exceeded")
	require.Less(t, result.LatencyMs, int64(4000))
}
