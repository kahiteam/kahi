package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/kahiteam/kahi/internal/config"
	"github.com/kahiteam/kahi/internal/ctl"
	"github.com/spf13/cobra"
)

var (
	ctlSocket          string
	ctlConfig          string
	ctlAddr            string
	ctlUser            string
	ctlPass            string
	ctlNoColor         bool
	ctlJSON            bool
	ctlShutdownTimeout time.Duration
	ctlActionTimeout   time.Duration
)

var ctlCmd = &cobra.Command{
	Use:   "ctl",
	Short: "Control a running Kahi daemon",
	Long:  "Send commands to a running Kahi daemon via its API.",
}

func newCtlClient() *ctl.Client {
	if ctlAddr != "" {
		return ctl.NewTCPClient(ctlAddr, ctlUser, ctlPass)
	}
	sock := ctlSocket
	if sock == "" {
		sock = resolveSocketFromConfig()
	}
	return ctl.NewUnixClient(sock)
}

// resolveSocketFromConfig attempts to find the socket path from the config
// file. Falls back to the default socket path if config cannot be loaded.
func resolveSocketFromConfig() string {
	defaultSocket := config.DefaultSocketPath()

	if env := os.Getenv("KAHI_SOCKET"); env != "" {
		return env
	}

	cfgPath, err := config.Resolve(ctlConfig)
	if err != nil {
		return defaultSocket
	}
	cfg, _, err := config.Load(cfgPath)
	if err != nil {
		return defaultSocket
	}
	if cfg.Server.Unix.File != "" {
		// The daemon expands the path (%(here)s, ${VAR}); ctl must dial the
		// same one.
		file, err := config.ExpandString(cfg.Server.Unix.File, config.ExpandContext{Here: filepath.Dir(cfgPath)})
		if err != nil {
			return defaultSocket
		}
		return file
	}
	return defaultSocket
}

var ctlStartCmd = &cobra.Command{
	Use:   "start [process...]",
	Short: "Start processes",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAction(cmd, args, "start", "started", (*ctl.Client).Start, (*ctl.Client).StartGroup)
	},
}

var ctlStopCmd = &cobra.Command{
	Use:   "stop [process...]",
	Short: "Stop processes",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAction(cmd, args, "stop", "stopped", (*ctl.Client).Stop, (*ctl.Client).StopGroup)
	},
}

var ctlRestartCmd = &cobra.Command{
	Use:   "restart [process...]",
	Short: "Restart processes",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAction(cmd, args, "restart", "restarted", (*ctl.Client).Restart, (*ctl.Client).RestartGroup)
	},
}

