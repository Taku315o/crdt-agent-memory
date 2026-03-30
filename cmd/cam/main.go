package main

import (
	"fmt"
	"os"
	"time"

	cam "crdt-agent-memory/internal/cam"

	"github.com/spf13/cobra"
)

func main() {
	if err := newRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	app := cam.NewApp()
	root := &cobra.Command{
		Use:           "cam",
		Short:         "Manage local CRDT Agent Memory profiles",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&app.Profile, "profile", "default", "profile name")
	root.AddCommand(newInitCommand(app))
	root.AddCommand(newUpCommand(app))
	root.AddCommand(newStatusCommand(app))
	root.AddCommand(newStopCommand(app))
	root.AddCommand(newLogsCommand(app))
	root.AddCommand(newDoctorCommand(app))
	root.AddCommand(newMCPCommand(app))
	return root
}

func newInitCommand(app *cam.App) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create or repair the local profile",
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := app.Init(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "profile=%s\nconfig=%s\ndata=%s\napi=%s\nsync=%s\n", result.Profile, result.ConfigPath, result.DataDir, result.APIBaseURL, result.SyncPublicURL)
			return nil
		},
	}
}

func newUpCommand(app *cam.App) *cobra.Command {
	var withSync bool
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Start background services for the profile",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := cam.UpOptions{WithSync: withSync}
			state, err := app.Up(cmd.Context(), opts)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "profile=%s\n", state.Profile)
			for _, svc := range state.Services {
				fmt.Fprintf(cmd.OutOrStdout(), "%s pid=%d log=%s\n", svc.Name, svc.PID, svc.LogPath)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&withSync, "with-sync", false, "start syncd in addition to memoryd and indexd")
	return cmd
}

func newStatusCommand(app *cam.App) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show service status for the profile",
		RunE: func(cmd *cobra.Command, args []string) error {
			status, err := app.Status(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "profile=%s\nconfig=%s\ndb=%s\n", status.Profile, status.ConfigPath, status.DatabasePath)
			if status.StartedAt != "" {
				if t, err := time.Parse(time.RFC3339, status.StartedAt); err == nil {
					fmt.Fprintf(cmd.OutOrStdout(), "started_at=%s\n", t.Format(time.RFC3339))
				}
			}
			if len(status.Services) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "services=stopped")
				return nil
			}
			for _, svc := range status.Services {
				fmt.Fprintf(cmd.OutOrStdout(), "%s pid=%d running=%t health=%s log=%s\n", svc.Name, svc.PID, svc.Running, svc.Health, svc.LogPath)
			}
			return nil
		},
	}
}

func newStopCommand(app *cam.App) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop background services for the profile",
		RunE: func(cmd *cobra.Command, args []string) error {
			stopped, err := app.Stop(cmd.Context())
			if err != nil {
				return err
			}
			if !stopped {
				fmt.Fprintf(cmd.OutOrStdout(), "profile=%s already stopped\n", app.Profile)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "profile=%s stopped\n", app.Profile)
			return nil
		},
	}
}

func newLogsCommand(app *cam.App) *cobra.Command {
	var service string
	var tail int
	var follow bool
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Show profile logs",
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.Logs(cmd.Context(), cmd.OutOrStdout(), cam.LogOptions{
				Service: service,
				Tail:    tail,
				Follow:  follow,
			})
		},
	}
	cmd.Flags().StringVar(&service, "service", "", "service name: memoryd, indexd, or syncd")
	cmd.Flags().IntVar(&tail, "tail", 40, "number of lines to show")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "follow appended log output")
	return cmd
}

func newDoctorCommand(app *cam.App) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Run profile diagnostics",
		RunE: func(cmd *cobra.Command, args []string) error {
			report, err := app.Doctor(cmd.Context())
			if err != nil {
				return err
			}
			for _, check := range report.Checks {
				fmt.Fprintf(cmd.OutOrStdout(), "%-18s %s", check.Name, check.Status)
				if check.Detail != "" {
					fmt.Fprintf(cmd.OutOrStdout(), " %s", check.Detail)
				}
				fmt.Fprintln(cmd.OutOrStdout())
			}
			if !report.OK {
				return fmt.Errorf("doctor found %d failing checks", report.Failures)
			}
			return nil
		},
	}
}

func newMCPCommand(app *cam.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Manage MCP client configuration for the profile",
	}
	cmd.AddCommand(newMCPPrintConfigCommand(app))
	cmd.AddCommand(newMCPInstallCommand(app))
	return cmd
}

func newMCPPrintConfigCommand(app *cam.App) *cobra.Command {
	var client string
	cmd := &cobra.Command{
		Use:   "print-config",
		Short: "Print MCP config for a client",
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := app.MCPPrintConfig(cmd.Context(), client)
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), out)
			return nil
		},
	}
	cmd.Flags().StringVar(&client, "client", "local", "target client: local, inspector, claude, or codex")
	return cmd
}

func newMCPInstallCommand(app *cam.App) *cobra.Command {
	var client string
	var createMissingDirs bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install MCP config into a client",
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := app.MCPInstall(cmd.Context(), cam.MCPInstallOptions{
				Client:            client,
				CreateMissingDirs: createMissingDirs,
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "updated %s\n", path)
			return nil
		},
	}
	cmd.Flags().StringVar(&client, "client", "local", "target client: local, inspector, claude, or codex")
	cmd.Flags().BoolVar(&createMissingDirs, "create-missing-dirs", false, "create client config directories if missing")
	return cmd
}
