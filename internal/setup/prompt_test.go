package setup

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestPrompter_Ask(t *testing.T) {
	var out strings.Builder
	p := NewPrompter(strings.NewReader("\n  custom \n"), &out, false, english)

	got, err := p.Ask("port", "8866")
	if err != nil {
		t.Fatalf("Ask with empty answer = %q, %v; want default", got, err)
	}
	if got != "8866" {
		t.Fatalf("Ask with empty answer = %q, %v; want default", got, err)
	}
	got, err = p.Ask("port", "8866")
	if err != nil {
		t.Fatalf("Ask = %q, %v; want %q", got, err, "custom")
	}
	if got != "custom" {
		t.Fatalf("Ask = %q, %v; want %q", got, err, "custom")
	}
	if _, err := p.Ask("port", "8866"); !errors.Is(err, ErrInputClosed) {
		t.Fatalf("Ask at EOF: err = %v, want ErrInputClosed", err)
	}
}

func TestPrompter_AskValidRetries(t *testing.T) {
	var out strings.Builder
	p := NewPrompter(strings.NewReader("bad\ngood\n"), &out, false, english)
	got, err := p.AskValid("value", "", func(s string) error {
		if s != "good" {
			return errors.New("must be good")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("AskValid = %q, %v", got, err)
	}
	if got != "good" {
		t.Fatalf("AskValid = %q, %v", got, err)
	}
	if !strings.Contains(out.String(), "must be good") {
		t.Fatalf("expected validation error in output, got %q", out.String())
	}
}

func TestPrompter_Confirm(t *testing.T) {
	var out strings.Builder
	p := NewPrompter(strings.NewReader("\nmaybe\nY\nno\n"), &out, false, english)
	for i, want := range []bool{true, true, false} {
		got, err := p.Confirm("continue?", true)
		if err != nil {
			t.Fatalf("Confirm #%d = %v, %v; want %v", i, got, err, want)
		}
		if got != want {
			t.Fatalf("Confirm #%d = %v, %v; want %v", i, got, err, want)
		}
	}
}

func TestPrompter_ConfirmChinese(t *testing.T) {
	var out strings.Builder
	p := NewPrompter(strings.NewReader("是\n否\n好\ny\n"), &out, false, chinese)
	for i, want := range []bool{true, false, true} {
		got, err := p.Confirm("继续？", false)
		if err != nil || got != want {
			t.Fatalf("Confirm #%d = %v, %v; want %v", i, got, err, want)
		}
	}
	if !strings.Contains(out.String(), "[y/N, 默认 n]") || !strings.Contains(out.String(), "请回答") {
		t.Fatalf("expected Chinese hint and retry message, got %q", out.String())
	}
}

func TestPrompter_Auto(t *testing.T) {
	var out strings.Builder
	p := NewPrompter(strings.NewReader(""), &out, true, english)
	got, err := p.Ask("port", "8866")
	if err != nil {
		t.Fatalf("auto Ask = %q, %v", got, err)
	}
	if got != "8866" {
		t.Fatalf("auto Ask = %q, %v", got, err)
	}
	confirmed, err := p.Confirm("overwrite?", false)
	if err != nil {
		t.Fatalf("auto Confirm = %v, %v; want false", confirmed, err)
	}
	if confirmed {
		t.Fatalf("auto Confirm = %v, %v; want false", confirmed, err)
	}
	if _, err := p.AskValid("ip", "", func(string) error { return errors.New("required") }); err == nil {
		t.Fatalf("auto AskValid with invalid default: expected error")
	}
}

func TestParseSelection(t *testing.T) {
	tests := []struct {
		in   string
		want []int
	}{
		{"", nil},
		{"none", nil},
		{"all", []int{0, 1, 2, 3}},
		{"2", []int{1}},
		{"3, 1,3", []int{0, 2}},
		{"2-4", []int{1, 2, 3}},
	}
	for _, tt := range tests {
		got, err := ParseSelection(tt.in, 4, english)
		if err != nil {
			t.Errorf("ParseSelection(%q): %v", tt.in, err)
			continue
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("ParseSelection(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}

	for _, bad := range []string{"0", "5", "x", "3-2"} {
		if _, err := ParseSelection(bad, 4, english); err == nil {
			t.Errorf("ParseSelection(%q): expected error", bad)
		}
	}
}
