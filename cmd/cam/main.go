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
	root.AddCommand(newPeerCommand(app))
	root.AddCommand(newSyncCommand(app))
	root.AddCommand(newProfileCommand(app))
	root.AddCommand(newConfigCommand(app))
	root.AddCommand(newCompletionCommand())
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
			var withSyncOpt *bool
			if cmd.Flags().Changed("with-sync") {
				withSyncOpt = &withSync
			}
			opts := cam.UpOptions{WithSync: withSyncOpt}
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
			fmt.Fprintf(
				cmd.OutOrStdout(),
				"profile=%s\nconfig=%s\nsettings=%s\ndb=%s\nsync_enabled=%t\n",
				status.Profile,
				status.ConfigPath,
				status.SettingsPath,
				status.DatabasePath,
				status.SyncEnabled,
			)
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

func newPeerCommand(app *cam.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "peer",
		Short: "Manage peer registry entries for the profile",
	}
	cmd.AddCommand(newPeerListCommand(app))
	cmd.AddCommand(newPeerAddCommand(app))
	cmd.AddCommand(newPeerRemoveCommand(app))
	return cmd
}

func newPeerListCommand(app *cam.App) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured peers",
		RunE: func(cmd *cobra.Command, args []string) error {
			peers, err := app.PeerList(cmd.Context())
			if err != nil {
				return err
			}
			if len(peers) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "peers=none")
				return nil
			}
			for _, peer := range peers {
				fmt.Fprintf(cmd.OutOrStdout(), "%s sync_url=%s namespaces=%v\n", peer.PeerID, peer.SyncURL, peer.NamespaceAllowlist)
			}
			return nil
		},
	}
}

func newPeerAddCommand(app *cam.App) *cobra.Command {
	var peerID string
	var displayName string
	var publicKey string
	var syncURL string
	var namespaces []string
	var discoveryProfile string
	var relayProfile string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add or update a peer registry entry",
		RunE: func(cmd *cobra.Command, args []string) error {
			entry, err := app.PeerAdd(cmd.Context(), cam.PeerAddOptions{
				PeerID:             peerID,
				DisplayName:        displayName,
				SigningPublicKey:   publicKey,
				SyncURL:            syncURL,
				NamespaceAllowlist: namespaces,
				DiscoveryProfile:   discoveryProfile,
				RelayProfile:       relayProfile,
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "peer=%s sync_url=%s namespaces=%v\n", entry.PeerID, entry.SyncURL, entry.NamespaceAllowlist)
			return nil
		},
	}
	cmd.Flags().StringVar(&peerID, "peer-id", "", "peer identifier")
	cmd.Flags().StringVar(&displayName, "display-name", "", "display name")
	cmd.Flags().StringVar(&publicKey, "public-key", "", "signing public key hex")
	cmd.Flags().StringVar(&syncURL, "sync-url", "", "peer sync base URL")
	cmd.Flags().StringSliceVar(&namespaces, "namespace", nil, "namespace allowlist entry; repeat or pass comma-separated values")
	cmd.Flags().StringVar(&discoveryProfile, "discovery-profile", "http-dev", "discovery profile")
	cmd.Flags().StringVar(&relayProfile, "relay-profile", "none", "relay profile")
	_ = cmd.MarkFlagRequired("peer-id")
	_ = cmd.MarkFlagRequired("public-key")
	_ = cmd.MarkFlagRequired("sync-url")
	return cmd
}

func newPeerRemoveCommand(app *cam.App) *cobra.Command {
	var peerID string
	cmd := &cobra.Command{
		Use:   "remove",
		Short: "Remove a peer registry entry",
		RunE: func(cmd *cobra.Command, args []string) error {
			removed, err := app.PeerRemove(cmd.Context(), peerID)
			if err != nil {
				return err
			}
			if !removed {
				fmt.Fprintf(cmd.OutOrStdout(), "peer=%s not found\n", peerID)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "peer=%s removed\n", peerID)
			return nil
		},
	}
	cmd.Flags().StringVar(&peerID, "peer-id", "", "peer identifier")
	_ = cmd.MarkFlagRequired("peer-id")
	return cmd
}

func newSyncCommand(app *cam.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Inspect sync status for the profile",
	}
	cmd.AddCommand(newSyncEnableCommand(app))
	cmd.AddCommand(newSyncDisableCommand(app))
	cmd.AddCommand(newSyncStatusCommand(app))
	return cmd
}

func newSyncEnableCommand(app *cam.App) *cobra.Command {
	return &cobra.Command{
		Use:   "enable",
		Short: "Enable sync by default for the profile",
		RunE: func(cmd *cobra.Command, args []string) error {
			settings, err := app.SetSyncEnabled(cmd.Context(), true)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "sync_enabled=%t\n", settings.SyncEnabled)
			return nil
		},
	}
}

