package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const defaultURL = nats.DefaultURL

// Options holds everything needed to connect to a NATS server. Values set here
// take precedence over the ones found in a nats cli context.
type Options struct {
	Context     string
	URL         string
	Creds       string
	NKey        string
	User        string
	Password    string
	Token       string
	TLSCert     string
	TLSKey      string
	TLSCA       string
	TLSFirst    bool
	Domain      string
	APIPrefix   string
	InboxPrefix string
}

// Info holds connection information for display.
type Info struct {
	Context string
	URL     string
}

// contextSettings mirrors the subset of the nats cli context file format that tsui understands.
// docs: https://github.com/nats-io/jsm.go/blob/main/natscontext/context.go
type contextSettings struct {
	URL         string `json:"url"`
	Token       string `json:"token"`
	User        string `json:"user"`
	Password    string `json:"password"`
	Creds       string `json:"creds"`
	NKey        string `json:"nkey"`
	Cert        string `json:"cert"`
	Key         string `json:"key"`
	CA          string `json:"ca"`
	TLSFirst    bool   `json:"tls_first"`
	Domain      string `json:"jetstream_domain"`
	APIPrefix   string `json:"jetstream_api_prefix"`
	InboxPrefix string `json:"inbox_prefix"`
}

// OptionsFromEnv returns options populated from the environment variables also used by the nats cli.
func OptionsFromEnv() Options {
	return Options{
		Context:  os.Getenv("NATS_CONTEXT"),
		URL:      os.Getenv("NATS_URL"),
		Creds:    os.Getenv("NATS_CREDS"),
		NKey:     os.Getenv("NATS_NKEY"),
		User:     os.Getenv("NATS_USER"),
		Password: os.Getenv("NATS_PASSWORD"),
		Token:    os.Getenv("NATS_TOKEN"),
		TLSCert:  os.Getenv("NATS_CERT"),
		TLSKey:   os.Getenv("NATS_KEY"),
		TLSCA:    os.Getenv("NATS_CA"),
		Domain:   os.Getenv("NATS_JS_DOMAIN"),
	}
}

func configDir() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "nats")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "nats")
}

// selectedContext returns the context selected with `nats context select`, if any.
func selectedContext(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "context.txt"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func loadContext(dir, name string) (contextSettings, error) {
	var s contextSettings
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return s, fmt.Errorf("invalid context name %q", name)
	}
	b, err := os.ReadFile(filepath.Join(dir, "context", name+".json"))
	if err != nil {
		return s, fmt.Errorf("loading nats context %q: %w", name, err)
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("parsing nats context %q: %w", name, err)
	}
	return s, nil
}

// Resolve merges the given options with the explicitly requested or currently selected nats cli context.
func Resolve(opts Options) (Options, error) {
	dir := configDir()

	name := opts.Context
	explicit := name != ""
	if !explicit {
		name = selectedContext(dir)
	}
	if name == "" {
		if opts.URL == "" {
			opts.URL = defaultURL
		}
		return opts, nil
	}

	ctx, err := loadContext(dir, name)
	if err != nil {
		if explicit {
			return opts, err
		}
		fmt.Fprintf(os.Stderr, "warning: ignoring selected nats context %q: %v\n", name, err)
		if opts.URL == "" {
			opts.URL = defaultURL
		}
		return opts, nil
	}

	opts.Context = name
	fill := func(dst *string, src string) {
		if *dst == "" {
			*dst = expandHome(src)
		}
	}
	fill(&opts.URL, ctx.URL)
	fill(&opts.Creds, ctx.Creds)
	fill(&opts.NKey, ctx.NKey)
	fill(&opts.User, ctx.User)
	fill(&opts.Password, ctx.Password)
	fill(&opts.Token, ctx.Token)
	fill(&opts.TLSCert, ctx.Cert)
	fill(&opts.TLSKey, ctx.Key)
	fill(&opts.TLSCA, ctx.CA)
	// a domain or api prefix given as flag or env replaces both jetstream settings of the context
	if opts.Domain == "" && opts.APIPrefix == "" {
		fill(&opts.Domain, ctx.Domain)
		fill(&opts.APIPrefix, ctx.APIPrefix)
	}
	fill(&opts.InboxPrefix, ctx.InboxPrefix)
	opts.TLSFirst = opts.TLSFirst || ctx.TLSFirst

	if opts.URL == "" {
		opts.URL = defaultURL
	}
	return opts, nil
}

