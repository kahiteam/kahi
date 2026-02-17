package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/kahidev/kahi/internal/config"
	"github.com/kahidev/kahi/internal/ctl"
	"github.com/spf13/cobra"
)

var (
	ctlSocket  string
	ctlConfig  string
	ctlAddr    string
	ctlUser    string
	ctlPass    string
	ctlNoColor bool
	ctlJSON    bool
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
	const defaultSocket = "/var/run/kahi.sock"

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
		return cfg.Server.Unix.File
	}
	return defaultSocket
}

var ctlStartCmd = &cobra.Command{
	Use:   "start [process...]",
	Short: "Start processes",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newCtlClient()
		for _, name := range args {
			if strings.HasSuffix(name, ":*") {
				group := strings.TrimSuffix(name, ":*")
				if err := c.StartGroup(group); err != nil {
					fmt.Fprintf(os.Stderr, "%s: %s\n", name, err)
					continue
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s: started\n", name)
			} else {
				if err := c.Start(name); err != nil {
					fmt.Fprintf(os.Stderr, "%s: %s\n", name, err)
					continue
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s: started\n", name)
			}
		}
		return nil
	},
}

var ctlStopCmd = &cobra.Command{
	Use:   "stop [process...]",
	Short: "Stop processes",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newCtlClient()
		for _, name := range args {
			if strings.HasSuffix(name, ":*") {
				group := strings.TrimSuffix(name, ":*")
				if err := c.StopGroup(group); err != nil {
					fmt.Fprintf(os.Stderr, "%s: %s\n", name, err)
					continue
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s: stopped\n", name)
			} else {
				if err := c.Stop(name); err != nil {
					fmt.Fprintf(os.Stderr, "%s: %s\n", name, err)
					continue
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s: stopped\n", name)
			}
		}
		return nil
	},
}

var ctlRestartCmd = &cobra.Command{
	Use:   "restart [process...]",
	Short: "Restart processes",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newCtlClient()
		for _, name := range args {
			if name == "all" {
				// Restart all requires listing first.
				var buf strings.Builder
				if err := c.Status(nil, true, &buf); err != nil {
					return err
				}
				// For now, restart individually.
				if err := c.Restart(name); err != nil {
					fmt.Fprintf(os.Stderr, "%s: %s\n", name, err)
					continue
				}
			} else if strings.HasSuffix(name, ":*") {
				group := strings.TrimSuffix(name, ":*")
				if err := c.RestartGroup(group); err != nil {
					fmt.Fprintf(os.Stderr, "%s: %s\n", name, err)
					continue
				}
			} else {
				if err := c.Restart(name); err != nil {
					fmt.Fprintf(os.Stderr, "%s: %s\n", name, err)
					continue
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: restarted\n", name)
		}
		return nil
	},
}

var ctlSignalCmd = &cobra.Command{
	Use:   "signal <signal> <process>",
	Short: "Send a signal to a process",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newCtlClient()
		sig, name := args[0], args[1]
		if err := c.Signal(name, sig); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s: signaled %s\n", name, sig)
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
	Short: "Initiate daemon shutdown",
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newCtlClient()
		if err := c.Shutdown(); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "shutdown initiated")
		return nil
	},
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
		fmt.Fprintf(cmd.OutOrStdout(), "config reloaded: added=%v changed=%v removed=%v\n",
			result["added"], result["changed"], result["removed"])
		return nil
	},
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
			fmt.Fprintf(cmd.OutOrStdout(), "%s: %v\n", k, v)
		}
		return nil
	},
}

var ctlPIDCmd = &cobra.Command{
	Use:   "pid [process]",
	Short: "Show daemon or process PID",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newCtlClient()
		name := ""
		if len(args) > 0 {
			name = args[0]
		}
		pid, err := c.PID(name)
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), pid)
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
		fmt.Fprintln(cmd.OutOrStdout(), strings.ToUpper(status))
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
		fmt.Fprintln(cmd.OutOrStdout(), strings.ToUpper(status))
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
	Use:   "reread",
	Short: "Preview config changes without applying",
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newCtlClient()
		result, err := c.Reread()
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "added=%v changed=%v removed=%v\n",
			result["added"], result["changed"], result["removed"])
		return nil
	},
}

var ctlUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Reload config and apply all changes",
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newCtlClient()
		result, err := c.Reload()
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "updated: added=%v changed=%v removed=%v\n",
			result["added"], result["changed"], result["removed"])
		return nil
	},
}

var ctlAddCmd = &cobra.Command{
	Use:   "add <group>",
	Short: "Activate a new group from config",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newCtlClient()
		// Reload first to pick up new config.
		if _, err := c.Reload(); err != nil {
			return err
		}
		// Start the group.
		if err := c.StartGroup(args[0]); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s: added and started\n", args[0])
		return nil
	},
}

var ctlRemoveCmd = &cobra.Command{
	Use:   "remove <group>",
	Short: "Stop and remove a group",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newCtlClient()
		// Stop the group first.
		if err := c.StopGroup(args[0]); err != nil {
			return fmt.Errorf("stop %s: %w", args[0], err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s: stopped and removed\n", args[0])
		return nil
	},
}

var ctlAttachCmd = &cobra.Command{
	Use:   "attach <process>",
	Short: "Attach to a process (stdin/stdout forwarding)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newCtlClient()
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		return c.Attach(ctx, args[0], os.Stdin, os.Stdout)
	},
}

func init() {
	ctlCmd.PersistentFlags().StringVarP(&ctlSocket, "socket", "s", "", "Unix socket path (overrides config)")
	ctlCmd.PersistentFlags().StringVarP(&ctlConfig, "config", "c", "", "config file path (to resolve socket path)")
	ctlCmd.PersistentFlags().StringVar(&ctlAddr, "addr", "", "TCP address (host:port)")
	ctlCmd.PersistentFlags().StringVarP(&ctlUser, "username", "u", "", "HTTP Basic Auth username")
	ctlCmd.PersistentFlags().StringVarP(&ctlPass, "password", "p", "", "HTTP Basic Auth password")

	ctlStatusCmd.Flags().BoolVar(&ctlNoColor, "no-color", false, "Disable color output")
	ctlStatusCmd.Flags().BoolVar(&ctlJSON, "json", false, "Output JSON")

	ctlTailCmd.Flags().BoolVarP(&tailFollow, "follow", "f", false, "Follow log output")
	ctlTailCmd.Flags().IntVar(&tailBytes, "bytes", 1600, "Number of bytes to tail")

	ctlReadyCmd.Flags().StringSliceVar(&ctlReadyProcesses, "process", nil, "Filter by process names")

	ctlCmd.AddCommand(
		ctlStartCmd, ctlStopCmd, ctlRestartCmd, ctlSignalCmd,
		ctlStatusCmd, ctlTailCmd,
		ctlShutdownCmd, ctlReloadCmd, ctlVersionCmd, ctlPIDCmd,
		ctlHealthCmd, ctlReadyCmd, ctlSendCmd, ctlRereadCmd,
		ctlUpdateCmd, ctlAddCmd, ctlRemoveCmd, ctlAttachCmd,
	)
	rootCmd.AddCommand(ctlCmd)
}
