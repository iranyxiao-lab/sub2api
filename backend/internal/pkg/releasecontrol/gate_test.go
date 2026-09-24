package releasecontrol

import (
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestMarkerActivatesWithoutRestart(t *testing.T) {
	path := t.TempDir() + "/activate"
	g := newGate(path)
	defer g.shutdown()
	done := make(chan struct{})
	g.start("periodic", func() { close(done) })
	if err := os.WriteFile(path, []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("marker did not activate pending jobs")
	}
}

func TestGateActivationAndStop(t *testing.T) {
	g := newGate(t.TempDir() + "/activate")
	var count atomic.Int32
	g.start("periodic", func() { count.Add(1) })
	if count.Load() != 0 {
		t.Fatal("candidate started periodic job")
	}
	g.activate()
	g.activate()
	if count.Load() != 1 {
		t.Fatal("activation must happen exactly once")
	}
	g.shutdown()
	g.start("late", func() { count.Add(1) })
	if count.Load() != 1 {
		t.Fatal("started after shutdown")
	}
}

func TestGateStopBeforeActivation(t *testing.T) {
	g := newGate(t.TempDir() + "/activate")
	g.start("pending", func() { t.Error("started after shutdown") })
	g.shutdown()
	g.activate()
	g.shutdown()
}

func TestGateDefaultImmediate(t *testing.T) {
	g := newGate("")
	defer g.shutdown()
	called := false
	g.start("normal", func() { called = true })
	if !called {
		t.Fatal("default startup changed")
	}
}
