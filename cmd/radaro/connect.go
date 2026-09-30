package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/verrloren/radaro/internal/client"
	"github.com/verrloren/radaro/internal/store"
)

func (a *app) connectCmd() *cobra.Command {
	var project int64
	cmd := &cobra.Command{
		Use:   "connect",
		Short: "Connect a publishing account (stored on the server, readable only by you)",
		Long: "Connect a publishing account. Secrets are read from a hidden prompt, or from stdin\n" +
			"when it is not a terminal (e.g. `echo $TOKEN | radaro connect devto`).\n" +
			"With --project the account is also bound to that project. The dashboard connects accounts too.",
	}
	cmd.PersistentFlags().Int64Var(&project, "project", 0, "also make this project publish with the account")

	var bskyHandle, bskyService string
	bsky := &cobra.Command{
		Use: "bluesky", Short: "Connect Bluesky with an app password (Settings → Privacy and security → App passwords)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if bskyHandle == "" {
				return errors.New("--handle is required (e.g. you.bsky.social)")
			}
			pass, err := readSecret("App password: ")
			if err != nil {
				return err
			}
			return a.connect(cmd.Context(), map[string]any{"platform": "bluesky", "handle": bskyHandle, "service": bskyService, "secret": pass}, project)
		},
	}
	bsky.Flags().StringVar(&bskyHandle, "handle", "", "your handle or email")
	bsky.Flags().StringVar(&bskyService, "service", "https://bsky.social", "your PDS")

	var mastoInstance string
	masto := &cobra.Command{
		Use: "mastodon", Short: "Connect Mastodon with an access token (Preferences → Development → New application, scopes read + write:statuses)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			token, err := readSecret("Access token: ")
			if err != nil {
				return err
			}
			return a.connect(cmd.Context(), map[string]any{"platform": "mastodon", "instance": mastoInstance, "secret": token}, project)
		},
	}
	masto.Flags().StringVar(&mastoInstance, "instance", "mastodon.social", "your instance")

	devto := &cobra.Command{
		Use: "devto", Short: "Connect Dev.to with an API key (Settings → Extensions → DEV Community API Keys)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			key, err := readSecret("API key: ")
			if err != nil {
				return err
			}
			return a.connect(cmd.Context(), map[string]any{"platform": "devto", "secret": key}, project)
		},
	}

	var clientID string
	reddit := &cobra.Command{
		Use:   "reddit",
		Short: "Connect Reddit through your own Reddit app and a browser sign-in",
		Long: "Create an app at https://www.reddit.com/prefs/apps (type \"web app\" or \"installed app\")\n" +
			"with the redirect URI <server>/oauth/reddit/callback (this command prints it), then run\n" +
			"  radaro connect reddit --client-id <id>\n" +
			"You will be asked for the app secret (empty for an installed app) and sent to Reddit to approve access.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if clientID == "" {
				return errors.New("--client-id is required")
			}
			secret, err := readSecret("Client secret (empty for an installed app): ")
			if err != nil && !errors.Is(err, errEmptySecret) {
				return err
			}
			return a.connectReddit(cmd.Context(), clientID, secret, project)
		},
	}
	reddit.Flags().StringVar(&clientID, "client-id", "", "your Reddit app's client id")

	cmd.AddCommand(bsky, masto, devto, reddit)
	return cmd
}

// connect sends the key to the server, which checks it with the platform.
func (a *app) connect(ctx context.Context, body map[string]any, project int64) error {
	c, err := a.api()
	if err != nil {
		return err
	}
	if project != 0 {
		body["project_id"] = project
	}
	var acc store.Account
	if err := c.Do(ctx, "POST", "/api/accounts", nil, body, &acc); err != nil {
		return err
	}
	if a.jsonFlag {
		return printJSON(acc)
	}
	fmt.Printf("✓ connected %s as %s (account %d)\n", acc.Platform, acc.Handle, acc.ID)
	if project != 0 {
		fmt.Printf("  project %d now publishes with it\n", project)
	}
	return nil
}

