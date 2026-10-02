// Package redditbrowser operates Reddit's website in isolated Chromium sessions.
package redditbrowser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/storage"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

const Width, Height = 1000, 720

var site = "https://old.reddit.com"
var loginSite = "https://www.reddit.com/login/"
var slots = make(chan struct{}, 4)
var headedLaunch sync.Mutex
var ErrSession = errors.New("Reddit session expired; reconnect this account")
var ErrChallenge = errors.New("Reddit requires browser confirmation; reconnect this account to continue")
var ErrRateLimited = errors.New("Reddit has rate-limited this IP; stop retrying and wait before reconnecting")
var ErrNetworkBlocked = errors.New("Reddit has blocked this connection with network security; sign-in cannot continue")

type Credentials struct {
	Username string                 `json:"username"`
	Cookies  []*network.CookieParam `json:"cookies"`
	Proxy    string                 `json:"proxy,omitempty"`
}

type Screen struct {
	Image  string `json:"image"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type Input struct {
	Kind  string  `json:"kind"` // click | text | key | scroll
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Text  string  `json:"text"`
	Key   string  `json:"key"`
	Delta float64 `json:"delta"`
}

// A login context lasts until completion, cancellation or the ten-minute TTL.
// Request contexts only bound individual commands, not the Chromium lifetime.
type Session struct {
	mu                       sync.Mutex
	ctx                      context.Context
	close                    func()
	proxy                    string
	login                    bool
	loginUser, loginPassword string
}

func Executable() string {
	if p := os.Getenv("RADARO_BROWSER_PATH"); p != "" {
		if found, err := exec.LookPath(p); err == nil {
			return found
		}
		return ""
	}
	for _, p := range []string{"chromium", "chromium-browser", "google-chrome", "chrome"} {
		if found, err := exec.LookPath(p); err == nil {
			return found
		}
	}
	return ""
}

func Open(parent context.Context, proxy string, login bool) (*Session, error) {
	path := Executable()
	if path == "" {
		return nil, errors.New("browser connection is unavailable: install Chromium on the Radaro server")
	}
	select {
	case slots <- struct{}{}:
	case <-parent.Done():
		return nil, errors.New("browser is busy; try again")
	}
	bridge, stopProxy, err := proxyBridge(proxy)
	if err != nil {
		<-slots
		return nil, err
	}
	opts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	opts = append(opts, chromedp.ExecPath(path), chromedp.WindowSize(Width, Height), chromedp.Flag("disable-dev-shm-usage", true), chromedp.Flag("force-webrtc-ip-handling-policy", "disable_non_proxied_udp"))
	if os.Getenv("RADARO_BROWSER_HEADED") == "true" {
		if os.Getenv("DISPLAY") == "" {
			stopProxy()
			<-slots
			return nil, errors.New("headed Chromium requires a display; start Radaro with xvfb-run")
		}
		// Serialize selection and launch so simultaneous sessions cannot choose
		// the same DevTools port. Each endpoint stays on loopback.
		headedLaunch.Lock()
		defer headedLaunch.Unlock()
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			stopProxy()
			<-slots
			return nil, errors.New("could not allocate a browser port")
		}
		port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
		ln.Close()
		opts = append(opts, chromedp.Flag("headless", false), chromedp.Flag("enable-automation", false), chromedp.Flag("remote-debugging-port", port), chromedp.Flag("remote-debugging-address", "127.0.0.1"))
	}
	if os.Getenv("RADARO_BROWSER_NO_SANDBOX") == "true" {
		opts = append(opts, chromedp.NoSandbox)
	}
	if bridge != "" {
		opts = append(opts, chromedp.ProxyServer(bridge), chromedp.Flag("proxy-bypass-list", "<-loopback>"))
	} else {
		opts = append(opts, chromedp.Flag("no-proxy-server", true))
	}
	alloc, cancelAlloc := chromedp.NewExecAllocator(parent, opts...)
	ctx, cancel := chromedp.NewContext(alloc, chromedp.WithErrorf(func(string, ...any) {}))
	var once sync.Once
	s := &Session{ctx: ctx, proxy: proxy, login: login}
	s.close = func() { once.Do(func() { cancel(); cancelAlloc(); stopProxy(); <-slots }) }
	if err = chromedp.Run(ctx, chromedp.EmulateViewport(Width, Height)); err != nil {
		s.close()
		return nil, errors.New("could not start Chromium")
	}
	// The interactive view only allows login. It cannot submit a post or expose
	// arbitrary internal URLs; publication remains exclusively in the outbox.
	chromedp.ListenTarget(ctx, func(ev any) {
		if e, ok := ev.(*fetch.EventRequestPaused); ok {
			go func() {
				c := cdp.WithExecutor(ctx, chromedp.FromContext(ctx).Target)
				if s.allowed(e.Request.URL, e.ResourceType, e.Request.Method) {
					_ = fetch.ContinueRequest(e.RequestID).Do(c)
				} else {
					_ = fetch.FailRequest(e.RequestID, network.ErrorReasonBlockedByClient).Do(c)
				}
			}()
		}
	})
	if err = chromedp.Run(ctx, fetch.Enable()); err != nil {
		s.close()
		return nil, errors.New("could not initialize Chromium")
	}
	return s, nil
}

func (s *Session) allowed(raw string, typ network.ResourceType, method string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	base, _ := url.Parse(site)
	if (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
		return false
	}
	h := u.Hostname()
	allowed := u.Host == base.Host || h == "reddit.com" || strings.HasSuffix(h, ".reddit.com") || h == "redditstatic.com" || strings.HasSuffix(h, ".redditstatic.com") || h == "redditmedia.com" || strings.HasSuffix(h, ".redditmedia.com") || (h == "www.google.com" || h == "recaptcha.google.com" || h == "www.recaptcha.net") && strings.HasPrefix(u.Path, "/recaptcha/") || h == "www.gstatic.com" || h == "hcaptcha.com" || strings.HasSuffix(h, ".hcaptcha.com") || h == "challenges.cloudflare.com"
	if !allowed {
		return false
	}
	if u.Host != base.Host && (u.Scheme != "https" || u.Port() != "") {
		return false
	}
	if s.login {
		if typ == network.ResourceTypeDocument && (h == "reddit.com" || strings.HasSuffix(h, ".reddit.com") || u.Host == base.Host) {
			return u.Path == "/" || strings.Contains(u.Path, "login") || strings.Contains(u.Path, "account") || strings.Contains(u.Path, "register") || strings.Contains(u.Path, "challenge")
		}
		if method != httpGet && method != "OPTIONS" {
			return strings.Contains(u.Path, "login") || strings.Contains(u.Path, "account") || strings.Contains(u.Path, "challenge") || strings.Contains(u.Path, "recaptcha") || strings.Contains(h, "hcaptcha")
		}
	}
	return true
}

const httpGet = "GET"

func (s *Session) Close() {
	s.close()
	s.mu.Lock()
	s.loginUser, s.loginPassword = "", ""
	s.mu.Unlock()
}

func (s *Session) run(ctx context.Context, actions ...chromedp.Action) error {
	c, cancel := context.WithCancel(s.ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	if err := chromedp.Run(c, actions...); err != nil {
		return browserOperationError(ctx, s.ctx, err)
	}
	return nil
}

func (s *Session) Navigate(ctx context.Context, target string) error {
	if err := retryNavigation(ctx, func() error { return s.run(ctx, chromedp.Navigate(target)) }); err != nil {
		return fmt.Errorf("loading Reddit page: %w", err)
	}
	return s.checkBlock(ctx)
}

// Chromium can interrupt the first page load while its network state settles.
// Repeat only that GET navigation, never browser input or form submission.
func retryNavigation(ctx context.Context, load func() error) error {
	err := load()
	if !errors.Is(err, errNetworkChanged) {
		return err
	}
	timer := time.NewTimer(250 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return browserOperationError(ctx, ctx, ctx.Err())
	case <-timer.C:
		if ctx.Err() != nil {
			return browserOperationError(ctx, ctx, ctx.Err())
		}
		return load()
	}
}

func (s *Session) checkBlock(ctx context.Context) error {
	var status string
	js := `(()=>{const text=(document.body?.innerText||'').toLowerCase().replace(/\s+/g,' ').trim();if(text.length>3000)return '';if(text.includes('whoa there')&&text.includes('far too many requests')&&text.includes('ip address'))return 'rate_limited';if(text.includes('blocked by network security')&&text.includes('file a ticket'))return 'network_blocked';return '';})()`
	if err := s.run(ctx, chromedp.Evaluate(js, &status)); err != nil {
		return err
	}
	switch status {
	case "rate_limited":
		return ErrRateLimited
	case "network_blocked":
		return ErrNetworkBlocked
	}
	return nil
}

// Only reads may be repeated when navigation replaces the JavaScript context.
func (s *Session) wait(ctx context.Context, expr string, out any, timeout time.Duration, function bool) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) && ctx.Err() == nil && s.ctx.Err() == nil {
		var action chromedp.Action = chromedp.Poll(expr, out, chromedp.WithPollingTimeout(time.Second))
		if function {
			action = chromedp.PollFunction(expr, out, chromedp.WithPollingTimeout(time.Second))
		}
		if err := s.run(ctx, action); err == nil {
			return nil
		}
		if err := s.run(ctx, chromedp.Sleep(100*time.Millisecond)); err != nil {
			return err
		}
	}
	return errors.New("Reddit did not confirm the browser action")
}

const deepQuery = `function all(root,selector){let out=[...root.querySelectorAll(selector)];for(const el of root.querySelectorAll('*'))if(el.shadowRoot)out.push(...all(el.shadowRoot,selector));return out;} function visible(el){return !!el && el.getBoundingClientRect().width>0 && el.getBoundingClientRect().height>0;}`

func (s *Session) Login(ctx context.Context, username, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.Navigate(ctx, loginSite); err != nil {
		return err
	}
	s.loginUser, s.loginPassword = username, password
	var visibleForm bool
	_ = s.run(ctx, chromedp.Poll(`(()=>{`+deepQuery+`return all(document,'input[type="password"]').some(visible)})()`, &visibleForm, chromedp.WithPollingTimeout(5*time.Second)))
	return s.fillLogin(ctx)
}

// A CAPTCHA can appear before the login form. Keep credentials only in memory
// until that form appears, then discard them immediately after submission.
func (s *Session) fillLogin(ctx context.Context) error {
	if err := s.checkBlock(ctx); err != nil {
		return err
	}
	if s.loginPassword == "" {
		return nil
	}
	args, _ := json.Marshal([]string{s.loginUser, s.loginPassword})
	js := `(()=>{` + deepQuery + `const [user,pass]=` + string(args) + `; const u=all(document,'input[name="username"],input[name="user"],#login-username').find(visible);const p=all(document,'input[type="password"]').find(visible);if(!u||!p)return false;for(const [el,val] of [[u,user],[p,pass]]){Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(el,val);el.dispatchEvent(new Event('input',{bubbles:true,composed:true}));el.dispatchEvent(new Event('change',{bubbles:true,composed:true}));}return true;})()`
	var filled bool
	// A changed login form is still usable manually in the interactive view.
	if err := s.run(ctx, chromedp.Evaluate(js, &filled)); err != nil {
		return err
	}
	if filled {
		s.loginUser, s.loginPassword = "", ""
		button := deepQuery + `function loginButton(){return all(document,'button').find(b=>visible(b)&&(b.type==='submit'||b.classList.contains('login')||/^(log\s*in|sign\s*in)$/i.test(b.innerText.trim())));}`
		var ready bool
		if err := s.run(ctx, chromedp.Poll(`(()=>{`+button+`const b=loginButton();return !!b&&!b.disabled;})()`, &ready, chromedp.WithPollingTimeout(5*time.Second))); err != nil {
			// Keep the filled form available for manual submission.
			return nil
		}
		// Submission is dispatched once; a navigation must never trigger a retry.
		if err := s.run(ctx, chromedp.Evaluate(`(()=>{`+button+`const b=loginButton();if(b&&!b.disabled)b.click();})()`, nil)); err != nil {
			return err
		}
		return s.run(ctx, chromedp.Sleep(500*time.Millisecond))
	}
	return nil
}

func (s *Session) Screenshot(ctx context.Context) (Screen, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fillLogin(ctx); err != nil {
		return Screen{}, err
	}
	var data []byte
	err := s.run(ctx, chromedp.CaptureScreenshot(&data))
	return Screen{Image: base64.StdEncoding.EncodeToString(data), Width: Width, Height: Height}, err
}

func (s *Session) Input(ctx context.Context, in Input) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkBlock(ctx); err != nil {
		return err
	}
	var action chromedp.Action
	switch in.Kind {
	case "click":
		if in.X < 0 || in.X >= Width || in.Y < 0 || in.Y >= Height {
			return errors.New("click is outside the browser view")
		}
		action = chromedp.MouseClickXY(in.X, in.Y)
	case "text":
		if len(in.Text) > 512 {
			return errors.New("input is too long")
		}
		action = chromedp.ActionFunc(func(c context.Context) error { return input.InsertText(in.Text).Do(c) })
	case "key":
		keys := map[string]string{"Enter": kb.Enter, "Tab": kb.Tab, "Backspace": kb.Backspace, "Escape": kb.Escape, "ArrowLeft": kb.ArrowLeft, "ArrowRight": kb.ArrowRight, "ArrowUp": kb.ArrowUp, "ArrowDown": kb.ArrowDown, "Delete": kb.Delete}
		key, ok := keys[in.Key]
		if !ok {
			return errors.New("unsupported key")
		}
		action = chromedp.KeyEvent(key)
	case "scroll":
		if in.Delta < -Height || in.Delta > Height {
			return errors.New("scroll is too large")
		}
		action = chromedp.ActionFunc(func(c context.Context) error {
			return input.DispatchMouseEvent(input.MouseWheel, Width/2, Height/2).WithDeltaY(in.Delta).WithDeltaX(0).Do(c)
		})
	default:
		return errors.New("unsupported browser input")
	}
	return s.run(ctx, action)
}

func (s *Session) Finish(ctx context.Context) (*Credentials, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkBlock(ctx); err != nil {
		return nil, err
	}
	var current string
	if err := s.run(ctx, chromedp.Location(&current)); err != nil {
		return nil, err
	}
	if strings.Contains(current, "login") {
		return nil, ErrChallenge
	}
	var existing []*network.Cookie
	if err := s.run(ctx, chromedp.ActionFunc(func(c context.Context) error { var e error; existing, e = storage.GetCookies().Do(c); return e })); err != nil {
		return nil, err
	}
	haveSession := false
	for _, cookie := range existing {
		if cookieDomain(cookie.Domain) && (cookie.Name == "reddit_session" || cookie.Name == "token_v2" && !strings.Contains(current, "account")) && cookie.Value != "" {
			haveSession = true
		}
	}
	if !haveSession {
		return nil, ErrChallenge
	}
	if err := s.Navigate(ctx, site+"/"); err != nil {
		return nil, err
	}
	username, err := s.identity(ctx)
	if err != nil {
		return nil, err
	}
	var cookies []*network.Cookie
	if err = s.run(ctx, chromedp.ActionFunc(func(c context.Context) error { var e error; cookies, e = storage.GetCookies().Do(c); return e })); err != nil {
		return nil, err
	}
	out := &Credentials{Username: username, Proxy: s.proxy, Cookies: []*network.CookieParam{}}
	for _, c := range cookies {
		if !cookieDomain(c.Domain) {
			continue
		}
		p := &network.CookieParam{Name: c.Name, Value: c.Value, Domain: c.Domain, Path: c.Path, Secure: c.Secure, HTTPOnly: c.HTTPOnly, SameSite: c.SameSite}
		if !c.Session {
			expiry := cdp.TimeSinceEpoch(time.Unix(int64(c.Expires), 0))
			p.Expires = &expiry
		}
		out.Cookies = append(out.Cookies, p)
	}
	if len(out.Cookies) == 0 {
		return nil, ErrSession
	}
	return out, nil
}

func (s *Session) identity(ctx context.Context) (string, error) {
	var name string
	if err := s.run(ctx, chromedp.Evaluate(`(()=>{const a=document.querySelector('#header-bottom-right .user > a[href*="/user/"]:not(.login-required)');return a?.textContent?.trim()||'';})()`, &name)); err != nil {
		return "", err
	}
	if name == "" {
		var loggedOut bool
		_ = s.run(ctx, chromedp.Evaluate(`!!document.querySelector('#header-bottom-right .user .login-required')`, &loggedOut))
		if loggedOut {
			return "", ErrSession
		}
		return "", ErrChallenge
	}
	return name, nil
}

func WithAccount(ctx context.Context, c Credentials, fn func(*Session) error) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if c.Username == "" || len(c.Cookies) == 0 {
		return ErrSession
	}
	s, err := Open(ctx, c.Proxy, false)
	if err != nil {
		return err
	}
	defer s.Close()
	for _, cookie := range c.Cookies {
		if cookie == nil || !cookieDomain(cookie.Domain) {
			return ErrSession
		}
	}
	if err = s.run(ctx, network.SetCookies(c.Cookies)); err != nil {
		return err
	}
	if err = s.Navigate(ctx, site+"/"); err != nil {
		return err
	}
	name, err := s.identity(ctx)
	if err != nil {
		return err
	}
	if !strings.EqualFold(name, c.Username) {
		return errors.New("Reddit browser identity does not match this account; reconnect it")
	}
	return fn(s)
}

func cookieDomain(domain string) bool {
	base, _ := url.Parse(site)
	return strings.TrimPrefix(domain, ".") == base.Hostname() || domain == "reddit.com" || strings.HasSuffix(domain, ".reddit.com")
}
