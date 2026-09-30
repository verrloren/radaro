package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/verrloren/radaro/internal/client"
)

const defaultServer = "http://127.0.0.1:8042"

// serverURL is --server, else $RADARO_SERVER, else the saved session's, else
// a local `radaro serve`.
func serverURL(flag string) (string, error) {
	raw := flag
	if raw == "" {
		raw = os.Getenv("RADARO_SERVER")
	}
	if raw == "" {
		if s, _ := client.LoadSession(); s != nil {
			raw = s.Server
		}
	}
	if raw == "" {
		raw = defaultServer
	}
	return client.NormalizeServer(raw)
}

type signInFlags struct {
	server, email string
	passwordStdin bool
}

func (f *signInFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.server, "server", "", "Radaro server URL (default: $RADARO_SERVER, the last one used, or "+defaultServer+")")
	cmd.Flags().StringVar(&f.email, "email", "", "your email")
	cmd.Flags().BoolVar(&f.passwordStdin, "password-stdin", false, "read the password from stdin (for scripts and agents)")
}

func (a *app) registerCmd() *cobra.Command {
	var f signInFlags
	cmd := &cobra.Command{
		Use:   "register",
		Short: "Create an account on a Radaro server and sign in",
		Long: "Create an account and sign in. The first account on a server becomes its admin;\n" +
			"after that, sign-up works only when the server runs with RADARO_REGISTRATION=open.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.signIn(cmd, &f, true)
		},
	}
	f.register(cmd)
	return cmd
}

func (a *app) loginCmd() *cobra.Command {
	var f signInFlags
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in to a Radaro server; later commands act as this user",
		Long: "Sign in. The session is saved in your config directory (auth.json, readable only by you)\n" +
			"and renewed automatically. The password is never passed as a flag: type it at the prompt,\n" +
			"or pipe it with --password-stdin.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.signIn(cmd, &f, false)
		},
	}
	f.register(cmd)
	return cmd
}

func (a *app) signIn(cmd *cobra.Command, f *signInFlags, create bool) error {
	server, err := serverURL(f.server)
	if err != nil {
		return err
	}
	if client.Insecure(server) {
		stderr("! %s is not HTTPS: your password travels unencrypted.\n", server)
	}
	email := strings.TrimSpace(f.email)
	if email == "" {
		if email, err = prompt("Email: "); err != nil {
			return err
		}
	}
	password, err := readPassword(f.passwordStdin, create)
	if err != nil {
		return err
	}
	c := client.New(server)
	var sess *client.Session
	if create {
		sess, err = c.Register(cmd.Context(), email, password)
	} else {
		sess, err = c.Login(cmd.Context(), email, password)
	}
	if err != nil {
		return err
	}
	if err := sess.Save(); err != nil {
		return err
	}
	if a.jsonFlag {
		return printJSON(map[string]string{"server": sess.Server, "email": sess.Email})
	}
	verb := "signed in"
	if create {
		verb = "account created, signed in"
	}
	fmt.Printf("✓ %s as %s on %s\n", verb, sess.Email, sess.Server)
	return nil
}

func (a *app) logoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Sign out and forget the saved session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := client.FromSession()
			if errors.Is(err, client.ErrNotSignedIn) {
				fmt.Println("Not signed in.")
				return nil
			}
			if err != nil {
				return err
			}
			if err := c.Logout(cmd.Context()); err != nil {
				stderr("! the server did not confirm (%v); the local session is removed anyway\n", err)
			}
			// Keep only the server, so the next login knows where to go.
			if err := (&client.Session{Server: c.Server}).Save(); err != nil {
				return err
			}
			fmt.Printf("✓ signed out of %s\n", c.Server)
			return nil
		},
	}
}

func (a *app) whoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the signed-in user and server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.api()
			if err != nil {
				return err
			}
			var me struct {
				ID      int64  `json:"id"`
				Email   string `json:"email"`
				IsAdmin bool   `json:"is_admin"`
			}
			if err := c.Do(cmd.Context(), "GET", "/api/auth/me", nil, nil, &me); err != nil {
				return err
			}
			if a.jsonFlag {
				return printJSON(map[string]any{"server": c.Server, "id": me.ID, "email": me.Email, "is_admin": me.IsAdmin})
			}
			role := ""
			if me.IsAdmin {
				role = " (admin)"
			}
			fmt.Printf("%s%s on %s\n", me.Email, role, c.Server)
			return nil
		},
	}
}

// api is the signed-in client every data command uses.
func (a *app) api() (*client.Client, error) {
	return client.FromSession()
}

// stdin is shared so one prompt cannot swallow input meant for the next.
var stdin = bufio.NewReader(os.Stdin)

func prompt(label string) (string, error) {
	fmt.Fprint(os.Stderr, label)
	line, err := stdin.ReadString('\n')
	if err != nil && line == "" {
		return "", errors.New("no input")
	}
	return strings.TrimSpace(line), nil
}

// readPassword reads from stdin when asked or piped, else prompts without echo
// (twice when creating an account).
func readPassword(fromStdin, confirm bool) (string, error) {
	if fromStdin || !term.IsTerminal(int(os.Stdin.Fd())) {
		line, err := stdin.ReadString('\n')
		if err != nil && line == "" {
			return "", errors.New("no password on stdin")
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	read := func(label string) (string, error) {
		fmt.Fprint(os.Stderr, label)
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		return string(b), err
	}
	pw, err := read("Password: ")
	if err != nil {
		return "", err
	}
	if confirm {
		again, err := read("Repeat password: ")
		if err != nil {
			return "", err
		}
		if again != pw {
			return "", errors.New("the passwords do not match")
		}
	}
	return pw, nil
}
