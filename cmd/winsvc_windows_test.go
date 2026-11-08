//go:build windows

package cmd

import (
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"
)

// serviceManager stands in for the Windows service manager: it runs the
// handler and passes the statuses it reports back to the test.
type serviceManager struct {
	requests chan svc.ChangeRequest
	statuses chan svc.Status
	done     chan [2]any
}

func manage(t *testing.T, cfgPath string) *serviceManager {
	t.Helper()
	resetFlags()
	flagConfig = cfgPath
	m := &serviceManager{
		requests: make(chan svc.ChangeRequest),
		statuses: make(chan svc.Status, 16),
		done:     make(chan [2]any, 1),
	}
	go func() {
		specific, code := scmHandler{}.Execute([]string{"goft-test"}, m.requests, m.statuses)
		m.done <- [2]any{specific, code}
	}()
	return m
}

// await waits for the handler to report state.
func (m *serviceManager) await(t *testing.T, state svc.State) svc.Status {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case st := <-m.statuses:
			if st.State == state {
				return st
			}
		case <-deadline:
			t.Fatalf("the service never reported state %d", state)
		}
	}
}

func (m *serviceManager) exit(t *testing.T) (bool, uint32) {
	t.Helper()
	select {
	case r := <-m.done:
		return r[0].(bool), r[1].(uint32)
	case <-time.After(30 * time.Second):
		t.Fatal("the service did not finish")
		return false, 0
	}
}

func TestTheServiceManagerSeesTheServiceStartAndStop(t *testing.T) {
	f := newFleet(t)
	f.drop("alpha", "a.csv")
	m := manage(t, f.cfgPath)

	st := m.await(t, svc.Running)
	if st.Accepts&svc.AcceptStop == 0 || st.Accepts&svc.AcceptShutdown == 0 {
		t.Errorf("accepts = %v, want stop and shutdown", st.Accepts)
	}
	eventually(t, "alpha delivered its file", func() bool { return f.arrived("alpha", "a.csv") })

	m.requests <- svc.ChangeRequest{Cmd: svc.Interrogate, CurrentStatus: st}
	if got := m.await(t, svc.Running); got != st {
		t.Errorf("interrogated, the service said %+v, want %+v", got, st)
	}

	m.requests <- svc.ChangeRequest{Cmd: svc.Stop}
	m.await(t, svc.StopPending)
	if specific, code := m.exit(t); specific || code != 0 {
		t.Errorf("exit = (%v, %d), want a clean stop", specific, code)
	}
}

func TestTheServiceManagerIsToldWhyTheServiceCouldNotStart(t *testing.T) {
	f := newFleet(t)
	// A Windows service with a job that logs to a console nobody reads.
	f.writeFile("jobs.d/gamma.yaml", `jobs:
  - name: gamma
    direction: send
    local:
      path: ../gamma/src
    remote:
      protocol: local
      path: ../gamma/dst
`)
	m := manage(t, f.cfgPath)
	if specific, code := m.exit(t); !specific || code != 2 {
		t.Errorf("exit = (%v, %d), want the service's own code 2", specific, code)
	}
}
