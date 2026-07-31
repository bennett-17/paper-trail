package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/bennett-17/paper-trail/internal/crtsh"
)

func runCrtsh(args []string) {
	fs := flag.NewFlagSet("crtsh", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "print raw JSON")
	flagArgs, positional := splitPositional(fs, args)
	fs.Parse(flagArgs)

	const usage = "usage: paper-trail crtsh <domain> [--json]"
	if len(positional) != 1 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}
	domain := positional[0]

	client := crtsh.NewClient()
	certs, err := client.Certificates(domain)
	exitOnErr(err)

	if *asJSON {
		printJSON(certs)
		return
	}

	fmt.Printf("%d distinct certificate(s) found for %s:\n\n", len(certs), domain)
	for _, c := range certs {
		fmt.Printf("%s -- %s to %s\n", orDash(c.IssuerName), c.NotBefore.Format("2006-01-02"), c.NotAfter.Format("2006-01-02"))
		fmt.Printf("  names: %s\n", orDash(strings.Join(c.SANs, ", ")))
	}
	if len(certs) == 0 {
		fmt.Println("No certificates found. Note: this searches public Certificate Transparency logs (crt.sh) only -- a domain that has never used a publicly-trusted TLS certificate (e.g. HTTP-only, or a purely internal name) won't appear here regardless of whether it's real.")
	}
}
