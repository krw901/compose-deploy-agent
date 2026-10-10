package setup

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

// ErrInputClosed is returned when input ends before a question is answered.
var ErrInputClosed = errors.New("input closed before the question was answered")

// Prompter asks questions on out and reads one-line answers from in. In
// auto mode (`setup --yes`) every question is answered with its default
// without reading any input.
type Prompter struct {
	in   *bufio.Reader
	out  io.Writer
	auto bool
	text Text
}

// NewPrompter returns a Prompter reading from in and writing to out, in
// the language of text.
func NewPrompter(in io.Reader, out io.Writer, auto bool, text Text) *Prompter {
	return &Prompter{
		in:   bufio.NewReader(in),
		out:  out,
		auto: auto,
		text: text,
	}
}

// Text returns the messages of the prompter's language.
func (p *Prompter) Text() Text {
	return p.text
}

// Printf writes formatted text to the prompter's output.
func (p *Prompter) Printf(format string, args ...any) {
	fmt.Fprintf(p.out, format, args...)
}

// Print writes s to the prompter's output as is.
func (p *Prompter) Print(s string) {
	fmt.Fprint(p.out, s)
}

// Ask prints question and returns the trimmed answer, or def if the answer
// is empty.
func (p *Prompter) Ask(question, def string) (string, error) {
	if def != "" {
		p.Printf("%s [%s]: ", question, def)
	} else {
		p.Printf("%s: ", question)
	}
	if p.auto {
		p.Printf("%s\n", def)
		return def, nil
	}
	line, err := p.readLine()
	if err != nil {
		return "", err
	}
	if line == "" {
		return def, nil
	}
	return line, nil
}

// AskValid is Ask, repeated until validate accepts the answer. In auto
// mode an invalid default is returned as an error instead.
func (p *Prompter) AskValid(question, def string, validate func(string) error) (string, error) {
	for {
		answer, err := p.Ask(question, def)
		if err != nil {
			return "", err
		}
		err = validate(answer)
		if err == nil {
			return answer, nil
		}
		if p.auto {
			return "", err
		}
		p.Printf("  %v\n", err)
	}
}

// Confirm asks a yes/no question, returning def for an empty answer.
func (prompter *Prompter) Confirm(question string, def bool) (bool, error) {
	hint := prompter.text.ConfirmHintNo
	defAnswer := "n"
	if def {
		hint = prompter.text.ConfirmHintYes
		defAnswer = "y"
	}
	for {
		prompter.Printf("%s [%s]: ", question, hint)
		if prompter.auto {
			prompter.Printf("%s\n", defAnswer)
			return def, nil
		}
		line, err := prompter.readLine()
		if err != nil {
			return false, err
		}
		switch strings.ToLower(line) {
		case "":
			return def, nil
		case "y", "yes", "是":
			return true, nil
		case "n", "no", "否":
			return false, nil
		default:
			prompter.Print(prompter.text.ConfirmRetry)
		}
	}
}

func (p *Prompter) readLine() (string, error) {
	line, err := p.in.ReadString('\n')
	if errors.Is(err, io.EOF) {
		if line == "" {
			p.Printf("\n")
			return "", ErrInputClosed
		}
	} else if err != nil {
		return "", fmt.Errorf("read answer: %w", err)
	}
	return strings.TrimSpace(line), nil
}

// ParseSelection parses a multi-selection answer over n numbered items
// (1-based) into sorted, de-duplicated 0-based indexes. It accepts "all",
// "none", comma-separated numbers and ranges such as "1,3-5"; an empty
// answer selects nothing. Errors are worded in the language of text.
func ParseSelection(answer string, n int, text Text) ([]int, error) {
	answer = strings.TrimSpace(answer)
	if strings.EqualFold(answer, "none") {
		return nil, nil
	}
	if strings.EqualFold(answer, "all") {
		out := make([]int, n)
		for i := range n {
			out[i] = i
		}
		return out, nil
	}
	var out []int
	for field := range strings.SplitSeq(answer, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		lo, hi, isRange := strings.Cut(field, "-")
		if !isRange {
			hi = lo
		}
		first, err := parseItemNumber(lo, n, text)
		if err != nil {
			return nil, err
		}
		last, err := parseItemNumber(hi, n, text)
		if err != nil {
			return nil, err
		}
		if first > last {
			return nil, fmt.Errorf(text.ErrBadRange, field)
		}
		for i := first; i <= last; i++ {
			out = append(out, i-1)
		}
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

func parseItemNumber(s string, n int, text Text) (int, error) {
	i, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf(text.ErrNotNumber, s)
	}
	if i < 1 || i > n {
		return 0, fmt.Errorf(text.ErrOutOfRange, i, n)
	}
	return i, nil
}