func expandHome(p string) string {
	if !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, p[2:])
}

func (o Options) natsOptions(name string) ([]nats.Option, error) {
	nopts := []nats.Option{
		nats.Name(name),
		nats.Timeout(5 * time.Second),
		nats.MaxReconnects(-1),
	}
	if o.Creds != "" {
		nopts = append(nopts, nats.UserCredentials(o.Creds))
	}
	if o.NKey != "" {
		opt, err := nats.NkeyOptionFromSeed(o.NKey)
		if err != nil {
			return nil, fmt.Errorf("loading nkey: %w", err)
		}
		nopts = append(nopts, opt)
	}
	if o.User != "" {
		nopts = append(nopts, nats.UserInfo(o.User, o.Password))
	}
	if o.Token != "" {
		nopts = append(nopts, nats.Token(o.Token))
	}
	if (o.TLSCert == "") != (o.TLSKey == "") {
		return nil, errors.New("tlscert and tlskey must be set together")
	}
	if o.TLSCert != "" {
		nopts = append(nopts, nats.ClientCert(o.TLSCert, o.TLSKey))
	}
	if o.TLSCA != "" {
		nopts = append(nopts, nats.RootCAs(o.TLSCA))
	}
	if o.TLSFirst {
		nopts = append(nopts, nats.TLSHandshakeFirst())
	}
	if o.InboxPrefix != "" {
		nopts = append(nopts, nats.CustomInboxPrefix(o.InboxPrefix))
	}
	return nopts, nil
}

// Connect establishes a NATS connection and returns a JetStream handle for it.
func Connect(opts Options, name string) (*nats.Conn, jetstream.JetStream, Info, error) {
	opts, err := Resolve(opts)
	if err != nil {
		return nil, nil, Info{}, err
	}

	nopts, err := opts.natsOptions(name)
	if err != nil {
		return nil, nil, Info{}, err
	}

	nc, err := nats.Connect(opts.URL, nopts...)
	if err != nil {
		if _, ok := errors.AsType[*url.Error](err); ok {
			err = errors.New("invalid server url") // url.Error quotes the url, password included
		}
		return nil, nil, Info{}, fmt.Errorf("connecting to %s: %w", redactURLs(opts.URL), err)
	}

	var js jetstream.JetStream
	switch {
	case opts.Domain != "" && opts.APIPrefix != "":
		err = errors.New("jetstream domain and api prefix are mutually exclusive")
	case opts.Domain != "":
		js, err = jetstream.NewWithDomain(nc, opts.Domain)
	case opts.APIPrefix != "":
		js, err = jetstream.NewWithAPIPrefix(nc, opts.APIPrefix)
	default:
		js, err = jetstream.New(nc)
	}
	if err != nil {
		nc.Close()
		return nil, nil, Info{}, err
	}

	info := Info{Context: opts.Context, URL: redactURLs(nc.ConnectedUrl())}
	if info.Context == "" {
		info.Context = "-"
	}
	return nc, js, info, nil
}

// redactURLs hides the credentials in a comma separated list of server urls. A user without a
// password is most likely a token, so the whole userinfo is hidden in that case.
func redactURLs(servers string) string {
	parts := strings.Split(servers, ",")
	for i, part := range parts {
		scheme, rest := "", strings.TrimSpace(part)
		if j := strings.Index(rest, "://"); j >= 0 {
			scheme, rest = rest[:j+3], rest[j+3:]
		}
		authority, path := rest, ""
		host := strings.LastIndex(rest, "@") + 1
		if j := strings.Index(rest[host:], "/"); j >= 0 {
			authority, path = rest[:host+j], rest[host+j:]
		}
		at := strings.LastIndex(authority, "@")
		if at < 0 {
			continue
		}
		userinfo := "xxxxx"
		if user, _, ok := strings.Cut(authority[:at], ":"); ok {
			userinfo = user + ":xxxxx"
		}
		parts[i] = scheme + userinfo + authority[at:] + path
	}
	return strings.Join(parts, ",")
}
