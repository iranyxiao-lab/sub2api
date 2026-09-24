// Package releasecontrol defers periodic jobs during a staged application handover.
package releasecontrol

import (
	"log"
	"os"
	"sync"
	"time"
)

const ActivationEnv = "SUB2API_BACKGROUND_ACTIVATION_FILE"

type job struct {
	name  string
	start func()
}
type gate struct {
	mu              sync.Mutex
	path            string
	active, stopped bool
	pending         []job
	stop            chan struct{}
	done            chan struct{}
}

func newGate(path string) *gate {
	g := &gate{path: path, active: path == "", stop: make(chan struct{}), done: make(chan struct{})}
	if path == "" {
		close(g.done)
	} else {
		go g.watch()
	}
	return g
}

func (g *gate) watch() {
	defer close(g.done)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-g.stop:
			return
		case <-tick.C:
			info, err := os.Stat(g.path)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			g.activate()
			return
		}
	}
}

func (g *gate) start(name string, start func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopped {
		return
	}
	if g.active {
		start()
		return
	}
	g.pending = append(g.pending, job{name, start})
	log.Printf("[ReleaseControl] deferred %s", name)
}

func (g *gate) activate() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopped || g.active {
		return
	}
	g.active = true
	for _, j := range g.pending {
		j.start()
		log.Printf("[ReleaseControl] activated %s", j.name)
	}
	g.pending = nil
}

func (g *gate) shutdown() {
	g.mu.Lock()
	if !g.stopped {
		g.stopped = true
		g.pending = nil
		close(g.stop)
	}
	g.mu.Unlock()
	<-g.done
}

var processGate = newGate(os.Getenv(ActivationEnv))

// Configured stays true after activation: cross-process startup cleanup is never safe.
func Configured() bool                { return processGate.path != "" }
func Start(name string, start func()) { processGate.start(name, start) }

// Stop must precede service cleanup so deferred starts cannot race with Stop methods.
func Stop() { processGate.shutdown() }
