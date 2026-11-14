package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// MaxRestartBackoff caps exponential restart backoff so a persistent failure
// is retried often enough to be useful.
const MaxRestartBackoff = time.Minute

// ErrSupervisorExhausted means the child kept exiting and the restart window
// ran out of attempts. The state file is intentionally left in place so the
// TUI can show a crash card and `wool restart` can relaunch the instance.
var ErrSupervisorExhausted = errors.New("restart attempts exhausted")

// RestartPolicy bounds automatic restarts. MaxAttempts counts restarts, not
// child launches: zero gives up after the first unexpected exit.
type RestartPolicy struct {
	MaxAttempts int
	Window      time.Duration
	Backoff     time.Duration
}

// NextRestartDelay doubles the base backoff per attempt, capped at
// MaxRestartBackoff.
func NextRestartDelay(backoff time.Duration, attempt int) time.Duration {
	if backoff <= 0 {
		backoff = time.Second
	}
	d := backoff
	for i := 0; i < attempt; i++ {
		if d >= MaxRestartBackoff {
			return MaxRestartBackoff
		}
		d *= 2
	}
	if d > MaxRestartBackoff {
		return MaxRestartBackoff
	}
	return d
}

// PruneAttempts drops attempts outside the sliding window.
func PruneAttempts(attempts []time.Time, now time.Time, window time.Duration) []time.Time {
	if window <= 0 {
		window = 10 * time.Minute
	}
	cutoff := now.Add(-window)
	kept := attempts[:0]
	for _, at := range attempts {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	return kept
}

// AttemptsInWindow counts attempts inside the sliding window without mutating
// the history.
func AttemptsInWindow(attempts []time.Time, now time.Time, window time.Duration) int {
	if window <= 0 {
		window = 10 * time.Minute
	}
	cutoff := now.Add(-window)
	n := 0
	for _, at := range attempts {
		if at.After(cutoff) {
			n++
		}
	}
	return n
}

// SupervisorConfig describes the child and policy for one supervisor run.
type SupervisorConfig struct {
	Name   string
	Kind   Kind
	Host   string
	Port   int
	Bin    string
	Args   []string
	Env    []string
	Policy RestartPolicy
	Stdout io.Writer
	Stderr io.Writer
}

// RunSupervisor launches the child and restarts it after an unexpected exit.
// It returns nil when ctx is cancelled, which is how a clean `wool stop`
// reaches the supervisor, and ErrSupervisorExhausted when the policy gives up.
// The supervisor itself never writes state: the state file describes the
// supervisor, so a later `wool status` or `wool restart` still works.
func RunSupervisor(ctx context.Context, cfg SupervisorConfig) error {
	if cfg.Bin == "" {
		return fmt.Errorf("supervisor: missing child binary")
	}
	out := cfg.Stdout
	if out == nil {
		out = os.Stdout
	}
	errOut := cfg.Stderr
	if errOut == nil {
		errOut = os.Stderr
	}
	policy := cfg.Policy
	if policy.Window <= 0 {
		policy.Window = 10 * time.Minute
	}
	if policy.Backoff <= 0 {
		policy.Backoff = 2 * time.Second
	}

	var attempts []time.Time
	for run := 1; ; run++ {
		if ctx.Err() != nil {
			return nil
		}
		logChildLaunch(out, cfg, run)

		cmd := exec.Command(cfg.Bin, cfg.Args...)
		cmd.Stdout = out
		cmd.Stderr = errOut
		cmd.Env = append(os.Environ(), cfg.Env...)
		if err := cmd.Start(); err != nil {
			fmt.Fprintf(errOut, "supervisor: start %s: %v\n", cfg.Bin, err)
		} else {
			waitCh := make(chan error, 1)
			go func() { waitCh <- cmd.Wait() }()
			select {
			case <-ctx.Done():
				stopSupervisedChild(cmd, waitCh, 15*time.Second)
				return nil
			case err := <-waitCh:
				if ctx.Err() != nil {
					return nil
				}
				fmt.Fprintf(errOut, "supervisor: child exited: %s\n", exitReason(err))
			}
		}

		now := time.Now()
		attempts = PruneAttempts(attempts, now, policy.Window)
		if len(attempts) >= policy.MaxAttempts {
			fmt.Fprintf(errOut, "supervisor: giving up after %d restart attempt(s) in %s\n",
				len(attempts), policy.Window)
			return ErrSupervisorExhausted
		}
		delay := NextRestartDelay(policy.Backoff, len(attempts))
		fmt.Fprintf(errOut, "supervisor: restarting in %s (attempt %d/%d)\n",
			delay, len(attempts)+1, policy.MaxAttempts)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
		}
		attempts = append(attempts, time.Now())
	}
}

// stopSupervisedChild asks the child to exit, then kills it if it ignores the
// signal. The child shares the supervisor's process group, so signalling the
// PID avoids touching the supervisor itself.
func stopSupervisedChild(cmd *exec.Cmd, waitCh <-chan error, timeout time.Duration) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-waitCh:
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		<-waitCh
	}
}

// logChildLaunch writes a launch header so ClassifyCrash only inspects the
// newest attempt.
func logChildLaunch(w io.Writer, cfg SupervisorConfig, run int) {
	host := cfg.Host
	if host == "" {
		host = "127.0.0.1"
	}
	kind := cfg.Kind
	if kind == "" {
		kind = KindServer
	}
	fmt.Fprintf(w, "=== %s starting %s %s on %s:%d (attempt %d) ===\n",
		time.Now().Format(time.RFC3339), kind, cfg.Name, displayHost(host), cfg.Port, run)
	fmt.Fprintf(w, "$ %s\n", renderCommand(cfg.Bin, cfg.Args))
}

func exitReason(err error) string {
	if err == nil {
		return "exit status 0"
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return "terminated by " + ws.Signal().String()
		}
	}
	return err.Error()
}
