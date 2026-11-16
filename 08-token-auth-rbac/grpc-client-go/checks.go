package main

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Checks records the demo's narrated assertions. Failures are counted rather than fatal, so the
// whole tour runs. Output goes to stdout (container logs) and is kept for the HTTP response.
// The format matches the Java clients' logs line for line.
type Checks struct {
	client         string
	metrics        *Metrics
	text           strings.Builder
	passed, failed int
}

// Report is the outcome of one demo run.
type Report struct {
	Passed, Failed int
	Text           string
}

func newChecks(client string, metrics *Metrics) *Checks {
	return &Checks{client: client, metrics: metrics}
}

func (c *Checks) section(title string) {
	c.line("\n[%s] ── %s %s", c.client, title, strings.Repeat("─", max(3, 64-utf8.RuneCountInString(title))))
}

// check records one assertion. An optional observed value is printed after the description.
func (c *Checks) check(description string, ok bool, observed ...any) {
	suffix := ""
	if len(observed) > 0 && observed[0] != nil {
		suffix = fmt.Sprintf("   → %v", observed[0])
	}
	mark := "✔"
	if !ok {
		mark = "✘ FAILED:"
	}
	c.line("[%s]   %s %s%s", c.client, mark, description, suffix)
	c.metrics.recordCheck(ok)
	if ok {
		c.passed++
	} else {
		c.failed++
	}
}

func (c *Checks) note(message string) {
	c.line("[%s]     ℹ %s", c.client, message)
}

// abort records an unexpected error that stopped the tour.
func (c *Checks) abort(err error) {
	c.check(fmt.Sprintf("demo aborted by %s", errorCode(err)), false, err)
}

func (c *Checks) finish() Report {
	c.line("\n[%s] ══ demo complete: %d passed, %d failed ══\n", c.client, c.passed, c.failed)
	return Report{Passed: c.passed, Failed: c.failed, Text: c.text.String()}
}

func (c *Checks) line(format string, args ...any) {
	s := fmt.Sprintf(format, args...)
	fmt.Println(s)
	c.text.WriteString(s)
	c.text.WriteByte('\n')
}