func newSyncDisableCommand(app *cam.App) *cobra.Command {
	return &cobra.Command{
		Use:   "disable",
		Short: "Disable sync by default for the profile",
		RunE: func(cmd *cobra.Command, args []string) error {
			settings, err := app.SetSyncEnabled(cmd.Context(), false)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "sync_enabled=%t\n", settings.SyncEnabled)
			return nil
		},
	}
}

func newSyncStatusCommand(app *cam.App) *cobra.Command {
	var namespace string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show sync status for a namespace via memoryd",
		RunE: func(cmd *cobra.Command, args []string) error {
			settings, err := app.SyncSettings(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "sync_enabled=%t\n", settings.SyncEnabled)
			status, err := app.SyncStatus(cmd.Context(), namespace)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "namespace=%s state=%s schema_fenced=%t\n", status.Namespace, status.State, status.SchemaFenced)
			for _, peer := range status.Peers {
				lastError := ""
				if peer.LastError != nil {
					lastError = *peer.LastError
				}
				fmt.Fprintf(
					cmd.OutOrStdout(),
					"%s last_success_at_ms=%d schema_fenced=%t last_transport=%s last_path_type=%s last_error=%s\n",
					peer.PeerID,
					peer.LastSuccessAtMS,
					peer.SchemaFenced,
					peer.LastTransport,
					peer.LastPathType,
					lastError,
				)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&namespace, "namespace", "", "namespace to inspect; defaults to the first configured namespace")
	return cmd
}

func newProfileCommand(app *cam.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Manage local profiles",
	}
	cmd.AddCommand(newProfileListCommand(app))
	cmd.AddCommand(newProfileCreateCommand(app))
	cmd.AddCommand(newProfileRemoveCommand(app))
	return cmd
}

func newProfileListCommand(app *cam.App) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available profiles",
		RunE: func(cmd *cobra.Command, args []string) error {
			profiles, err := app.ProfileList(cmd.Context())
			if err != nil {
				return err
			}
			if len(profiles) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "profiles=none")
				return nil
			}
			for _, profile := range profiles {
				fmt.Fprintf(cmd.OutOrStdout(), "%s config=%s settings=%s data=%s\n", profile.Name, profile.ConfigPath, profile.SettingsPath, profile.DataDir)
			}
			return nil
		},
	}
}

func newProfileCreateCommand(app *cam.App) *cobra.Command {
	return &cobra.Command{
		Use:   "create",
		Short: "Create the selected profile",
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := app.ProfileCreate(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "profile=%s\nconfig=%s\ndata=%s\n", result.Profile, result.ConfigPath, result.DataDir)
			return nil
		},
	}
}

func newProfileRemoveCommand(app *cam.App) *cobra.Command {
	return &cobra.Command{
		Use:   "remove",
		Short: "Remove the selected profile",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := app.ProfileRemove(cmd.Context()); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "profile=%s removed\n", app.Profile)
			return nil
		},
	}
}

func newConfigCommand(app *cam.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect profile config files",
	}
	cmd.AddCommand(newConfigPathCommand(app))
	cmd.AddCommand(newConfigShowCommand(app))
	return cmd
}

func newConfigPathCommand(app *cam.App) *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Show config and settings paths for the profile",
		RunE: func(cmd *cobra.Command, args []string) error {
			paths, err := app.ConfigPaths(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "config=%s\nsettings=%s\n", paths.ConfigPath, paths.SettingsPath)
			return nil
		},
	}
}

func newConfigShowCommand(app *cam.App) *cobra.Command {
	var target string
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Print config.yaml or settings.json for the profile",
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := app.ConfigShow(cmd.Context(), target)
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), out)
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", "config", "target file: config or settings")
	return cmd
}

func newCompletionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion",
		Short: "Generate shell completion scripts",
	}
	cmd.AddCommand(newCompletionSubcommand("bash"))
	cmd.AddCommand(newCompletionSubcommand("zsh"))
	cmd.AddCommand(newCompletionSubcommand("fish"))
	cmd.AddCommand(newCompletionSubcommand("powershell"))
	return cmd
}

func newCompletionSubcommand(shell string) *cobra.Command {
	return &cobra.Command{
		Use:   shell,
		Short: fmt.Sprintf("Generate %s completion script", shell),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := cmd.Root()
			switch shell {
			case "bash":
				return root.GenBashCompletion(cmd.OutOrStdout())
			case "zsh":
				return root.GenZshCompletion(cmd.OutOrStdout())
			case "fish":
				return root.GenFishCompletion(cmd.OutOrStdout(), true)
			case "powershell":
				return root.GenPowerShellCompletionWithDesc(cmd.OutOrStdout())
			default:
				return fmt.Errorf("unsupported shell %q", shell)
			}
		},
	}
}
