package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/verrloren/radaro/internal/publish"
	"github.com/verrloren/radaro/internal/store"
)

func (a *app) connectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "connect",
		Short: "Connect a publishing account (credentials are stored in the local database)",
		Long: "Connect a publishing account. Secrets are read from a hidden prompt, or from stdin\n" +
			"when it is not a terminal (e.g. `echo $TOKEN | radaro connect devto`).\n" +
			"The dashboard (radaro serve → Setup) connects accounts too.",
	}

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
			return a.connect(cmd.Context(), "bluesky", publish.ConnectInput{Handle: bskyHandle, Service: bskyService, Secret: pass})
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
			return a.connect(cmd.Context(), "mastodon", publish.ConnectInput{Instance: mastoInstance, Secret: token})
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
			return a.connect(cmd.Context(), "devto", publish.ConnectInput{Secret: key})
		},
	}

	var clientID string
	var port int
	reddit := &cobra.Command{
		Use:   "reddit",
		Short: "Connect Reddit through your own Reddit app and a browser sign-in",
		Long: "Create an app at https://www.reddit.com/prefs/apps (type \"web app\" or \"installed app\")\n" +
			"with redirect URI http://127.0.0.1:8765/callback, then run\n" +
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
			r := &publish.Reddit{Version: version, Creds: publish.RedditCredentials{
				ClientID: clientID, ClientSecret: secret, RedirectURI: fmt.Sprintf("http://127.0.0.1:%d/callback", port),
			}}
			code, err := redditAuthorize(cmd.Context(), r.Creds, port)
			if err != nil {
				return err
			}
			if err := r.ExchangeCode(cmd.Context(), code); err != nil {
				return err
			}
			return a.saveAccount("reddit", r.Creds.Username, r.Creds)
		},
	}
	reddit.Flags().StringVar(&clientID, "client-id", "", "your Reddit app's client id")
	reddit.Flags().IntVar(&port, "port", 8765, "local port for the OAuth redirect")

	cmd.AddCommand(bsky, masto, devto, reddit)
	return cmd
}

func (a *app) connect(ctx context.Context, platform string, in publish.ConnectInput) error {
	handle, creds, err := publish.Connect(ctx, platform, in)
	if err != nil {
		return err
	}
	return a.saveAccount(platform, handle, creds)
}

func (a *app) saveAccount(platform, handle string, creds any) error {
	st, err := a.openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	acc, err := st.SaveAccount(0, platform, handle, creds)
	if err != nil {
		return err
	}
	_ = st.LogActivity(0, "account.connected", 0, platform+" "+handle)
	if a.jsonFlag {
		return printJSON(acc)
	}
	fmt.Printf("✓ connected %s as %s (account %d)\n", platform, handle, acc.ID)
	return nil
}

// redditAuthorize runs the local OAuth redirect: it opens the consent page and
// waits for Reddit to send the browser back to 127.0.0.1 with a code.
func redditAuthorize(ctx context.Context, creds publish.RedditCredentials, port int) (string, error) {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	state := hex.EncodeToString(buf)
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return "", fmt.Errorf("cannot listen on 127.0.0.1:%d: %w", port, err)
	}
	type result struct {
		code string
		err  error
	}
	done := make(chan result, 1)
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		var res result
		switch {
		case q.Get("state") != state:
			res.err = errors.New("OAuth state mismatch")
		case q.Get("error") != "":
			res.err = fmt.Errorf("reddit refused access: %s", q.Get("error"))
		default:
			res.code = q.Get("code")
		}
		if res.err != nil {
			fmt.Fprintln(w, "Radaro: "+res.err.Error())
		} else {
			fmt.Fprintln(w, "Radaro: Reddit connected. You can close this tab.")
		}
		select {
		case done <- res:
		default:
		}
	})}
	go srv.Serve(ln)
	defer srv.Close()

	authURL := publish.RedditAuthorizeURL(creds.ClientID, creds.RedirectURI, state)
	fmt.Println("Open this URL to approve access (waiting up to 5 minutes):")
	fmt.Println("  " + authURL)
	openBrowser(authURL)

	select {
	case res := <-done:
		return res.code, res.err
	case <-time.After(5 * time.Minute):
		return "", errors.New("timed out waiting for the Reddit redirect")
	case <-ctx.Done():
		return "", ctx.Err()
	}
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
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
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
		Use: "accounts", Short: "List connected publishing accounts", Args: cobra.NoArgs,
		RunE: a.withStore(func(st *store.Store, _ []string) error {
			accs, err := st.Accounts(0, "")
			if err != nil {
				return err
			}
			if a.jsonFlag {
				return printJSON(accs)
			}
			if len(accs) == 0 {
				fmt.Println("No accounts yet. Connect one with: radaro connect bluesky|mastodon|devto|reddit")
				return nil
			}
			fmt.Printf("%4s  %-9s %s\n", "ID", "PLATFORM", "HANDLE")
			for _, acc := range accs {
				fmt.Printf("%4d  %-9s %s\n", acc.ID, acc.Platform, acc.Handle)
			}
			return nil
		}),
	}
	cmd.AddCommand(&cobra.Command{
		Use: "remove <id>", Short: "Forget a connected account and its credentials", Args: cobra.ExactArgs(1),
		RunE: a.withStore(func(st *store.Store, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return errors.New("account id must be a number")
			}
			ok, err := st.DeleteAccount(0, id)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("account %d does not exist", id)
			}
			_ = st.LogActivity(0, "account.removed", 0, "account "+args[0])
			fmt.Printf("✓ removed account %d\n", id)
			return nil
		}),
	})
	return cmd
}
