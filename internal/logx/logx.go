// Package logx is the only place in agentic-review that writes GitHub Actions
// workflow commands (::warning::, ::debug::, ::error::) and appends to
// $GITHUB_STEP_SUMMARY. All other packages log through it so message escaping
// and the debug-suppression policy stay in one place.
package logx

import (
	"fmt"
	"os"
)

// escape applies GitHub Actions workflow-command escaping to a message body.
// Order matters: '%' is escaped first so the '%' introduced by escaping '\r'
// and '\n' is never itself re-escaped.
func escape(s string) string {
	raw := []byte(s)
	b := make([]byte, 0, len(raw))
	for i := range raw {
		switch raw[i] {
		case '%':
			b = append(b, '%', '2', '5')
		case '\r':
			b = append(b, '%', '0', 'D')
		case '\n':
			b = append(b, '%', '0', 'A')
		default:
			b = append(b, raw[i])
		}
	}
	return string(b)
}

// Warn writes an ::warning:: workflow command to stdout.
func Warn(format string, a ...any) {
	_, _ = fmt.Fprintf(os.Stdout, "::warning::%s\n", escape(fmt.Sprintf(format, a...)))
}

// Error writes an ::error:: workflow command to stdout.
func Error(format string, a ...any) {
	_, _ = fmt.Fprintf(os.Stdout, "::error::%s\n", escape(fmt.Sprintf(format, a...)))
}

// Debug writes an ::debug:: workflow command to stdout. On GitHub Actions
// runners this is unconditional — the runner itself filters visibility on the
// repository/run's ACTIONS_STEP_DEBUG setting. Outside GITHUB_ACTIONS, it is
// suppressed unless AGENTIC_REVIEW_DEBUG=1, so local runs stay quiet by
// default.
func Debug(format string, a ...any) {
	if os.Getenv("GITHUB_ACTIONS") != "true" && os.Getenv("AGENTIC_REVIEW_DEBUG") != "1" {
		return
	}
	_, _ = fmt.Fprintf(os.Stdout, "::debug::%s\n", escape(fmt.Sprintf(format, a...)))
}

// StepSummary appends markdown to $GITHUB_STEP_SUMMARY. It is a no-op when
// that variable is unset (e.g. local runs).
func StepSummary(markdown string) error {
	path := os.Getenv("GITHUB_STEP_SUMMARY")
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("logx: open GITHUB_STEP_SUMMARY: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(markdown); err != nil {
		return fmt.Errorf("logx: write GITHUB_STEP_SUMMARY: %w", err)
	}
	if len(markdown) == 0 || markdown[len(markdown)-1] != '\n' {
		if _, err := f.WriteString("\n"); err != nil {
			return fmt.Errorf("logx: write GITHUB_STEP_SUMMARY: %w", err)
		}
	}
	return nil
}

// Output appends name=value to $GITHUB_OUTPUT (the action.yml output
// surface, spec item 46). A no-op when that variable is unset (e.g.
// local runs). value must not contain a newline — every current caller
// passes an integer or compact (non-indented) JSON, both always safe.
func Output(name, value string) error {
	path := os.Getenv("GITHUB_OUTPUT")
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("logx: open GITHUB_OUTPUT: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := fmt.Fprintf(f, "%s=%s\n", name, value); err != nil {
		return fmt.Errorf("logx: write GITHUB_OUTPUT: %w", err)
	}
	return nil
}
