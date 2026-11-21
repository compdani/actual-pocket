// Package cli adds the Actual-specific maintenance commands to PocketBase's
// root command.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/config"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/store"
)

// IO is overridable in tests.
var (
	stdin  io.Reader = os.Stdin
	stdout io.Writer = os.Stdout

	// readPassword reads one password. Terminals disable echo; piped input
	// (tests, Docker secrets) is read as a line.
	readPassword = func(prompt string) (string, error) {
		fmt.Fprint(stdout, prompt)

		fd := int(os.Stdin.Fd())
		if !term.IsTerminal(fd) {
			// Read exactly one line without buffering the next one, so the
			// confirmation prompt sees its own line.
			var line strings.Builder
			buffer := make([]byte, 1)
			for {
				n, err := stdin.Read(buffer)
				if n > 0 {
					if buffer[0] == '\n' {
						break
					}
					if buffer[0] != '\r' {
						line.WriteByte(buffer[0])
					}
				}
				if err != nil {
					if errors.Is(err, io.EOF) {
						break
					}
					return "", err
				}
			}
			return line.String(), nil
		}

		value, err := term.ReadPassword(fd)
		fmt.Fprintln(stdout)
		if err != nil {
			return "", err
		}
		return string(value), nil
	}
)

// Register adds reset-password, create-owner and health-check to the app.
func Register(app *pocketbase.PocketBase, cfg *config.Config) {
	app.RootCmd.AddCommand(
		resetPasswordCommand(app),
		createOwnerCommand(app),
		healthCheckCommand(cfg),
	)
}

func resetPasswordCommand(app core.App) *cobra.Command {
	command := &cobra.Command{
		Use:          "reset-password <email>",
		Short:        "Reset the password of a user",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, args []string) error {
			return withApp(app, func() error {
				record, err := app.FindAuthRecordByEmail("users", args[0])
				if err != nil {
					return fmt.Errorf("user %q not found", args[0])
				}

				password, err := promptTwice()
				if err != nil {
					return err
				}

				store.SetPassword(record, password)
				if err := app.Save(record); err != nil {
					return err
				}

				fmt.Fprintf(stdout, "Password changed for %s\n", args[0])
				return nil
			})
		},
	}

	return command
}

func createOwnerCommand(app core.App) *cobra.Command {
	command := &cobra.Command{
		Use:          "create-owner <email>",
		Short:        "Create the server owner on a fresh install",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, args []string) error {
			return withApp(app, func() error {
				count, err := app.CountRecords("users")
				if err != nil {
					return err
				}
				if count > 0 {
					return errors.New("the server already has users")
				}

				password, err := promptTwice()
				if err != nil {
					return err
				}

				err = app.RunInTransaction(func(txApp core.App) error {
					count, err := txApp.CountRecords("users")
					if err != nil {
						return err
					}
					if count > 0 {
						return errors.New("the server already has users")
					}

					users, err := txApp.FindCollectionByNameOrId("users")
					if err != nil {
						return err
					}

					record := core.NewRecord(users)
					record.Set("email", args[0])
					record.Set("name", "")
					record.Set("role", "ADMIN")
					record.Set("enabled", true)
					record.Set("owner", true)
					record.Set("verified", true)
					store.SetPassword(record, password)

					return txApp.Save(record)
				})
				if err != nil {
					return err
				}

				fmt.Fprintf(stdout, "Owner created: %s\n", args[0])
				return nil
			})
		},
	}

	return command
}

func healthCheckCommand(cfg *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:          "health-check",
		Short:        "Check that a running server reports healthy",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			hostname := cfg.Hostname
			if hostname == "" || hostname == "::" || hostname == "0.0.0.0" {
				hostname = "localhost"
			}

			target := "http://" + net.JoinHostPort(hostname, strconv.Itoa(cfg.Port)) + "/health"
			client := &http.Client{Timeout: 10 * time.Second}

			response, err := client.Get(target)
			if err != nil {
				fmt.Fprintf(stdout, "Health check failed: %v\n", err)
				return fmt.Errorf("health check failed: %w", err)
			}
			defer response.Body.Close()

			var body struct {
				Status string `json:"status"`
			}
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				fmt.Fprintf(stdout, "Health check failed: %v\n", err)
				return fmt.Errorf("health check failed: %w", err)
			}

			if body.Status != "UP" {
				fmt.Fprintf(stdout, "Health check failed: Server responded to health check with status %s\n", body.Status)
				return fmt.Errorf("health check failed: status %s", body.Status)
			}

			return nil
		},
	}
}

// promptTwice asks for the same password twice.
func promptTwice() (string, error) {
	first, err := readPassword("Enter a new password: ")
	if err != nil {
		return "", err
	}
	if first == "" {
		return "", errors.New("password cannot be empty")
	}

	second, err := readPassword("Confirm password: ")
	if err != nil {
		return "", err
	}
	if first != second {
		return "", errors.New("passwords do not match")
	}

	return first, nil
}

// withApp bootstraps the app, applies migrations and runs fn.
func withApp(app core.App, fn func() error) error {
	bootstrapped := app.IsBootstrapped()
	if !bootstrapped {
		if err := app.Bootstrap(); err != nil {
			return err
		}
		defer app.ResetBootstrapState() //nolint:errcheck
	}

	if err := app.RunAllMigrations(); err != nil {
		return err
	}

	return fn()
}
