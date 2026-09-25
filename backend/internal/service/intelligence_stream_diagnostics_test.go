//go:build unit

package service

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestIntelligenceStreamDiagnosticsRecognizesReasoningAndCompletion(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	d := &intelligenceStreamDiagnostics{started: time.Now().Add(-time.Second), firstEventMs: -1, lastEventMs: -1}
	c.Set(intelligenceStreamDiagnosticsKey, d)
	observeIntelligenceStreamLine(c, ": heartbeat\n")
	require.Equal(t, int64(-1), d.firstEventMs)
	svc := &AccountTestService{}
	err := svc.processOpenAIStream(c, strings.NewReader("data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"thinking\"}\n\ndata: {\"type\":\"response.completed\"}\n\n"))
	require.NoError(t, err)
	require.GreaterOrEqual(t, d.firstEventMs, int64(1000))
	require.GreaterOrEqual(t, d.lastEventMs, d.firstEventMs)
	require.True(t, d.completed)
}

func TestIntelligenceStreamDiagnosticsDoesNotCompleteOnTruncatedStream(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	d := &intelligenceStreamDiagnostics{started: time.Now(), firstEventMs: -1, lastEventMs: -1}
	c.Set(intelligenceStreamDiagnosticsKey, d)
	err := (&AccountTestService{}).processOpenAIStream(c, strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"))
	require.ErrorContains(t, err, "before response.completed")
	require.False(t, d.completed)
}