// runAction applies action to every target: a process name, a group as
// name:*, or all, which must be the only target and goes through the ordered
// bulk API. Every target is attempted; the command fails when any of them
// did (ADR-035, #213).
func runAction(cmd *cobra.Command, args []string, action, done string, one, group func(*ctl.Client, string) error) error {
	for _, name := range args {
		if name == "all" && len(args) > 1 {
			return usageError{errors.New("all cannot be combined with other names")}
		}
	}
	c := newCtlClient()
	c.SetActionTimeout(ctlActionTimeout)
	if len(args) == 1 && args[0] == "all" {
		return runAll(cmd, c, action)
	}
	failed := 0
	for _, name := range args {
		var err error
		if g, ok := strings.CutSuffix(name, ":*"); ok {
			err = group(c, g)
		} else {
			err = one(c, name)
		}
		if err != nil {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s: %s\n", name, err)
			failed++
			continue
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", name, done)
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d failed", failed, len(args))
	}
	return nil
}

// runAll prints one line per process of a bulk action and fails when any
// process failed.
func runAll(cmd *cobra.Command, c *ctl.Client, action string) error {
	results, err := c.All(action)
	if err != nil {
		return err
	}
	failed := 0
	for _, r := range results {
		if r.Status == "error" {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: error: %s\n", r.Name, r.Error)
			failed++
			continue
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", r.Name, r.Status)
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d failed", failed, len(results))
	}
	return nil
}

var ctlSignalCmd = &cobra.Command{
	Use:   "signal <signal> <process|group:*|all>",
	Short: "Send a signal to a process, a group or all processes",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newCtlClient()
		sig, name := args[0], args[1]
		if _, isGroup := strings.CutSuffix(name, ":*"); !isGroup && name != "all" {
			signaled, err := c.Signal(name, sig)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: signaled %s\n", name, signaled)
			return nil
		}
		// name:* and all signal every process they name, one line each;
		// the command fails when any did (FUNC-031, the start/stop rule).
		procs, err := c.Processes()
		if err != nil {
			return err
		}
		targets := ctl.MatchTargets(procs, []string{name})
		if len(targets) == 0 {
			if g, ok := strings.CutSuffix(name, ":*"); ok {
				return fmt.Errorf("no such group: %s", g)
			}
			return errors.New("no processes")
		}
		failed := 0
		for _, p := range targets {
			signaled, err := c.Signal(p.Name, sig)
			if err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s: %s\n", p.Name, err)
				failed++
				continue
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: signaled %s\n", p.Name, signaled)
		}
		if failed > 0 {
			return fmt.Errorf("%d of %d failed", failed, len(targets))
		}
		return nil
	},
}

var ctlStatusCmd = &cobra.Command{
	Use:   "status [process...]",
	Short: "Show process status",
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newCtlClient()
		return c.StatusWithOptions(args, ctl.StatusOptions{
			JSON:    ctlJSON,
			NoColor: ctlNoColor,
		}, cmd.OutOrStdout())
	},
}

var (
	tailFollow bool
	tailBytes  int
)

var ctlTailCmd = &cobra.Command{
	Use:   "tail <process> [stream]",
	Short: "Tail process log output",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newCtlClient()
		name := args[0]
		stream := "stdout"
		if len(args) > 1 {
			stream = args[1]
		}

		if tailFollow {
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
			defer cancel()
			return c.TailFollow(ctx, name, stream, cmd.OutOrStdout())
		}

		return c.Tail(name, stream, tailBytes, cmd.OutOrStdout())
	},
}

var ctlShutdownCmd = &cobra.Command{
	Use:   "shutdown",
	Short: "Shut the daemon down and wait until it has exited",
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newCtlClient()
		if err := c.Shutdown(); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "shutdown initiated")
		if ctlShutdownTimeout <= 0 {
			return nil
		}
		if !c.WaitGone(ctlShutdownTimeout) {
			return fmt.Errorf("daemon still running after %s", ctlShutdownTimeout)
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "shut down")
		return nil
	},
}

// printConfigWarnings prints the warnings a reload or preview reported for
// the new config (unknown keys, ignored settings) to stderr, one per line.
func printConfigWarnings(cmd *cobra.Command, result map[string]any) {
	warnings, _ := result["warnings"].([]any)
	for _, w := range warnings {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", w)
	}
}

var ctlReloadCmd = &cobra.Command{
	Use:   "reload",
	Short: "Reload daemon configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newCtlClient()
		result, err := c.Reload()
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "config reloaded: added=%s changed=%s removed=%s\n",
			nameList(result["added"]), nameList(result["changed"]), nameList(result["removed"]))
		printConfigWarnings(cmd, result)
		return nil
	},
}

// nameList formats a JSON list of names as [a b]; an empty or null list is
// [], not <nil>.
func nameList(v any) string {
	items, _ := v.([]any)
	names := make([]string, len(items))
	for i, item := range items {
		names[i] = fmt.Sprint(item)
	}
	return "[" + strings.Join(names, " ") + "]"
}

var ctlVersionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show remote daemon version",
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newCtlClient()
		result, err := c.Version()
		if err != nil {
			return err
		}
		for k, v := range result {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %v\n", k, v)
		}
		return nil
	},
}

