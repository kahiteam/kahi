package main

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/kahiteam/kahi/internal/api"
	"github.com/kahiteam/kahi/internal/config"
	"github.com/kahiteam/kahi/internal/logging"
	"github.com/kahiteam/kahi/internal/supervisor"
	"github.com/spf13/cobra"
)

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
	daemonCmd.Flags().BoolVarP(&daemonizeFlag, "daemonize", "d", false, "run in background (double-fork)")
	daemonCmd.Flags().StringVarP(&userFlag, "user", "u", "", "drop privileges to user (uid or uid:gid)")
	rootCmd.AddCommand(daemonCmd)
}

func daemonRun(cmd *cobra.Command, args []string) error {
	// Resolve config path.
	cfgPath, err := config.Resolve(configFlag)
	if err != nil {
		return err
	}

	// Load config with includes and variable expansion.
	cfg, warnings, err := config.LoadWithIncludes(cfgPath)
	if err != nil {
		return err
	}
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}

	// Create structured logger.
	logger, cleanup, err := logging.DaemonLogger(
		cfg.Supervisor.LogLevel,
		cfg.Supervisor.LogFormat,
		cfg.Supervisor.Logfile,
	)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}

	// Validate socket directory is writable.
	socketPath := cfg.Server.Unix.File
	if socketPath != "" {
		if err := supervisor.ValidateSocketPermissions(socketPath); err != nil {
			return err
		}
	}

	// Warn if running as root without privilege drop configured.
	supervisor.RootWarning(logger, userFlag != "")

	// Optional daemonize (before privilege drop, after socket validation).
	if daemonizeFlag {
		shouldExit, err := supervisor.Daemonize(logger)
		if err != nil {
			return fmt.Errorf("daemonize failed: %w", err)
		}
		if shouldExit {
			os.Exit(0)
		}
	}

	// Create supervisor.
	sup := supervisor.New(supervisor.SupervisorConfig{
		Config:     cfg,
		ConfigPath: cfgPath,
		PIDFile:    pidfileFlag,
		Logger:     logger,
	})

	// Build API server config.
	socketMode := parseSocketMode(cfg.Server.Unix.Chmod)
	apiCfg := api.Config{
		UnixSocket: socketPath,
		SocketMode: socketMode,
		TCPAddr:    cfg.Server.HTTP.Listen,
		TCPEnabled: cfg.Server.HTTP.Enabled,
		Username:   cfg.Server.HTTP.Username,
		Password:   cfg.Server.HTTP.Password,
	}

	mgr := sup.Manager()
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
	defer func() { _ = server.Stop(context.Background()) }()

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
