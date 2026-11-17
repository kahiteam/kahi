package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/kahiteam/kahi/internal/api"
	"github.com/kahiteam/kahi/internal/logging"
	"github.com/kahiteam/kahi/internal/process"
	"github.com/kahiteam/kahi/internal/supervisor"
	"github.com/kahiteam/kahi/internal/web"
	"github.com/spf13/cobra"
)

var (
	checkConfigFlag  string
	checkPidfileFlag string
	checkUserFlag    string
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Work with the config file",
}

var configCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Check that kahi daemon would start with this config, without starting it",
	Long: `Check runs the daemon's config path (search, load, includes, validation,
warnings, user resolution) and read-only probes of the startup steps that
touch the system: [supervisor] directory, the daemon log, minfds/minprocs,
the socket directory and the PID file. Program commands, directories and log
files are checked too; their problems are warnings, as they do not stop the
daemon. Nothing is created, bound, signalled or spawned.

Exit status: 0 when the daemon would start (warnings or not), 1 on errors,
2 on usage errors. Run it as the user that starts the daemon.`,
	Args: cobra.NoArgs,
	RunE: configCheckRun,
}

func init() {
	configCheckCmd.Flags().StringVarP(&checkConfigFlag, "config", "c", "", "config file path (default: search paths)")
	configCheckCmd.Flags().StringVarP(&checkPidfileFlag, "pidfile", "p", "", "PID file path, as given to kahi daemon -p")
	configCheckCmd.Flags().StringVarP(&checkUserFlag, "user", "u", "", "user, as given to kahi daemon -u")
	configCmd.AddCommand(configCheckCmd)
	rootCmd.AddCommand(configCmd)
}

// errCheckFailed ends kahi config check with exit 1 after the errors have
// been printed; main prints nothing more for it.
var errCheckFailed = silentError{errors.New("config check failed")}

func configCheckRun(cmd *cobra.Command, _ []string) error {
	stderr := cmd.ErrOrStderr()
	cfgPath, cfg, err := loadDaemonConfig(checkConfigFlag, stderr)
	if err != nil {
		return err
	}

	drop, err := supervisor.CheckDrop(checkUserFlag, cfg.Supervisor.User)
	if err != nil {
		return err
	}
	if drop.Note != "" {
		_, _ = fmt.Fprintf(stderr, "note: %s\n", drop.Note)
	}

	logger := slog.New(&checkHandler{w: stderr})
	var errs []error
	fail := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}

	// [supervisor] directory: relative paths below are resolved after the
	// daemon's chdir, so the check changes its own working directory too
	// (process-local). When the directory is unusable they are skipped.
	sv := cfg.Supervisor
	relOK := true
	if err := supervisor.ProbeDirectory(sv.Directory); err != nil {
		fail(err)
		relOK = false
	} else if sv.Directory != "" {
		if err := os.Chdir(sv.Directory); err != nil {
			fail(fmt.Errorf("cannot access directory: %s: %w", sv.Directory, err))
			relOK = false
		}
	}
	usable := func(p string) bool { return p != "" && (relOK || filepath.IsAbs(p)) }

	if usable(sv.Logfile) {
		fail(logging.ProbeDaemonLog(sv.Logfile))
	}
	fail(supervisor.ProbeLimits(sv, logger))
	if drop.Override {
		logger.Info("kahi daemon -u " + checkUserFlag + " overrides supervisor.user")
	}

	// A running daemon is the normal case before a restart: it is noted, and
	// the checks it would fail (the PID file refusal, the socket in use) are
	// skipped.
	socketPath := cfg.Server.Unix.File
	var livePID int
	if usable(checkPidfileFlag) {
		live, err := supervisor.ProbePIDFile(checkPidfileFlag, logger)
		fail(err)
		livePID = live
	}
	socketLive := false
	var socketErr error
	if usable(socketPath) {
		socketLive, socketErr = api.ProbeSocket(socketPath)
	}
	switch {
	case livePID > 0:
		_, _ = fmt.Fprintf(stderr, "note: a daemon is running (pid %d); checked as a fresh start\n", livePID)
	case socketLive:
		_, _ = fmt.Fprintf(stderr, "note: a daemon is running (socket %s); checked as a fresh start\n", socketPath)
	case usable(socketPath):
		fail(supervisor.ProbeSocketDir(socketPath))
		fail(socketErr)
	}

	supervisor.RootWarning(logger, drop.Drop)
	if cfg.Server.Web.Enabled {
		web.ProbeStaticDir(cfg.Server.Web.StaticDir, logger)
	}
	for _, w := range process.ProbePrograms(cfg) {
		_, _ = fmt.Fprintf(stderr, "warning: %s\n", w)
	}

	if len(errs) > 0 {
		for _, e := range errs {
			_, _ = fmt.Fprintf(stderr, "error: %v\n", e)
		}
		return errCheckFailed
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "config OK: %s\n", cfgPath)
	return nil
}

// checkHandler prints the records the daemon would write to its log after
// opening it: warnings as "warning: {msg} key=value...", the rest as
// "note: ...".
type checkHandler struct {
	w     io.Writer
	attrs []slog.Attr
}

func (h *checkHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *checkHandler) Handle(_ context.Context, r slog.Record) error {
	prefix := "note"
	if r.Level >= slog.LevelWarn {
		prefix = "warning"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s", prefix, r.Message)
	for _, a := range h.attrs {
		fmt.Fprintf(&b, " %s=%v", a.Key, a.Value)
	}
	r.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(&b, " %s=%v", a.Key, a.Value)
		return true
	})
	b.WriteByte('\n')
	_, err := io.WriteString(h.w, b.String())
	return err
}

func (h *checkHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &checkHandler{w: h.w, attrs: append(append([]slog.Attr{}, h.attrs...), attrs...)}
}

func (h *checkHandler) WithGroup(string) slog.Handler { return h }
