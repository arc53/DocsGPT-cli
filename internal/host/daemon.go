package host

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/arc53/DocsGPT-cli/internal/display"
	"github.com/arc53/DocsGPT-cli/internal/update"
)

// DaemonOptions configures RunDaemon.
type DaemonOptions struct {
	PollInterval string // overrides host.yml's poll_interval when set
	ServiceMode  bool   // log to the host log file instead of the terminal
	Version      string
	AutoUpdate   string // update.ModeOn / ModeNotify / ModeOff
}

// idleHeartbeatInterval is how often the daemon prints an "idle" line
// when no work has arrived for a while. Keeps the user reassured the
// process is alive without spamming on every poll.
const idleHeartbeatInterval = 60 * time.Second

// RunDaemon loads host.yml and runs the long-lived poll/stream loop until
// SIGINT/SIGTERM (nil) or a revoke (ErrRevoked, after printing why; the
// caller exits 0 so service managers do not restart a revoked device).
func RunDaemon(opts DaemonOptions) error {
	cfg, err := LoadHostConfig()
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("load host.yml: %w", err)
	}
	// Service mode (Windows scheduled task): redirect output to the log
	// file and drop the console window before anything is printed, so even
	// the "not paired" error below lands somewhere inspectable.
	if opts.ServiceMode {
		if err := EnterServiceMode(cfg.LogFile); err != nil {
			return err
		}
	}
	if cfg.DeviceID == "" || cfg.SessionToken == "" {
		return fmt.Errorf("not paired. Run `docsgpt-cli host pair` first")
	}
	if opts.PollInterval != "" {
		cfg.PollInterval = opts.PollInterval
	}

	key, err := LoadOrCreateKey()
	if err != nil {
		return err
	}
	ShowStartupBanner(cfg)

	t := NewTransport(cfg, key, opts.Version)
	jobs := newJobs(t, cfg.DeviceID, "")
	if err := jobs.openSpool(SpoolDir()); err != nil {
		fmt.Fprintln(os.Stderr, display.Warn("command reports are kept in memory only: "+err.Error()))
	}
	if n := jobs.Recover(); n > 0 {
		fmt.Println(display.Muted(fmt.Sprintf("%s delivering %d command report(s) left by the last run",
			LogStamp(time.Now()), n)))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// force ends the shutdown's waiting: a second Ctrl+C kills the running
	// commands at once and leaves undelivered reports in the spool.
	force, forceNow := context.WithCancel(context.Background())
	defer forceNow()

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		<-sigCh
		fmt.Println()
		fmt.Println(display.Muted("Shutting down host..."))
		cancel()
		<-sigCh
		forceNow()
	}()

	go runIdleHeartbeat(ctx, t)
	go runAutoUpdate(ctx, t, jobs, opts.Version, opts.AutoUpdate)

	// Commands run in the background, outside any session, so the SSE loop
	// keeps reading the next events (a cancel among them).
	t.OnInvocation = jobs.Start
	t.OnCancel = jobs.Cancel
	t.OnReconnect = jobs.Kick

	err = serve(ctx, t)
	if !jobs.Idle() {
		fmt.Println(display.Muted("Stopping running commands and sending their reports (Ctrl+C again to force)..."))
	}
	jobs.Shutdown(force)
	return err
}

// serve runs poll and SSE sessions until ctx ends (nil) or the device is
// revoked (ErrRevoked).
func serve(ctx context.Context, t *Transport) error {
	revoked := func() error {
		fmt.Fprintln(os.Stderr, display.Warn("device has been revoked, terminating"))
		return ErrRevoked
	}
	fastUntil := time.Time{}
	for {
		if ctx.Err() != nil {
			return nil
		}
		pr, err := t.RunPolling(ctx, fastUntil)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, ErrRevoked) {
				return revoked()
			}
			return err
		}
		sessionID := pr.SessionTicket
		t.Baton.SetSessionID(sessionID)
		fmt.Println(display.Muted(LogStamp(time.Now()) + " session opened"))
		if err := t.RunSSE(ctx, sessionID, ""); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, ErrRevoked) {
				return revoked()
			}
			fmt.Fprintln(os.Stderr, display.Warn("SSE error: "+err.Error()))
		}
		t.Baton.Transition(StateStreaming, StatePolling)
		fmt.Println(display.Muted(LogStamp(time.Now()) + " session closed"))
		fastUntil = time.Now().Add(30 * time.Second)
	}
}

