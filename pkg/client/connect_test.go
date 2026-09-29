package client

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
)

func runServer(t *testing.T, opts *server.Options) *server.Server {
	t.Helper()
	opts.Host = "127.0.0.1"
	opts.Port = -1
	opts.JetStream = true
	opts.StoreDir = t.TempDir()
	opts.NoLog = true
	opts.NoSigs = true
	srv, err := server.NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats server not ready")
	}
	t.Cleanup(srv.Shutdown)
	return srv
}

// setHome points os.UserHomeDir at dir, which reads USERPROFILE on Windows and HOME elsewhere.
func setHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}

func selectContext(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "nats", "context.txt"), []byte(name+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestOptionsFromEnv(t *testing.T) {
	env := map[string]string{
		"NATS_CONTEXT":   "ctx",
		"NATS_URL":       "nats://env:4222",
		"NATS_CREDS":     "/creds",
		"NATS_NKEY":      "/nkey",
		"NATS_USER":      "user",
		"NATS_PASSWORD":  "pass",
		"NATS_TOKEN":     "token",
		"NATS_CERT":      "/cert",
		"NATS_KEY":       "/key",
		"NATS_CA":        "/ca",
		"NATS_JS_DOMAIN": "hub",
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	want := Options{
		Context: "ctx", URL: "nats://env:4222", Creds: "/creds", NKey: "/nkey", User: "user", Password: "pass",
		Token: "token", TLSCert: "/cert", TLSKey: "/key", TLSCA: "/ca", Domain: "hub",
	}
	if got := OptionsFromEnv(); got != want {
		t.Errorf("OptionsFromEnv() = %+v, want %+v", got, want)
	}
}

// Values given as flags or environment variables win, the context only fills what is left empty.
func TestResolvePrecedence(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	setHome(t, home)
	writeContext(t, dir, "prod", `{
		"url": "nats://prod:4222",
		"creds": "~/.nkeys/prod.creds",
		"nkey": "/abs/seed.nk",
		"token": "ctx-token",
		"cert": "~/tls/cert.pem",
		"key": "~/tls/key.pem",
		"ca": "/etc/ca.pem",
		"tls_first": true,
		"jetstream_api_prefix": "$JS.prod.API",
		"inbox_prefix": "_INBOX.prod"
	}`)
	selectContext(t, dir, "prod")

	opts, err := Resolve(Options{URL: "nats://flag:4222", Token: "flag-token"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Context != "prod" || opts.URL != "nats://flag:4222" || opts.Token != "flag-token" {
		t.Errorf("explicit values should win over the context: %+v", opts)
	}
	if want := filepath.Join(home, ".nkeys", "prod.creds"); opts.Creds != want {
		t.Errorf("expected ~ to expand to %q, got %q", want, opts.Creds)
	}
	if opts.TLSCert != filepath.Join(home, "tls", "cert.pem") || opts.TLSKey != filepath.Join(home, "tls", "key.pem") || opts.TLSCA != "/etc/ca.pem" {
		t.Errorf("unexpected tls paths: %+v", opts)
	}
	if opts.NKey != "/abs/seed.nk" || !opts.TLSFirst || opts.APIPrefix != "$JS.prod.API" || opts.InboxPrefix != "_INBOX.prod" {
		t.Errorf("context values not applied: %+v", opts)
	}
}

func TestResolveExplicitContextBeatsSelected(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeContext(t, dir, "selected", `{"url":"nats://selected:4222"}`)
	writeContext(t, dir, "explicit", `{}`)
	selectContext(t, dir, "selected")

	opts, err := Resolve(Options{Context: "explicit"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Context != "explicit" || opts.URL != defaultURL {
		t.Errorf("expected the explicit context with the default url, got %+v", opts)
	}
}

func TestResolveBrokenContexts(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeContext(t, dir, "broken", `{"url":`)

	if _, err := Resolve(Options{Context: "broken"}); err == nil || !strings.Contains(err.Error(), "parsing nats context") {
		t.Errorf("expected a parse error for an explicit broken context, got %v", err)
	}

	// A selected context that is missing or unreadable falls back to the defaults.
	for _, name := range []string{"broken", "gone"} {
		selectContext(t, dir, name)
		opts, err := Resolve(Options{})
		if err != nil || opts.URL != defaultURL || opts.Context != "" {
			t.Errorf("selected %q: expected a fallback to the default url, got %+v, %v", name, opts, err)
		}
		opts, err = Resolve(Options{URL: "nats://flag:4222"})
		if err != nil || opts.URL != "nats://flag:4222" {
			t.Errorf("selected %q: expected the given url to be kept, got %+v, %v", name, opts, err)
		}
	}
}

func TestConfigDirFallsBackToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	setHome(t, home)
	if got, want := configDir(), filepath.Join(home, ".config", "nats"); got != want {
		t.Errorf("configDir() = %q, want %q", got, want)
	}
	if got := expandHome("/abs/path"); got != "/abs/path" {
		t.Errorf("expected absolute paths to be kept, got %q", got)
	}
}

func TestConnectWithContextCredentials(t *testing.T) {
	srv := runServer(t, &server.Options{Users: []*server.User{{Username: "app", Password: "secret"}}})

	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeContext(t, dir, "local", `{"url":"`+srv.ClientURL()+`","user":"app","password":"secret"}`)
	selectContext(t, dir, "local")

	nc, js, info, err := Connect(Options{}, "tsui-test")
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	if info.Context != "local" || !strings.HasPrefix(info.URL, "nats://") || strings.Contains(info.URL, "secret") {
		t.Errorf("unexpected info: %+v", info)
	}
	if _, err := js.AccountInfo(context.Background()); err != nil {
		t.Errorf("expected a working jetstream handle: %v", err)
	}

	// A password given explicitly wins over the context and is rejected here.
	if _, _, _, err := Connect(Options{Password: "wrong"}, "tsui-test"); err == nil || !strings.Contains(err.Error(), "connecting to") {
		t.Errorf("expected an authorization error, got %v", err)
	}
}

func TestConnectWithoutContext(t *testing.T) {
	srv := runServer(t, &server.Options{Authorization: "s3cr3t"})
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	nc, _, info, err := Connect(Options{URL: srv.ClientURL(), Token: "s3cr3t"}, "tsui-test")
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	if info.Context != "-" {
		t.Errorf("expected '-' without a context, got %q", info.Context)
	}
	if _, _, _, err := Connect(Options{URL: srv.ClientURL()}, "tsui-test"); err == nil {
		t.Error("expected connecting without the token to fail")
	}
}

func TestConnectJetStreamDomainAndPrefix(t *testing.T) {
	srv := runServer(t, &server.Options{JetStreamDomain: "hub"})
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ctx := context.Background()

	nc, js, _, err := Connect(Options{URL: srv.ClientURL(), Domain: "hub"}, "tsui-test")
	if err != nil {
		t.Fatal(err)
	}
	info, err := js.AccountInfo(ctx)
	nc.Close()
	if err != nil || info.Domain != "hub" {
		t.Errorf("expected the account info of domain hub, got %+v, %v", info, err)
	}

	nc, js, _, err = Connect(Options{URL: srv.ClientURL(), APIPrefix: "$JS.hub.API"}, "tsui-test")
	if err != nil {
		t.Fatal(err)
	}
	_, err = js.AccountInfo(ctx)
	nc.Close()
	if err != nil {
		t.Errorf("expected the api prefix to reach jetstream: %v", err)
	}

	if _, _, _, err := Connect(Options{URL: srv.ClientURL(), Domain: "hub", APIPrefix: "$JS.hub.API"}, "tsui-test"); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected domain and api prefix to be rejected together, got %v", err)
	}
}

func TestConnectFlagDomainOverridesContextPrefix(t *testing.T) {
	srv := runServer(t, &server.Options{JetStreamDomain: "hub"})
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeContext(t, dir, "prefixed", `{"url":"`+srv.ClientURL()+`","jetstream_api_prefix":"$JS.other.API"}`)
	selectContext(t, dir, "prefixed")

	nc, _, _, err := Connect(Options{Domain: "hub"}, "tsui-test")
	if err != nil {
		t.Fatalf("expected the explicit domain to win over the context api prefix: %v", err)
	}
	nc.Close()
}

func TestConnectErrors(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if _, _, _, err := Connect(Options{Context: "missing"}, "tsui-test"); err == nil {
		t.Error("expected an error for a missing explicit context")
	}
	if _, _, _, err := Connect(Options{NKey: filepath.Join(t.TempDir(), "missing.nk")}, "tsui-test"); err == nil || !strings.Contains(err.Error(), "loading nkey") {
		t.Errorf("expected an nkey error, got %v", err)
	}
	if _, _, _, err := Connect(Options{URL: "nats://127.0.0.1:1"}, "tsui-test"); err == nil || !strings.Contains(err.Error(), "connecting to nats://127.0.0.1:1") {
		t.Errorf("expected a connection error naming the url, got %v", err)
	}
}

func TestNatsOptions(t *testing.T) {
	base, err := Options{}.natsOptions("x")
	if err != nil {
		t.Fatal(err)
	}
	all, err := Options{
		Creds: "/creds", User: "u", Password: "p", Token: "t", TLSCert: "/c", TLSKey: "/k", TLSCA: "/ca",
		TLSFirst: true, InboxPrefix: "_I",
	}.natsOptions("x")
	if err != nil {
		t.Fatal(err)
	}
	if len(all)-len(base) != 7 {
		t.Errorf("expected 7 extra options, got %d", len(all)-len(base))
	}
}
