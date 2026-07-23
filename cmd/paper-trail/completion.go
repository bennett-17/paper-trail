package main

import (
	"fmt"
	"os"
	"strings"
)

// completionCommands and completionFlags are the single source of truth for
// what the completion scripts below offer -- keep both in sync with the
// subcommands registered in main() and their flag sets in printUsage().
const completionCommands = "lookup filings graph fulltext nonprofit aucharity ukcharity sanctions uksanctions companieshouse person risk completion version help"

var completionFlags = map[string]string{
	"lookup":         "--cik --json",
	"filings":        "--cik --form --limit --json",
	"graph":          "--output --include-insiders --include-beneficial-owners --cik",
	"fulltext":       "--forms --ciks --start --end --offset --limit --json",
	"nonprofit":      "--ein --page --json",
	"aucharity":      "--abn --offset --limit --json",
	"ukcharity":      "--regno --suffix --json",
	"sanctions":      "--fuzzy --offset --limit --json",
	"uksanctions":    "--limit --json",
	"companieshouse": "--number --officer --limit --json",
	"person":         "--limit --json",
	"risk":           "--input-file --batch --serve --limit --output --graph --html --report-html --graph-csv --entities-csv --graph-graphml --cache-ttl --diff --watch --top --min-weight --indicator --min-corroboration --exclude --exclude-file --fail-on --webhook --summary --no-color --quiet --json",
}

const bashCompletionScript = `# paper-trail bash completion
# Install: source this file, or copy it to a directory your bash-completion
# setup sources (e.g. /etc/bash_completion.d/ or /usr/local/etc/bash_completion.d/).
#   source <(paper-trail completion bash)
_paper_trail() {
    local cur cmd
    COMPREPLY=()
    cur="${COMP_WORDS[COMP_CWORD]}"
    cmd="${COMP_WORDS[1]}"

    if [[ $COMP_CWORD -eq 1 ]]; then
        COMPREPLY=($(compgen -W "%[1]s" -- "$cur"))
        return 0
    fi

    if [[ "$cmd" == "completion" && $COMP_CWORD -eq 2 ]]; then
        COMPREPLY=($(compgen -W "bash zsh" -- "$cur"))
        return 0
    fi

    if [[ "$cur" == -* ]]; then
        local flags
        case "$cmd" in
%[2]s
        esac
        COMPREPLY=($(compgen -W "$flags" -- "$cur"))
    fi
}
complete -F _paper_trail paper-trail
`

const zshCompletionScript = `#compdef paper-trail
# paper-trail zsh completion
# Install: place on your $fpath as _paper-trail (autoload -U compinit; compinit
# will pick it up), or:
#   source <(paper-trail completion zsh)
_paper_trail() {
    local -a commands
    commands=(%[1]s)

    if (( CURRENT == 2 )); then
        compadd -- "${commands[@]}"
        return
    fi

    if [[ "${words[2]}" == "completion" && CURRENT -eq 3 ]]; then
        compadd -- bash zsh
        return
    fi

    local -a flags
    case "${words[2]}" in
%[2]s
    esac
    compadd -- "${flags[@]}"
}
_paper_trail
`

func runCompletion(args []string) {
	const usage = "usage: paper-trail completion bash|zsh"
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}

	switch args[0] {
	case "bash":
		fmt.Printf(bashCompletionScript, completionCommands, bashFlagCases())
	case "zsh":
		fmt.Printf(zshCompletionScript, completionCommands, zshFlagCases())
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}
}

func bashFlagCases() string {
	var out string
	for _, cmd := range orderedCompletionCommands() {
		out += fmt.Sprintf("        %s) flags=\"%s\" ;;\n", cmd, completionFlags[cmd])
	}
	return out
}

func zshFlagCases() string {
	var out string
	for _, cmd := range orderedCompletionCommands() {
		out += fmt.Sprintf("        %s) flags=(%s) ;;\n", cmd, completionFlags[cmd])
	}
	return out
}

// orderedCompletionCommands returns the subcommands that take flags, in the
// same order they appear in completionCommands, so the generated case
// statements are deterministic between runs.
func orderedCompletionCommands() []string {
	var out []string
	for _, cmd := range strings.Fields(completionCommands) {
		if _, ok := completionFlags[cmd]; ok {
			out = append(out, cmd)
		}
	}
	return out
}
