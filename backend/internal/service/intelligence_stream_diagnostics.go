package service

import (
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const intelligenceStreamDiagnosticsKey = "intelligence_stream_diagnostics"

// Updated synchronously by the account-test stream parser; never stores payloads.
type intelligenceStreamDiagnostics struct {
	started      time.Time
	firstEventMs int64
	lastEventMs  int64
	completed    bool
}

func observeIntelligenceStreamLine(c *gin.Context, line string) {
	v, ok := c.Get(intelligenceStreamDiagnosticsKey)
	if !ok || !strings.HasPrefix(strings.TrimSpace(line), "data:") {
		return
	}
	d := v.(*intelligenceStreamDiagnostics)
	d.lastEventMs = time.Since(d.started).Milliseconds()
	if d.firstEventMs < 0 {
		d.firstEventMs = d.lastEventMs
	}
}
