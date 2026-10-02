package redditbrowser

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

func TestLoginWithChromiumAndManualVerification(t *testing.T) {
	if Executable() == "" {
		t.Skip("Chromium not installed")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/login/" {
			w.Write([]byte(`<input name="username"><input type="password"><button type="submit" onclick="if(document.querySelector('input[name=username]').value==='alice'&&document.querySelector('input[type=password]').value==='pw')location.href='/account/verify'">Login</button>`))
			return
		}
		if r.URL.Path == "/account/verify" {
			w.Write([]byte(`<input id="code"><button onclick="if(document.querySelector('#code').value==='123456')location.href='/'">Verify</button>`))
			return
		}
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "reddit_session", Value: "fake-cookie", Path: "/"})
		w.Write([]byte(`<div id="header-bottom-right"><span class="user"><a href="/user/alice">alice</a></span></div>`))
	}))
	defer srv.Close()
	oldSite, oldLogin := site, loginSite
	site, loginSite = srv.URL, srv.URL+"/login/"
	defer func() { site, loginSite = oldSite, oldLogin }()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	s, err := Open(ctx, "", true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Login(ctx, "alice", "pw"); err != nil {
		t.Fatal(err)
	}
	var current string
	var ready bool
	if err = s.wait(ctx, `location.pathname==='/account/verify'`, &ready, 3*time.Second, false); err != nil {
		t.Fatal("login was not submitted: ", err)
	}
	if _, err = s.Finish(ctx); err == nil {
		t.Fatal("verification was skipped")
	}
	// Finish must leave the challenge page intact.
	_ = s.run(ctx, chromedp.Location(&current))
	if !strings.HasSuffix(current, "/account/verify") {
		t.Fatal("Finish navigated away from verification")
	}
	if err = s.run(ctx, chromedp.Focus(`#code`, chromedp.ByQuery)); err != nil {
		t.Fatal(err)
	}
	if err = s.Input(ctx, Input{Kind: "text", Text: "123456"}); err != nil {
		t.Fatal(err)
	}
	if err = s.run(ctx, chromedp.Click(`button`, chromedp.ByQuery)); err != nil {
		t.Fatal(err)
	}
	if err = s.wait(ctx, `location.pathname==='/'`, &ready, 3*time.Second, false); err != nil {
		t.Fatal(err)
	}
	c, err := s.Finish(ctx)
	if err != nil || c.Username != "alice" || len(c.Cookies) != 1 {
		t.Fatalf("verified session not captured: %v", err)
	}
	view, err := s.Screenshot(ctx)
	if err != nil || view.Image == "" || view.Width != Width {
		t.Fatal("interactive screenshot missing")
	}
	if s.allowed("http://127.0.0.1:9999/secrets", network.ResourceTypeDocument, "GET") || s.allowed(srv.URL+"/r/test/submit", network.ResourceTypeDocument, "GET") || s.allowed(srv.URL+"/api/comment", network.ResourceTypeXHR, "POST") {
		t.Fatal("interactive login could navigate or publish outside authentication")
	}
}

func TestTargetsAndCredentialsAreRestricted(t *testing.T) {
	for _, raw := range []string{"https://evil.test/comments/abc", "https://reddit.com.evil.test/comments/abc", "https://user:private@reddit.com/comments/abc", "http://reddit.com/comments/abc", "https://reddit.com:9999/comments/abc", "https://reddit.com/login", "t3_abc/../../secret"} {
		if _, err := targetURL(raw); err == nil {
			t.Fatalf("unsafe target accepted: %q", raw)
		}
	}
	if _, err := targetURL("https://www.reddit.com/r/go/comments/abc/title/def/"); err != nil {
		t.Fatal(err)
	}
	if err := WithAccount(context.Background(), Credentials{}, func(*Session) error { return nil }); !errors.Is(err, ErrSession) {
		t.Fatal("empty credentials accepted")
	}
	for _, domain := range []string{"evilreddit.com", "reddit.com.evil.test", ".example.com"} {
		if cookieDomain(domain) {
			t.Fatal("unrelated cookie accepted")
		}
	}
}

func TestRecaptchaOriginsAreRestricted(t *testing.T) {
	s := &Session{login: true}
	for _, host := range []string{"www.google.com", "recaptcha.google.com", "www.recaptcha.net"} {
		if !s.allowed("https://"+host+"/recaptcha/api2/bframe", network.ResourceTypeDocument, "GET") {
			t.Fatalf("required reCAPTCHA frame blocked on %s", host)
		}
		if s.allowed("https://"+host+"/unrelated", network.ResourceTypeDocument, "GET") {
			t.Fatalf("unrelated Google page allowed on %s", host)
		}
	}
	for _, raw := range []string{"https://recaptcha.google.com.evil.test/recaptcha/api2/bframe", "http://recaptcha.google.com/recaptcha/api2/bframe", "https://recaptcha.google.com:9999/recaptcha/api2/bframe"} {
		if s.allowed(raw, network.ResourceTypeDocument, "GET") {
			t.Fatal("untrusted reCAPTCHA frame allowed")
		}
	}
}

