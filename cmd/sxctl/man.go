package main

import (
	"fmt"
	"io"
	"strings"
)

// man.go writes sxctl(1).
//
// Generated, never typed. The command list is lifted out of the same `usage`
// constant that -h prints and that the dispatcher is written against, so a
// verb cannot exist in the CLI and be missing from the manual: there is one
// list, and both readers get it. A page kept by hand drifts on the first flag
// that changes, and nobody notices until somebody follows it.
//
// docs/man/sxctl.1 is the committed output; man_test.go regenerates and
// refuses a difference, which is the same guard the catalog and the
// stylesheet have.

// manDate is the page's date field. Fixed rather than time.Now(): a manual
// that changes every time it is generated makes the drift guard fail for a
// reason that is not drift.
const manDate = "2026-08-13"

// commandBlock returns the "Resources and verbs" section of usage, without
// its heading, exactly as the CLI prints it.
func commandBlock() string {
	const head = "Resources and verbs:\n"
	i := strings.Index(usage, head)
	if i < 0 {
		return ""
	}
	rest := usage[i+len(head):]
	// The block ends at the first blank line; what follows is prose about
	// SCOPE and the environment, which the page words for itself.
	if j := strings.Index(rest, "\n\n"); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimRight(rest, "\n")
}

// roffEscape makes a line safe inside a .nf block: a leading dot or
// apostrophe would be read as a request, and a bare backslash starts an
// escape. Hyphens become \- so they stay copyable minus signs rather than
// typographic dashes.
func roffEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\e`)
	s = strings.ReplaceAll(s, "-", `\-`)
	if strings.HasPrefix(s, ".") || strings.HasPrefix(s, "'") {
		s = `\&` + s
	}
	return s
}

// writeMan writes the roff source of sxctl(1).
func writeMan(w io.Writer) error {
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }

	p(`.TH SXCTL 1 "%s" "Sextant" "Sextant Manual"`, manDate)
	p(".SH NAME")
	p(`sxctl \- headless client for the Sextant fleet control plane`)

	p(".SH SYNOPSIS")
	p(".B sxctl")
	p(`[\fB\-url\fR \fIURL\fR] [\fB\-json\fR] \fIRESOURCE\fR \fIVERB\fR [\fIARGS\fR...]`)

	p(".SH DESCRIPTION")
	p(`.B sxctl`)
	p(`speaks the same HTTP API as the console (\fI/api/v1\fR) and nothing else.`)
	p(`Everything the console can do, it can do, and it authenticates the same`)
	p(`way \- with a token, never with a session. It holds no state of its own:`)
	p(`the fleet document in git is the state, and every write goes through the`)
	p(`same gate and audit trail as a change made in the browser.`)

	p(".SH COMMANDS")
	p(".nf")
	for _, ln := range strings.Split(commandBlock(), "\n") {
		p("%s", roffEscape(ln))
	}
	p(".fi")

	p(".SH SCOPE")
	p(`A scope is \fBorg\fR, \fBgroup:\fR\fINAME\fR or \fBdevice:\fR\fITAG\fR.`)
	p(`Values parse as JSON where they can (\fBtrue\fR, \fB42\fR, \fB"text"\fR)`)
	p(`and as a plain string otherwise.`)

	p(".SH OPTIONS")
	p(".TP")
	p(`.BR \-url " " \fIURL\fR`)
	p(`Base URL of the console. Defaults to \fBSEXTANT_URL\fR.`)
	p(".TP")
	p(`.B \-json`)
	p(`Emit JSON for list commands instead of the table meant for a terminal.`)

	p(".SH ENVIRONMENT")
	p(".TP")
	p(".B SEXTANT_URL")
	p(`Base URL of the console, when \fB\-url\fR is not given.`)
	p(".TP")
	p(".B SEXTANT_TOKEN")
	p(`API token. Required. Read from the environment on purpose: a secret`)
	p(`passed on the command line is visible in the process table and in shell`)
	p(`history to everybody on the machine.`)

	p(".SH EXIT STATUS")
	p(".TP")
	p(".B 0")
	p("The command succeeded.")
	p(".TP")
	p(".B 1")
	p("The command was understood and the server refused it, or the request failed.")
	p(".TP")
	p(".B 2")
	p(`Usage error: unknown resource or verb, missing argument, or missing`)
	p(`\fBSEXTANT_URL\fR / \fBSEXTANT_TOKEN\fR.`)

	p(".SH EXAMPLES")
	p(".nf")
	p(`export SEXTANT_URL=https://console.example.org SEXTANT_TOKEN=...`)
	p(`sxctl devices list`)
	p(`sxctl settings set group:pilot desktop \(dqgnome\(dq`)
	p(`sxctl changes open raise\-poll \(dqraise the poll interval\(dq`)
	p(`sxctl changes submit raise\-poll`)
	p(".fi")

	p(".SH SEE ALSO")
	p(`The API contract this speaks is published at \fI/api/v1/openapi.json\fR`)
	p(`on any console, and the handbook at \fIhttps://docs.sextantfleet.com\fR.`)

	_, err := io.WriteString(w, b.String())
	return err
}
