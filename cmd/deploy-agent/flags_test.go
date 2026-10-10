package main

import (
	"flag"
	"testing"
)

func Test_stringFlag(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "long form",
			args: []string{"--service", "lss-web"},
			want: "lss-web",
		},
		{
			name: "short form",
			args: []string{"-s", "lss-web"},
			want: "lss-web",
		},
		{
			name: "default",
			args: nil,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			service := stringFlag(fs, "service", "s", "", "logical service name")
			if err := fs.Parse(tt.args); err != nil {
				t.Fatalf("parse: %v", err)
			}
			if *service != tt.want {
				t.Fatalf("service = %q, want %q", *service, tt.want)
			}
		})
	}
}

func Test_requireOneRef(t *testing.T) {
	cases := []struct {
		tag, digest string
		ok          bool
	}{
		{"abc1234", "", true},
		{"", "sha256:abc", true},
		{"", "", false},
		{"abc1234", "sha256:abc", false},
	}
	for _, c := range cases {
		err := requireOneRef(c.tag, c.digest)
		if (err == nil) != c.ok {
			t.Errorf("requireOneRef(%q, %q) error=%v, want ok=%v", c.tag, c.digest, err, c.ok)
		}
	}
}