func TestBlockedPageStopsLoginActions(t *testing.T) {
	if Executable() == "" {
		t.Skip("Chromium not installed")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<div id="heading"></div><p id="message"></p><button onclick="window.clicked=true">Continue</button>`))
	}))
	defer srv.Close()
	previous := site
	site = srv.URL
	defer func() { site = previous }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, err := Open(ctx, "", true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Navigate(ctx, srv.URL); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		heading, message string
		want             error
	}{
		{"whoa there, pardner!", "We've seen far too many requests come from your IP address recently.", ErrRateLimited},
		{"You've been blocked by network\nsecurity.", "File a ticket", ErrNetworkBlocked},
	} {
		t.Run(tc.want.Error(), func(t *testing.T) {
			args, _ := json.Marshal([]string{tc.heading, tc.message})
			js := `(()=>{const [heading,message]=` + string(args) + `;document.querySelector('#heading').innerText=heading;document.querySelector('#message').innerText=message;window.clicked=false;})()`
			if err := s.run(ctx, chromedp.Evaluate(js, nil)); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Screenshot(ctx); !errors.Is(err, tc.want) {
				t.Fatalf("screenshot did not report the block: %v", err)
			}
			if err := s.Input(ctx, Input{Kind: "key", Key: "Enter"}); !errors.Is(err, tc.want) {
				t.Fatalf("blocked page accepted input: %v", err)
			}
			if _, err := s.Finish(ctx); !errors.Is(err, tc.want) {
				t.Fatalf("finish did not report the block: %v", err)
			}
			var clicked bool
			if err := s.run(ctx, chromedp.Evaluate(`window.clicked`, &clicked)); err != nil || clicked {
				t.Fatal("an action was dispatched on the blocked page")
			}
		})
	}
}

func TestCaptchaBeforeLoginFormRetainsOnlyTemporaryCredentials(t *testing.T) {
	if Executable() == "" {
		t.Skip("Chromium not installed")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/login/" {
			w.Write([]byte(`<button id="challenge" onclick="document.body.innerHTML=document.querySelector('#login-form').innerHTML">Human confirmation</button><template id="login-form"><input name="username"><input type="password" oninput="setTimeout(()=>document.querySelector('button').disabled=false,150)"><button type="button" disabled onclick="if(document.querySelector('input[name=username]').value==='alice'&&document.querySelector('input[type=password]').value==='pw')location.href='/'">Log In</button></template>`))
			return
		}
		if r.URL.Path == "/" {
			http.SetCookie(w, &http.Cookie{Name: "reddit_session", Value: "fake", Path: "/"})
			w.Write([]byte(`<div id="header-bottom-right"><span class="user"><a href="/user/alice">alice</a></span></div>`))
		}
	}))
	defer srv.Close()
	previous, previousLogin := site, loginSite
	site, loginSite = srv.URL, srv.URL+"/login/"
	defer func() { site, loginSite = previous, previousLogin }()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, err := Open(ctx, "", true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Login(ctx, "alice", "pw"); err != nil {
		t.Fatal(err)
	}
	if s.loginPassword == "" {
		t.Fatal("credentials were discarded before the login form appeared")
	}
	if err = s.run(ctx, chromedp.Click(`#challenge`, chromedp.ByQuery)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Screenshot(ctx); err != nil {
		t.Fatal(err)
	}
	if s.loginPassword != "" || s.loginUser != "" {
		t.Fatal("credentials were retained after submitting the login form")
	}
	c, err := s.Finish(ctx)
	if err != nil || c.Username != "alice" {
		t.Fatalf("login did not continue after confirmation: %v", err)
	}
}

func TestHeadedBrowserWithoutDisplayReleasesSlot(t *testing.T) {
	if Executable() == "" {
		t.Skip("Chromium not installed")
	}
	t.Setenv("RADARO_BROWSER_HEADED", "true")
	t.Setenv("DISPLAY", "")
	before := len(slots)
	if _, err := Open(context.Background(), "", true); err == nil || !strings.Contains(err.Error(), "display") {
		t.Fatal("headed mode did not report missing display")
	}
	if len(slots) != before {
		t.Fatal("failed browser start leaked a slot")
	}
}
