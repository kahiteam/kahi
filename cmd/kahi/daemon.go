package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/kahiteam/kahi/internal/api"
	"github.com/kahiteam/kahi/internal/config"
	"github.com/kahiteam/kahi/internal/logging"
	"github.com/kahiteam/kahi/internal/supervisor"
	"github.com/kahiteam/kahi/internal/web"
	"github.com/spf13/cobra"
)

// apiStopTimeout bounds API server shutdown when the daemon exits.
const apiStopTimeout = 5 * time.Second

var (
	configFlag    string
	pidfileFlag   string
	daemonizeFlag bool
	userFlag      string
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Run the Kahi supervisor daemon",
	RunE:  daemonRun,
}

func init() {
	daemonCmd.Flags().StringVarP(&configFlag, "config", "c", "", "config file path (default: search paths)")
	daemonCmd.Flags().StringVarP(&pidfileFlag, "pidfile", "p", "", "PID file path")
	daemonCmd.Flags().BoolVarP(&daemonizeFlag, "daemonize", "d", false, "run in the background; returns once the daemon runs")
	daemonCmd.Flags().StringVarP(&userFlag, "user", "u", "", "drop privileges to user (name or uid, optionally :group)")
	rootCmd.AddCommand(daemonCmd)
}

func daemonRun(cmd *cobra.Command, args []string) (err error) {
	// A SIGHUP during startup (config load, socket bind) is ignored instead
	// of ending the daemon with Go's default action. The supervisor's signal
	// handler takes SIGHUP over once it runs, before any program is spawned,
	// so no program inherits the ignored disposition (FUNC-014, #136).
	signal.Ignore(syscall.SIGHUP)

	// A daemonized child reports its start, or the error that ended it, to
	// the waiting parent; its stderr is /dev/null.
	report := daemonChildReport()
	if report != nil {
		defer func() {
			if err != nil {
				_, _ = fmt.Fprintln(report, err)
			}
			_ = report.Close()
		}()
	}

	// Resolve config path. The daemon excludes the current directory from its
	// default search order so a root daemon cannot be tricked into loading a
	// ./kahi.toml planted in an attacker-writable working directory (SEC-017).
	// Explicit -c and KAHI_CONFIG are still honored.
	cfgPath, err := config.ResolveDaemon(configFlag)
	if err != nil {
		return err
	}
	// Absolute before [supervisor] directory changes the working directory,
	// so a reload re-reads the same file (FUNC-045 startup order).
	if cfgPath, err = filepath.Abs(cfgPath); err != nil {
		return err
	}

	// Load config with includes and variable expansion.
	// Warnings come first, also for a refused config: the all-interfaces
	// warning accompanies the missing-credentials error (SEC-015).
	cfg, warnings, err := config.LoadWithIncludes(cfgPath)
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}
	if err != nil {
		return err
	}

	// The user to drop to (kahi daemon -u, else [supervisor] user) is
	// resolved and checked before anything is bound: it must exist, and -u
	// needs root while the key only warns as non-root (FUNC-045, ADR-046).
	drop, err := supervisor.PlanDrop(userFlag, cfg.Supervisor.User)
	if err != nil {
		return err
	}
	if drop.Warning != "" {
		fmt.Fprintf(os.Stderr, "warning: %s\n", drop.Warning)
	}

	// -d: the foreground process checks the PID file, then starts the
	// daemon in the background and returns once it runs, with its error if
	// it fails (FUNC-064). Only the child applies directory, umask, log,
	// rlimits, PID file and bind (FUNC-045 startup order).
	if daemonizeFlag && report == nil {
		// A relative PID file is resolved after [supervisor] directory, as
		// the child will.
		pidPath := pidfileFlag
		if pidPath != "" && !filepath.IsAbs(pidPath) && cfg.Supervisor.Directory != "" {
			pidPath = filepath.Join(cfg.Supervisor.Directory, pidPath)
		}
		stderrLog := slog.New(slog.NewTextHandler(os.Stderr, nil))
		if err := supervisor.CheckPIDFile(pidPath, stderrLog); err != nil {
			return err
		}
		if cfg.Supervisor.Logfile == "" {
			fmt.Fprintln(os.Stderr, "warning: with -d and no [supervisor] logfile the daemon log goes to /dev/null")
		}
		return daemonize(cmd.OutOrStdout())
	}

	// Startup order (FUNC-045, 047, 067, 068): directory and umask before
	// the daemon creates any file; their errors go to stderr.
	if err := supervisor.ChangeDirectory(cfg.Supervisor.Directory); err != nil {
		return err
	}
	if err := supervisor.SetUmask(cfg.Supervisor.Umask); err != nil {
		return err
	}

	// Create structured logger.
	daemonLog, err := logging.OpenDaemonLog(
		cfg.Supervisor.LogLevel,
		cfg.Supervisor.LogFormat,
		cfg.Supervisor.Logfile,
	)
	if err != nil {
		return err
	}
	defer func() { _ = daemonLog.Close() }()
	logger := daemonLog.Logger

	// Raise rlimits once, before any process starts, so children inherit
	// them (FUNC-068, ADR-047).
	if err := supervisor.RaiseLimits(cfg.Supervisor, logger); err != nil {
		return err
	}

	if drop.Override {
		logger.Info("kahi daemon -u " + userFlag + " overrides supervisor.user")
	}

	// The socket directory must be writable; a missing one is created, and
	// handed over with the socket on a drop (FUNC-044, ADR-045).
	socketPath := cfg.Server.Unix.File
	var socketDirCreated string
	if socketPath != "" {
		created, err := supervisor.PrepareSocketDir(socketPath)
		if err != nil {
			return err
		}
		if created {
			socketDirCreated = filepath.Dir(socketPath)
		}
	}

	// A running daemon named by the PID file is refused before the socket
	// is touched; a stale file is replaced (FUNC-064). The PID file is
	// written before the bind, so it exists when it is handed over on a
	// drop (ADR-045), and removed again on every return from here on.
	if err := supervisor.CheckPIDFile(pidfileFlag, logger); err != nil {
		return err
	}
	if err := supervisor.WritePIDFile(pidfileFlag); err != nil {
		return err
	}
	// The socket is checked only once this daemon has bound it: a failed
	// bind may leave another daemon's socket in place.
	var boundSocket string
	defer func() { supervisor.RemoveRuntimeFiles(pidfileFlag, boundSocket, logger) }()

	// Warn if running as root without privilege drop configured. Only the
	// process that runs the supervisor warns: with -d that is the
	// daemonized child, not the foreground parent above (FUNC-048, #248).
	supervisor.RootWarning(logger, drop.Drop)

	// Create supervisor.
	sup := supervisor.New(supervisor.SupervisorConfig{
		Config:     cfg,
		ConfigPath: cfgPath,
		Logger:     logger,

		ReopenDaemonLog: daemonLog.Reopen,
		SetLogLevel:     daemonLog.SetLevel,
		OnRunning: func() {
			if report != nil {
				_, _ = fmt.Fprintln(report, "ok")
				_ = report.Close()
				report = nil
			}
		},
		// Output of streams without a log file goes to the daemon's own
		// stdout and stderr, where a container runtime collects it.
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	})

	// Build API server config. The Web UI, when enabled, is mounted on the
	// same server under /ui/ (FUNC-084).
	mgr := sup.Manager()
	socketMode := parseSocketMode(cfg.Server.Unix.Chmod)
	apiCfg := api.Config{
		UnixSocket: socketPath,
		SocketMode: socketMode,
		TCPAddr:    cfg.Server.HTTP.Listen,
		TCPEnabled: cfg.Server.HTTP.Enabled,
		Username:   cfg.Server.HTTP.Username,
		Password:   cfg.Server.HTTP.Password,
		UI:         buildUIHandler(cfg.Server.Web, mgr, web.NewHandler, logger),
	}

	server := api.NewServer(apiCfg, mgr, mgr, sup, sup, sup.Bus(), logger)

	// Bounded: an open SSE stream (a UI page, a log tail) never ends on its
	// own, and Stop closes what is left once the timeout expires. Registered
	// before the listeners start, so a TCP bind failure still closes (and
	// unlinks) the Unix socket; Stop skips a server that never started.
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), apiStopTimeout)
		defer cancel()
		_ = server.Stop(ctx)
	}()

	// Start listeners.
	if socketPath != "" {
		if err := server.StartUnix(socketPath, socketMode); err != nil {
			return err
		}
		boundSocket = socketPath
	}
	if cfg.Server.HTTP.Enabled {
		if err := server.StartTCP(cfg.Server.HTTP.Listen); err != nil {
			return err
		}
	}
	// Optional privilege drop, after binding sockets: the files the daemon
	// created as root go to the target user first (ADR-045).
	if drop.Drop {
		paths := []string{socketPath, socketDirCreated, pidfileFlag, cfg.Supervisor.Logfile}
		if err := supervisor.DropPrivileges(drop.Cred, paths, logger); err != nil {
			return err
		}
	}

	// Run main loop (blocks until shutdown signal).
	return sup.Run()
}

// parseSocketMode parses a chmod string (e.g. "0700") into os.FileMode.
// Returns 0o700 if the string is empty or invalid.
func parseSocketMode(s string) os.FileMode {
	if s == "" {
		return 0o700
	}
	v, err := strconv.ParseUint(s, 8, 32)
	if err != nil {
		return 0o700
	}
	return os.FileMode(v)
}
