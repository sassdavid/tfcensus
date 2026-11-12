package prompt

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ErrNoInput is returned when a change needs confirmation and prompting isn't allowed.
var ErrNoInput = errors.New("refusing to modify state without --yes")

// ErrDeclined is returned when the typed answer doesn't match.
var ErrDeclined = errors.New("confirmation did not match")

// Prompter asks for confirmations.
type Prompter interface {
	// ConfirmTyped shows message and returns nil only when the answer equals want.
	ConfirmTyped(ctx context.Context, message, want string) error
}

// Line is a [Prompter] that writes to Out and reads one line from In per question.
type Line struct {
	in  *bufio.Reader
	out io.Writer
}

// NewLine returns a Line prompter.
func NewLine(in io.Reader, out io.Writer) *Line {
	return &Line{in: bufio.NewReader(in), out: out}
}

// ConfirmTyped implements [Prompter].
func (l *Line) ConfirmTyped(ctx context.Context, message, want string) error {
	if _, err := fmt.Fprintf(l.out, "%s\nType %q to confirm: ", message, want); err != nil {
		return fmt.Errorf("write prompt: %w", err)
	}
	type answer struct {
		line string
		err  error
	}
	ch := make(chan answer, 1)
	go func() {
		s, err := l.in.ReadString('\n')
		ch <- answer{s, err}
	}()
	select {
	case <-ctx.Done():
		return fmt.Errorf("read confirmation: %w", ctx.Err())
	case a := <-ch:
		if a.err != nil && !errors.Is(a.err, io.EOF) {
			return fmt.Errorf("read confirmation: %w", a.err)
		}
		if strings.TrimSpace(a.line) != want {
			return ErrDeclined
		}
		return nil
	}
}

// Allowed reports whether a command may prompt: stdin and stdout are terminals, CI is unset and
// --no-input wasn't given.
func Allowed(stdinTTY, stdoutTTY bool, lookup func(string) (string, bool), noInput bool) bool {
	if noInput || !stdinTTY || !stdoutTTY {
		return false
	}
	_, ci := lookup("CI")
	return !ci
}

// Gate confirms one change: yes skips the question, a missing prompter means [ErrNoInput].
type Gate struct {
	Yes      bool
	Prompter Prompter // nil when prompting isn't allowed
}

// Confirm asks for want unless the gate was opened with --yes.
func (g Gate) Confirm(ctx context.Context, message, want string) error {
	if g.Yes {
		return nil
	}
	if g.Prompter == nil {
		return ErrNoInput
	}
	return g.Prompter.ConfirmTyped(ctx, message, want)
}
