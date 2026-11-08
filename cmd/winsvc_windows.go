//go:build windows

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"

	"goft/internal/i18n"
)

// defaultServiceName is the name goft service install registers unless told
// otherwise.
const defaultServiceName = "goft"

// Event ids written to the Windows event log.
const (
	eventStarted     = 1
	eventStopped     = 2
	eventStartFailed = 3
)

// How a stop is reported to the service manager while the jobs finish the
// files they have under way: a fresh checkpoint every few seconds tells it the
// service is still making progress rather than hung.
const (
	stopWaitHint   = 30 * time.Second
	stopCheckpoint = 5 * time.Second
)

var flagServiceName string

var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: i18n.N("Install goft as a Windows service, or remove it"),
	Args:  cobra.NoArgs,
}

var serviceInstallCmd = &cobra.Command{
	Use:   "install",
	Short: i18n.N("Register goft as a Windows service that starts with Windows"),
	Long: i18n.N("Register goft as a Windows service that starts with Windows.\n\n" +
		"The service runs this executable as LocalSystem with the configuration\n" +
		"--config names, or the one goft would find, and is restarted a minute after\n" +
		"it stops on an error. A service has no console, so every log must go to a\n" +
		"file; the configuration is checked for that, and for everything else, before\n" +
		"anything is registered. Run it from an administrator's prompt."),
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return installService(cmd.OutOrStdout(), flagServiceName)
	},
}

var serviceUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: i18n.N("Stop the Windows service if it is running, and remove it"),
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return uninstallService(cmd.OutOrStdout(), flagServiceName)
	},
}

func init() {
	serviceCmd.PersistentFlags().StringVar(&flagServiceName, "name", defaultServiceName, i18n.N("name of the Windows service"))
	serviceCmd.AddCommand(serviceInstallCmd, serviceUninstallCmd)
	rootCmd.AddCommand(serviceCmd)
}

// underServiceManager reports whether goft was started by the Windows service
// manager.
func underServiceManager() bool {
	ok, err := svc.IsWindowsService()
	return err == nil && ok
}

// runUnderServiceManager runs the service on behalf of the Windows service
// manager, until it is told to stop.
//
// The name given to svc.Run does not matter for a service in a process of its
// own; the one the service was installed under arrives in Execute.
func runUnderServiceManager() error {
	if err := svc.Run(defaultServiceName, scmHandler{}); err != nil {
		return startupError(err)
	}
	return nil
}

// scmHandler answers the service manager.
type scmHandler struct{}

// Execute starts the jobs, reports the service running once every one of
// them has started, and stops them when asked. Starting and stopping are
// written to the event log, where an administrator looks first; what the jobs
// do goes to their own logs.
func (scmHandler) Execute(args []string, r <-chan svc.ChangeRequest, s chan<- svc.Status) (bool, uint32) {
	name := defaultServiceName
	if len(args) > 0 && args[0] != "" {
		name = args[0]
	}
	events := openEvents(name)
	defer events.Close()

	s <- svc.Status{State: svc.StartPending}

	cfg, err := loadConfig()
	if err == nil {
		err = requireLogFiles(cfg)
	}
	if err != nil {
		events.Error(eventStartFailed, tr.Sprintf("goft could not start: %s", tr.Error(err)))
		return true, 2
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- runService(ctx, cfg, func() { close(started) }) }()

	var (
		status = svc.Status{State: svc.StartPending}
		tick   <-chan time.Time
	)
	for {
		select {
		case <-started:
			started = nil
			status = svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
			s <- status
			names := make([]string, 0, len(cfg.Enabled()))
			for _, j := range cfg.Enabled() {
				names = append(names, j.Name)
			}
			events.Info(eventStarted, tr.Sprintf("goft is running %d job(s): %s", len(names), strings.Join(names, ", ")))

		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				s <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				if status.State == svc.StopPending {
					continue
				}
				status = svc.Status{State: svc.StopPending, WaitHint: uint32(stopWaitHint.Milliseconds())}
				s <- status
				cancel()
				t := time.NewTicker(stopCheckpoint)
				defer t.Stop()
				tick = t.C
			}

		case <-tick:
			status.CheckPoint++
			s <- status

		case err := <-done:
			if err != nil {
				events.Error(eventStartFailed, tr.Sprintf("goft could not start: %s", tr.Error(err)))
				code := uint32(2)
				var ee *exitError
				if errors.As(err, &ee) {
					code = uint32(ee.code)
				}
				return true, code
			}
			events.Info(eventStopped, tr.Text(i18n.N("goft stopped")))
			return false, 0
		}
	}
}

// eventWriter is the event log, or nothing when it cannot be opened: a
// service that cannot report its start is no reason not to start.
type eventWriter struct{ log *eventlog.Log }