// runIdleHeartbeat prints a periodic "idle" line while polling, so a
// service-mode user has positive evidence the daemon is alive. Streaming
// sessions emit their own opened/closed lines, so we suppress the
// heartbeat during streaming.
func runIdleHeartbeat(ctx context.Context, t *Transport) {
	ticker := time.NewTicker(idleHeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if t.Baton.State() != StatePolling {
				continue
			}
			suffix := ""
			if last := t.Baton.LastActivity(); !last.IsZero() {
				suffix = fmt.Sprintf(" (last connected: %s ago)", HumanDuration(time.Since(last)))
			}
			fmt.Println(display.Muted(fmt.Sprintf(
				"%s idle - polling every %s%s",
				LogStamp(time.Now()),
				t.Cfg.PollInterval,
				suffix,
			)))
		}
	}
}

// Auto-update cadence for the daemon: wait out the boot phase first (a
// crash-looping service must not update-storm), then check occasionally
// with jitter so a fleet doesn't hit GitHub in lockstep.
const (
	updateInitialDelay = 10 * time.Minute
	updateInterval     = 12 * time.Hour
	updateBusyRetry    = 5 * time.Minute
)

// runAutoUpdate periodically installs new releases and restarts the
// daemon into them, but only while idle: polling, no session, no command
// running and every report delivered.
func runAutoUpdate(ctx context.Context, t *Transport, jobs *Jobs, version, mode string) {
	if mode == update.ModeOff {
		return
	}

	delay := updateInitialDelay
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = updateInterval + rand.N(2*time.Hour)

		if t.Baton.State() != StatePolling || !jobs.Idle() {
			delay = updateBusyRetry
			continue
		}

		if mode == update.ModeNotify {
			if rel, err := update.FetchLatest(30 * time.Second); err == nil {
				update.RecordCheck(rel)
				if update.IsNewer(rel.TagName, version) {
					fmt.Println(display.Muted(LogStamp(time.Now()) +
						" update available: " + rel.TagName + " (run 'docsgpt-cli update')"))
				}
			}
			continue
		}

		ver, err := update.CheckAndApply(version)
		if err != nil {
			fmt.Fprintln(os.Stderr, display.Warn("auto-update failed: "+err.Error()))
			continue
		}
		if ver == "" {
			continue
		}
		// Sessions may have opened, and commands started, during the
		// download; hold the restart until the daemon is idle again.
		for !holdForRestart(t, jobs) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
			}
		}
		fmt.Println(display.Muted(LogStamp(time.Now()) + " updated to " + ver + ", restarting"))
		exe, err := os.Executable()
		if err == nil {
			err = update.Restart(exe)
		}
		// Unreachable unless the restart itself failed; the new binary is on
		// disk, so the next supervisor restart still picks it up.
		fmt.Fprintln(os.Stderr, display.Warn("restart after update failed: "+err.Error()))
		t.Baton.Transition(StateRestarting, StatePolling)
	}
}

// holdForRestart takes the baton, so no session (and no command) can start,
// and keeps it only when nothing runs and every report was delivered.
// Undelivered reports would survive in the spool, but not when the spool is
// unavailable, so they are waited for too.
func holdForRestart(t *Transport, jobs *Jobs) bool {
	if !t.Baton.Transition(StatePolling, StateRestarting) {
		return false
	}
	if jobs.Idle() {
		return true
	}
	t.Baton.Transition(StateRestarting, StatePolling)
	return false
}

// HumanDuration renders a Duration as 14s, 2m, 1h, etc. Trades precision
// for legibility — these are status lines, not metrics.
func HumanDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
