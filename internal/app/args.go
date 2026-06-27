package app

import (
	"flag"
	"io"
	"os"
)

// cliOptions is the resolved command line. Flags win; the HTTP settings fall
// back to env vars (NIX_MCP_HTTP / _HTTP_ADDR / _HTTP_PATH, and the
// MCP_NIXOS_* names the Python mcp-nixos used, for drop-in replacement).
type cliOptions struct {
	version  bool
	httpOn   bool
	httpAddr string
	httpPath string
}

// optHTTP is a flag with an optional value: `--http` enables HTTP on the
// default address, `--http=host:port` overrides it. IsBoolFlag lets the bare
// form work without swallowing the next argument.
type optHTTP struct {
	on   bool
	addr string
}

func (o *optHTTP) String() string   { return o.addr }
func (o *optHTTP) IsBoolFlag() bool { return true }
func (o *optHTTP) Set(s string) error {
	o.on = true
	if s != "true" { // the bare flag passes the literal "true"
		o.addr = s
	}
	return nil
}

// parseCLI parses args (os.Args[1:]) and layers env-var defaults underneath.
func parseCLI(args []string) (cliOptions, error) {
	fs := flag.NewFlagSet("nix-mcp", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // we format our own errors via the caller

	var (
		ver      bool
		httpf    optHTTP
		httpPath string
	)
	fs.BoolVar(&ver, "version", false, "print version and exit")
	fs.BoolVar(&ver, "v", false, "print version and exit")
	fs.Var(&httpf, "http", "serve over HTTP; bare to use the default address, or --http=host:port")
	fs.StringVar(&httpPath, "http-path", "", "HTTP endpoint path (default /mcp)")

	if err := fs.Parse(args); err != nil {
		return cliOptions{}, err
	}

	opt := cliOptions{version: ver, httpOn: httpf.on, httpAddr: httpf.addr, httpPath: httpPath}

	// `version` as a bare word (no dash), for parity with the old behavior.
	for _, a := range fs.Args() {
		if a == "version" {
			opt.version = true
		}
	}

	// HTTP can also be switched on by env when no --http flag was given.
	if !opt.httpOn {
		if v := os.Getenv("NIX_MCP_HTTP_ADDR"); v != "" {
			opt.httpOn, opt.httpAddr = true, v
		} else if truthy(os.Getenv("NIX_MCP_HTTP")) {
			opt.httpOn = true
		} else if t := envOr("MCP_NIXOS_TRANSPORT", ""); t == "http" {
			// Drop-in compatibility with the Python mcp-nixos service env.
			opt.httpOn = true
			if host := os.Getenv("MCP_NIXOS_HOST"); host != "" {
				if port := os.Getenv("MCP_NIXOS_PORT"); port != "" {
					opt.httpAddr = host + ":" + port
				}
			}
		}
	}
	if opt.httpOn && opt.httpAddr == "" {
		opt.httpAddr = defaultHTTPAddr
	}
	if opt.httpPath == "" {
		opt.httpPath = envOr("NIX_MCP_HTTP_PATH", envOr("MCP_NIXOS_PATH", defaultHTTPPath))
	}
	opt.httpPath = ensureLeadingSlash(opt.httpPath)

	return opt, nil
}
