package cmd

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/nats-io/nats.go"

	"github.com/kontrolplane/tsui/pkg/client"
	tui "github.com/kontrolplane/tsui/pkg/tui"
	"github.com/kontrolplane/tsui/pkg/tui/styles"
)

var (
	projectName = "kontrolplane"
	programName = "tsui"
)

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

// shorthands maps a flag to its one letter alias, listed together in the usage.
var shorthands = map[string]string{"server": "s"}

// usage prints the help, with the flags spelled with two dashes as the readme and the nats cli do.
func usage(w io.Writer, fs *flag.FlagSet) {
	_, _ = fmt.Fprintf(w, `%[1]s is a terminal user interface for managing NATS JetStream streams, messages and consumers.

usage: %[1]s [flags]

examples:
  %[1]s
  %[1]s --context production
  %[1]s --server nats://localhost:4222 --creds ~/.nkeys/app.creds

flags:
`, programName)
	aliases := map[string]bool{}
	for _, short := range shorthands {
		aliases[short] = true
	}
	fs.VisitAll(func(f *flag.Flag) {
		if aliases[f.Name] {
			return
		}
		name := "--" + f.Name
		if short, ok := shorthands[f.Name]; ok {
			name = "-" + short + ", " + name
		}
		if kind, _ := flag.UnquoteUsage(f); kind != "" {
			name += " " + kind
		}
		line := "  " + name
		// Other defaults come from the environment, and may be secrets.
		if f.Name == "theme" {
			_, _ = fmt.Fprintf(w, "%-28s %s (default %q)\n", line, f.Usage, f.DefValue)
			return
		}
		_, _ = fmt.Fprintf(w, "%-28s %s\n", line, f.Usage)
	})
}

func Execute(version, commit, date string) {
	opts := client.OptionsFromEnv()

	fs := flag.NewFlagSet(programName, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.URL, "server", opts.URL, "NATS server urls (env NATS_URL)")
	fs.StringVar(&opts.URL, "s", opts.URL, "shorthand for --server")
	fs.StringVar(&opts.Context, "context", opts.Context, "nats cli context to use (env NATS_CONTEXT)")
	fs.StringVar(&opts.Creds, "creds", opts.Creds, "user credentials file (env NATS_CREDS)")
	fs.StringVar(&opts.NKey, "nkey", opts.NKey, "user nkey seed file (env NATS_NKEY)")
	fs.StringVar(&opts.User, "user", opts.User, "username (env NATS_USER)")
	fs.StringVar(&opts.Password, "password", opts.Password, "password (env NATS_PASSWORD)")
	fs.StringVar(&opts.Token, "token", opts.Token, "authentication token (env NATS_TOKEN)")
	fs.StringVar(&opts.TLSCert, "tlscert", opts.TLSCert, "client tls certificate, requires --tlskey (env NATS_CERT)")
	fs.StringVar(&opts.TLSKey, "tlskey", opts.TLSKey, "client tls key, requires --tlscert (env NATS_KEY)")
	fs.StringVar(&opts.TLSCA, "tlsca", opts.TLSCA, "tls certificate authority (env NATS_CA)")
	fs.BoolVar(&opts.TLSFirst, "tlsfirst", opts.TLSFirst, "perform the tls handshake before the nats protocol")
	fs.StringVar(&opts.Domain, "js-domain", opts.Domain, "jetstream domain (env NATS_JS_DOMAIN)")
	theme := fs.String("theme", "auto", "colour theme: auto follows the terminal, dark or light also paint the background")
	debug := fs.Bool("debug", false, "write debug logs to debug.log")
	showVersion := fs.Bool("version", false, "print the version and exit")
	if err := fs.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			usage(os.Stdout, fs)
			return
		}
		fmt.Fprintf(os.Stderr, "%s (see %s --help)\n", styles.Clean(err.Error()), programName)
		os.Exit(2)
	}

	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "unexpected argument %q, %s only takes flags (see %s --help)\n", fs.Arg(0), programName, programName)
		os.Exit(2)
	}

	if *showVersion {
		fmt.Printf("%s %s (commit %s, built %s)\n", programName, version, commit, date)
		return
	}

	fs.Visit(func(f *flag.Flag) {
		if f.Name == "password" {
			fmt.Fprintln(os.Stderr, "warning: a password passed as a flag is visible to other users in the process list, use NATS_PASSWORD or a nats context instead")
		}
	})

	switch *theme {
	case "dark":
		styles.Use(true)
		styles.Paint = true
	case "light":
		styles.Use(false)
		styles.Paint = true
	case "auto":
		styles.Use(lipgloss.HasDarkBackground(os.Stdin, os.Stdout))
	default:
		fmt.Fprintf(os.Stderr, "unknown theme %q, use auto, dark or light\n", styles.Clean(*theme))
		os.Exit(2)
	}

	log.SetOutput(io.Discard)
	if *debug {
		f, err := tea.LogToFile("debug.log", "debug")
		if err != nil {
			fail("could not open debug.log: %s", styles.Clean(err.Error()))
		}
		defer func() { _ = f.Close() }()
	}

	conn, js, info, err := client.Connect(opts, fmt.Sprintf("%s/%s", projectName, programName))
	if err != nil {
		msg := "error connecting to nats: " + styles.Clean(err.Error())
		if errors.Is(err, nats.ErrNoServers) {
			msg += fmt.Sprintf("\nis the server running? pick one with --server, --context or NATS_URL (see %s --help)", programName)
		}
		fail("%s", msg)
	}
	defer conn.Close()

	model := tui.NewModel(projectName, programName, conn, js, info)
	if _, err := tea.NewProgram(model).Run(); err != nil {
		conn.Close()
		fail("error running program: %s", styles.Clean(err.Error()))
	}
}