var ctlPIDCmd = &cobra.Command{
	Use:   "pid [process|all]",
	Short: "Show the daemon's PID, a process's PID, or all running processes' PIDs",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newCtlClient()
		name := ""
		if len(args) > 0 {
			name = args[0]
		}
		// "all" lists every running process, as in supervisorctl.
		if name == "all" {
			procs, err := c.ProcessPIDs()
			if err != nil {
				return err
			}
			for _, p := range procs {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s %d\n", p.Name, p.PID)
			}
			return nil
		}
		pid, err := c.PID(name)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), pid)
		return nil
	},
}

var ctlHealthCmd = &cobra.Command{
	Use:   "health",
	Short: "Check daemon liveness",
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newCtlClient()
		status, err := c.Health()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		// "not_ready" prints as NOT READY.
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), strings.ToUpper(strings.ReplaceAll(status, "_", " ")))
		if status != "ok" {
			os.Exit(1)
		}
		return nil
	},
}

var ctlReadyProcesses []string

var ctlReadyCmd = &cobra.Command{
	Use:   "ready",
	Short: "Check daemon readiness",
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newCtlClient()
		status, err := c.Ready(ctlReadyProcesses)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		// "not_ready" prints as NOT READY.
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), strings.ToUpper(strings.ReplaceAll(status, "_", " ")))
		if status != "ready" {
			os.Exit(1)
		}
		return nil
	},
}

var ctlSendCmd = &cobra.Command{
	Use:   "send <process> <data>",
	Short: "Write data to a process stdin",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newCtlClient()
		return c.WriteStdin(args[0], args[1])
	},
}

var ctlRereadCmd = &cobra.Command{
	Use:   "reread [group...]",
	Short: "Preview config changes without applying",
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newCtlClient()
		result, err := c.Reread(args...)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "would apply: added=%s changed=%s removed=%s\n",
			nameList(result["added"]), nameList(result["changed"]), nameList(result["removed"]))
		printConfigWarnings(cmd, result)
		return nil
	},
}

var ctlUpdateCmd = &cobra.Command{
	Use:   "update [group...|all]",
	Short: "Reload config and apply its changes (all, or to the named groups)",
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newCtlClient()
		result, err := c.Reload(args...)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "updated: added=%s changed=%s removed=%s\n",
			nameList(result["added"]), nameList(result["changed"]), nameList(result["removed"]))
		printConfigWarnings(cmd, result)
		return nil
	},
}

var ctlAvailCmd = &cobra.Command{
	Use:   "avail",
	Short: "List the programs in the config file and whether they are in use",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		entries, err := newCtlClient().Avail()
		if err != nil {
			return err
		}
		for _, e := range entries {
			use, auto := "avail", "manual"
			if e.InUse {
				use = "in use"
			}
			if e.Autostart {
				auto = "auto"
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s %s %s %d\n", e.Name, use, auto, e.Priority)
		}
		return nil
	},
}

var (
	ctlAddReload  bool
	ctlRemoveStop bool
)

var ctlAddCmd = &cobra.Command{
	Use:   "add <group>",
	Short: "Add back a group removed at runtime",
	Long: "Add back a group that the loaded config defines and that was removed at runtime.\n" +
		"With --reload, load the config file first, as kahi ctl update does.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		c := newCtlClient()
		var reloaded map[string]any
		if ctlAddReload {
			result, err := c.Reload()
			if err != nil {
				return err
			}
			printConfigWarnings(cmd, result)
			reloaded = result
		}
		err := c.AddGroup(name)
		switch {
		case err == nil:
		case reloaded != nil && strings.HasPrefix(err.Error(), "group already active") && inList(reloaded["added"], name):
			// The reload itself added it back.
		case strings.HasPrefix(err.Error(), "no such group"):
			return fmt.Errorf("%w (not in the loaded config; run kahi ctl update, or kahi ctl add --reload)", err)
		default:
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: added\n", name)
		return nil
	},
}

