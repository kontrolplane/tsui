package client

import (
	"os"
	"path/filepath"
	"testing"
)

func writeContext(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "nats", "context"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nats", "context", name+".json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestResolveDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	opts, err := Resolve(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if opts.URL != defaultURL || opts.Context != "" {
		t.Errorf("unexpected options: %+v", opts)
	}
}

func TestResolveSelectedContext(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeContext(t, dir, "staging", `{"url":"nats://staging:4222","user":"app","password":"secret","jetstream_domain":"hub"}`)
	if err := os.WriteFile(filepath.Join(dir, "nats", "context.txt"), []byte("staging\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	opts, err := Resolve(Options{User: "override"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Context != "staging" || opts.URL != "nats://staging:4222" || opts.Domain != "hub" {
		t.Errorf("context not applied: %+v", opts)
	}
	if opts.User != "override" || opts.Password != "secret" {
		t.Errorf("explicit options should win over context values: %+v", opts)
	}
}

func TestResolveExplicitContextMissing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if _, err := Resolve(Options{Context: "missing"}); err == nil {
		t.Error("expected error for a missing explicit context")
	}
	if _, err := Resolve(Options{Context: "../etc/passwd"}); err == nil {
		t.Error("expected error for a context name with path separators")
	}
}

func TestResolveExplicitJetStreamSettingsReplaceContext(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeContext(t, dir, "prefixed", `{"jetstream_api_prefix":"$JS.other.API"}`)
	writeContext(t, dir, "domain", `{"jetstream_domain":"hub"}`)

	opts, err := Resolve(Options{Context: "prefixed", Domain: "hub"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Domain != "hub" || opts.APIPrefix != "" {
		t.Errorf("expected the explicit domain to replace the context api prefix: %+v", opts)
	}

	opts, err = Resolve(Options{Context: "domain", APIPrefix: "$JS.x.API"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Domain != "" || opts.APIPrefix != "$JS.x.API" {
		t.Errorf("expected the explicit api prefix to replace the context domain: %+v", opts)
	}
}

func TestNatsOptionsTLSPair(t *testing.T) {
	for _, o := range []Options{{TLSCert: "/c"}, {TLSKey: "/k"}} {
		if _, err := o.natsOptions("x"); err == nil {
			t.Errorf("expected an error for %+v", o)
		}
	}
}

func TestRedactURLs(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"nats://localhost:4222", "nats://localhost:4222"},
		{"nats://s3cr3t@localhost:4222", "nats://xxxxx@localhost:4222"},
		{"nats://app:secret@localhost:4222", "nats://app:xxxxx@localhost:4222"},
		{"tls://app:p@ss@host:4222/path", "tls://app:xxxxx@host:4222/path"},
		{"token@host:4222", "xxxxx@host:4222"},
		{"nats://a:b@one:4222, nats://tok@two:4222,nats://three:4222", "nats://a:xxxxx@one:4222,nats://xxxxx@two:4222,nats://three:4222"},
	}
	for _, tt := range tests {
		if got := redactURLs(tt.in); got != tt.want {
			t.Errorf("redactURLs(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