func openEvents(source string) eventWriter {
	l, err := eventlog.Open(source)
	if err != nil {
		return eventWriter{}
	}
	return eventWriter{log: l}
}

func (e eventWriter) Info(id uint32, msg string) {
	if e.log != nil {
		_ = e.log.Info(id, msg)
	}
}

func (e eventWriter) Error(id uint32, msg string) {
	if e.log != nil {
		_ = e.log.Error(id, msg)
	}
}

func (e eventWriter) Close() {
	if e.log != nil {
		_ = e.log.Close()
	}
}

// installService registers goft with the service manager, set to start with
// Windows and to be restarted after it stops on an error, and registers the
// event log source it reports under.
func installService(out io.Writer, name string) error {
	cfg, err := loadConfig()
	if err != nil {
		return startupError(err)
	}
	if err := requireLogFiles(cfg); err != nil {
		return startupError(err)
	}
	exe, err := os.Executable()
	if err != nil {
		return startupError(err)
	}

	m, err := mgr.Connect()
	if err != nil {
		return startupError(i18n.Errorf("connect to the service manager: %w (run goft from an administrator's prompt)", err))
	}
	defer m.Disconnect()
	if s, err := m.OpenService(name); err == nil {
		s.Close()
		return startupError(i18n.Errorf("a service called %q is already installed (goft service uninstall --name %s removes it)", name, name))
	}

	display := "goft"
	if name != defaultServiceName {
		display = "goft (" + name + ")"
	}
	s, err := m.CreateService(name, exe, mgr.Config{
		DisplayName: display,
		Description: tr.Sprintf("Transfers files as configured in %s", cfg.SourceFile),
		StartType:   mgr.StartAutomatic,
	}, "-c", cfg.SourceFile)
	if err != nil {
		return startupError(i18n.Errorf("register service %q: %w", name, err))
	}
	defer s.Close()
	undo := func(err error) error {
		_ = s.Delete()
		return startupError(err)
	}

	// Restarted a minute after any stop on an error, including one where the
	// process exited by itself with a failure code, as a configuration that
	// cannot be loaded makes it do. A day without one starts the count again.
	restart := mgr.RecoveryAction{Type: mgr.ServiceRestart, Delay: time.Minute}
	if err := s.SetRecoveryActions([]mgr.RecoveryAction{restart, restart, restart}, uint32((24 * time.Hour).Seconds())); err != nil {
		return undo(i18n.Errorf("set service %q to restart: %w", name, err))
	}
	if err := s.SetRecoveryActionsOnNonCrashFailures(true); err != nil {
		return undo(i18n.Errorf("set service %q to restart: %w", name, err))
	}

	// A source left from an earlier install would make registering it fail.
	_ = eventlog.Remove(name)
	if err := eventlog.InstallAsEventCreate(name, eventlog.Error|eventlog.Warning|eventlog.Info); err != nil {
		return undo(i18n.Errorf("register event log source %q: %w", name, err))
	}

	fmt.Fprintln(out, tr.Sprintf("Installed service %q: %s -c %s, as LocalSystem, starting with Windows.", name, exe, cfg.SourceFile))
	fmt.Fprintln(out, tr.Sprintf("Start it now with: sc.exe start %s", name))
	return nil
}

// uninstallService stops the service if it is running, then removes it and
// its event log source.
func uninstallService(out io.Writer, name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return startupError(i18n.Errorf("connect to the service manager: %w (run goft from an administrator's prompt)", err))
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		return startupError(i18n.Errorf("no service called %q is installed: %w", name, err))
	}
	defer s.Close()

	if st, err := s.Query(); err == nil && st.State != svc.Stopped {
		fmt.Fprintln(out, tr.Sprintf("Stopping service %q...", name))
		if st.State != svc.StopPending {
			if _, err := s.Control(svc.Stop); err != nil {
				return startupError(i18n.Errorf("stop service %q: %w", name, err))
			}
		}
		if err := waitStopped(s, name, 2*time.Minute); err != nil {
			return startupError(err)
		}
	}

	if err := s.Delete(); err != nil {
		return startupError(i18n.Errorf("remove service %q: %w", name, err))
	}
	if err := eventlog.Remove(name); err != nil {
		fmt.Fprintln(out, tr.Sprintf("The event log source %q was left in place: %s", name, tr.Error(err)))
	}
	fmt.Fprintln(out, tr.Sprintf("Removed service %q.", name))
	return nil
}

// waitStopped waits for the service to report itself stopped.
func waitStopped(s *mgr.Service, name string, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	for {
		st, err := s.Query()
		if err != nil {
			return i18n.Errorf("stop service %q: %w", name, err)
		}
		if st.State == svc.Stopped {
			return nil
		}
		if time.Now().After(deadline) {
			return i18n.Errorf("service %q did not stop within %s", name, limit)
		}
		time.Sleep(300 * time.Millisecond)
	}
}