var ctlRemoveCmd = &cobra.Command{
	Use:   "remove <group>",
	Short: "Remove a stopped group (its config stays)",
	Long: "Remove a group whose processes are all stopped; its config stays loaded.\n" +
		"With --stop, stop the group first and wait for it.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		err := newCtlClient().RemoveGroup(args[0], ctlRemoveStop)
		if err != nil && strings.Contains(err.Error(), "has running processes") {
			return fmt.Errorf("%w (or use kahi ctl remove --stop)", err)
		}
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: removed\n", args[0])
		return nil
	},
}

// inList reports whether a JSON array from the API contains name.
func inList(v any, name string) bool {
	items, _ := v.([]any)
	for _, item := range items {
		if fmt.Sprint(item) == name {
			return true
		}
	}
	return false
}

var ctlAttachCmd = &cobra.Command{
	Use:   "attach <process>",
	Short: "Attach to a process (stdin/stdout forwarding)",
	Args:  cobra.ExactArgs(1),
	Long: `Attach forwards your input to the process's stdin and its stdout and stderr
to yours. On a terminal, input is raw (^C, ^Z go to the process) and Ctrl+]
detaches; the process keeps running. With input from a pipe, the session ends
when the process exits, or 1 s after the input ends without further output.
Supervisorctl calls this command fg.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAttach(cmd, newCtlClient(), args[0])
	},
}

func init() {
	ctlCmd.PersistentFlags().StringVarP(&ctlSocket, "socket", "s", "", "Unix socket path (overrides config)")
	ctlCmd.PersistentFlags().StringVarP(&ctlConfig, "config", "c", "", "config file path (to resolve socket path)")
	ctlCmd.PersistentFlags().StringVar(&ctlAddr, "addr", "", "TCP address (host:port)")
	ctlCmd.PersistentFlags().StringVarP(&ctlUser, "username", "u", "", "HTTP Basic Auth username")
	ctlCmd.PersistentFlags().StringVarP(&ctlPass, "password", "p", "", "HTTP Basic Auth password")

	for _, cmd := range []*cobra.Command{ctlStartCmd, ctlStopCmd, ctlRestartCmd} {
		cmd.Flags().DurationVar(&ctlActionTimeout, "timeout", 0, "how long to wait for the daemon to finish (0: as long as it works)")
	}
	ctlAddCmd.Flags().BoolVar(&ctlAddReload, "reload", false, "load the config file first, as kahi ctl update does")
	ctlRemoveCmd.Flags().BoolVar(&ctlRemoveStop, "stop", false, "stop the group first and wait for it")
	ctlStatusCmd.Flags().BoolVar(&ctlNoColor, "no-color", false, "Disable color output")
	ctlShutdownCmd.Flags().DurationVar(&ctlShutdownTimeout, "timeout", 60*time.Second, "how long to wait for the daemon to exit (0: do not wait)")
	ctlStatusCmd.Flags().BoolVar(&ctlJSON, "json", false, "Output JSON")

	ctlTailCmd.Flags().BoolVarP(&tailFollow, "follow", "f", false, "Follow log output")
	ctlTailCmd.Flags().IntVar(&tailBytes, "bytes", 1600, "Number of bytes to tail")

	ctlReadyCmd.Flags().StringSliceVar(&ctlReadyProcesses, "process", nil, "Filter by process names")

	ctlCmd.AddCommand(
		ctlStartCmd, ctlStopCmd, ctlRestartCmd, ctlSignalCmd,
		ctlStatusCmd, ctlTailCmd,
		ctlShutdownCmd, ctlReloadCmd, ctlVersionCmd, ctlPIDCmd,
		ctlHealthCmd, ctlReadyCmd, ctlSendCmd, ctlRereadCmd, ctlAvailCmd,
		ctlUpdateCmd, ctlAddCmd, ctlRemoveCmd, ctlAttachCmd,
	)
	rootCmd.AddCommand(ctlCmd)
}
