package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
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
	daemonCmd.Flags().StringVarP(&userFlag, "user", "u", "", "drop privileges to user (uid or uid:gid)")
	rootCmd.AddCommand(daemonCmd)
}

func daemonRun(cmd *cobra.Command, args []string) (err error) {
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

	// Validate socket directory is writable.
	socketPath := cfg.Server.Unix.File
	if socketPath != "" {
		if err := supervisor.ValidateSocketPermissions(socketPath); err != nil {
			return err
		}
	}

	// Warn if running as root without privilege drop configured.
	supervisor.RootWarning(logger, userFlag != "")

	// A running daemon named by the PID file is refused before the socket
	// is touched; a stale file is replaced (FUNC-064).
	if err := supervisor.CheckPIDFile(pidfileFlag, logger); err != nil {
		return err
	}

	// -d: the foreground process starts the daemon in the background and
	// returns once it runs, with its error if it fails (FUNC-064).
	if daemonizeFlag && report == nil {
		if cfg.Supervisor.Logfile == "" {
			fmt.Fprintln(os.Stderr, "warning: with -d and no [supervisor] logfile the daemon log goes to /dev/null")
		}
		return daemonize(cmd.OutOrStdout())
	}

	// Create supervisor.
	sup := supervisor.New(supervisor.SupervisorConfig{
		Config:     cfg,
		ConfigPath: cfgPath,
		PIDFile:    pidfileFlag,
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

	// Start listeners.
	if socketPath != "" {
		if err := server.StartUnix(socketPath, socketMode); err != nil {
			return err
		}
	}
	if cfg.Server.HTTP.Enabled {
		if err := server.StartTCP(cfg.Server.HTTP.Listen); err != nil {
			return err
		}
	}
	// Bounded: an open SSE stream (a UI page, a log tail) never ends on its
	// own, and Stop closes what is left once the timeout expires.
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), apiStopTimeout)
		defer cancel()
		_ = server.Stop(ctx)
	}()

	// Optional privilege drop (after binding sockets).
	if userFlag != "" {
		if err := supervisor.DropPrivileges(userFlag, logger); err != nil {
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