// connectReddit starts the sign-in on the server, which receives Reddit's
// redirect, and waits until the new account shows up.
func (a *app) connectReddit(ctx context.Context, clientID, secret string, project int64) error {
	c, err := a.api()
	if err != nil {
		return err
	}
	before, err := accountIDs(ctx, c)
	if err != nil {
		return err
	}
	body := map[string]any{"client_id": clientID, "client_secret": secret}
	if project != 0 {
		body["project_id"] = project
	}
	var res struct {
		AuthorizeURL string `json:"authorize_url"`
		RedirectURI  string `json:"redirect_uri"`
	}
	if err := c.Do(ctx, "POST", "/api/accounts/reddit/authorize", nil, body, &res); err != nil {
		return err
	}
	fmt.Printf("Your Reddit app's redirect URI must be:\n  %s\n", res.RedirectURI)
	fmt.Println("Open this URL to approve access (waiting up to 10 minutes):")
	fmt.Println("  " + res.AuthorizeURL)
	openBrowser(res.AuthorizeURL)
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
		var list struct {
			Accounts []store.Account `json:"accounts"`
		}
		if err := c.Do(ctx, "GET", "/api/accounts", nil, nil, &list); err != nil {
			return err
		}
		for _, acc := range list.Accounts {
			if acc.Platform == "reddit" && !before[acc.ID] {
				if a.jsonFlag {
					return printJSON(acc)
				}
				fmt.Printf("✓ connected reddit as %s (account %d)\n", acc.Handle, acc.ID)
				return nil
			}
		}
	}
	return errors.New("timed out waiting for the Reddit sign-in; if the browser showed an error, fix it and try again")
}

func accountIDs(ctx context.Context, c *client.Client) (map[int64]bool, error) {
	var list struct {
		Accounts []store.Account `json:"accounts"`
	}
	if err := c.Do(ctx, "GET", "/api/accounts", nil, nil, &list); err != nil {
		return nil, err
	}
	ids := map[int64]bool{}
	for _, acc := range list.Accounts {
		ids[acc.ID] = true
	}
	return ids, nil
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

var errEmptySecret = errors.New("empty secret")

// readSecret prompts without echo on a terminal, or reads one line from piped stdin.
func readSecret(prompt string) (string, error) {
	var value string
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprint(os.Stderr, prompt)
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		value = string(b)
	} else {
		line, err := stdin.ReadString('\n')
		if err != nil && line == "" {
			return "", errEmptySecret
		}
		value = line
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errEmptySecret
	}
	return value, nil
}

func (a *app) accountsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "accounts", Short: "List your connected publishing accounts", Args: cobra.NoArgs,
		RunE: a.apiCmd(func(ctx context.Context, c *client.Client, _ []string) error {
			var list struct {
				Accounts []store.Account `json:"accounts"`
			}
			if err := c.Do(ctx, "GET", "/api/accounts", nil, nil, &list); err != nil {
				return err
			}
			if a.jsonFlag {
				return printJSON(list.Accounts)
			}
			if len(list.Accounts) == 0 {
				fmt.Println("No accounts yet. Connect one with: radaro connect bluesky|mastodon|devto|reddit")
				return nil
			}
			fmt.Printf("%4s  %-9s %s\n", "ID", "PLATFORM", "HANDLE")
			for _, acc := range list.Accounts {
				fmt.Printf("%4d  %-9s %s\n", acc.ID, acc.Platform, acc.Handle)
			}
			return nil
		}),
	}
	cmd.AddCommand(&cobra.Command{
		Use: "remove <id>", Short: "Forget a connected account and its credentials", Args: cobra.ExactArgs(1),
		RunE: a.apiCmd(func(ctx context.Context, c *client.Client, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil || id < 1 {
				return errors.New("account id must be a positive number")
			}
			if err := c.Do(ctx, "DELETE", "/api/accounts/"+args[0], nil, nil, nil); err != nil {
				return err
			}
			fmt.Printf("✓ removed account %d\n", id)
			return nil
		}),
	})
	return cmd
}
