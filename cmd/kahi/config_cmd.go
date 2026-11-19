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
	"github.com/kahiteam/kahi/internal/config"
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
	if n := programAccessNote(os.Geteuid(), drop, cfg); n != "" {
		_, _ = fmt.Fprintf(stderr, "note: %s\n", n)
	}
	for _, w := range process.ProbePrograms(cfg, relOK) {
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
	// The attrs go through slog's text handler, so values are quoted as
	// in a text log ("path=\"/run/my dir/k.pid\""); time, level and msg
	// are dropped.
	var attrs strings.Builder
	text := slog.NewTextHandler(&attrs, &slog.HandlerOptions{ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
		if len(groups) == 0 && (a.Key == slog.TimeKey || a.Key == slog.LevelKey || a.Key == slog.MessageKey) {
			return slog.Attr{}
		}
		return a
	}})
	rec := slog.NewRecord(r.Time, r.Level, "", r.PC)
	r.Attrs(func(a slog.Attr) bool { rec.AddAttrs(a); return true })
	if err := text.WithAttrs(h.attrs).Handle(context.Background(), rec); err != nil {
		return err
	}
	line := prefix + ": " + r.Message
	if s := strings.TrimSpace(attrs.String()); s != "" {
		line += " " + s
	}
	_, err := io.WriteString(h.w, line+"\n")
	return err
}

func (h *checkHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &checkHandler{w: h.w, attrs: append(append([]slog.Attr{}, h.attrs...), attrs...)}
}

func (h *checkHandler) WithGroup(string) slog.Handler { return h }

// programAccessNote says what the program checks could not cover when they
// ran as root (FUNC-087): after a privilege drop the daemon opens program
// logs and starts programs as the drop user, and a program's user setting
// starts it as that user; the checks used root's access rights.
func programAccessNote(euid int, drop supervisor.DropCheck, cfg *config.Config) string {
	if euid != 0 {
		return ""
	}
	if drop.Drop {
		who := fmt.Sprintf("uid %d", drop.Cred.Uid)
		if drop.Cred.Name != "" {
			who = fmt.Sprintf("%s (uid %d)", drop.Cred.Name, drop.Cred.Uid)
		}
		return "program checks ran as root; after the privilege drop the daemon opens program logs and starts programs as " + who + ", whose access was not checked"
	}
	for _, p := range cfg.Programs {
		if p.User != "" {
			return "program checks ran as root; a program with a user setting starts as that user, whose access to its command and directory was not checked"
		}
	}
	for _, l := range cfg.EventListeners {
		if l.User != "" {
			return "program checks ran as root; a program with a user setting starts as that user, whose access to its command and directory was not checked"
		}
	}
	return ""
}
