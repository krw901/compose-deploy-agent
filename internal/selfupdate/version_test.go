package selfupdate

import "testing"

func TestParseVersion(t *testing.T) {
	tests := []struct {
		in      string
		want    Version
		wantErr bool
	}{
		{
			in: "1.2.3",
			want: Version{
				Major: 1,
				Minor: 2,
				Patch: 3,
			},
		},
		{
			in:   "0.0.0",
			want: Version{},
		},
		{
			in: "10.20.30",
			want: Version{
				Major: 10,
				Minor: 20,
				Patch: 30,
			},
		},
		{
			in:      "v1.2.3",
			wantErr: true,
		},
		{
			in:      "1.2",
			wantErr: true,
		},
		{
			in:      "1.2.3.4",
			wantErr: true,
		},
		{
			in:      "1.2.3-rc1",
			wantErr: true,
		},
		{
			in:      "01.2.3",
			wantErr: true,
		},
		{
			in:      "1.-2.3",
			wantErr: true,
		},
		{
			in:      "1.+2.3",
			wantErr: true,
		},
		{
			in:      "dev",
			wantErr: true,
		},
		{
			in:      "",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseVersion(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseVersion(%q) = %v, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseVersion(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("ParseVersion(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestVersion_Compare(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{
			a:    "1.0.0",
			b:    "1.0.0",
			want: 0,
		},
		{
			a:    "1.0.0",
			b:    "1.0.1",
			want: -1,
		},
		{
			a:    "1.9.0",
			b:    "1.10.0",
			want: -1,
		},
		{
			a:    "2.0.0",
			b:    "1.99.99",
			want: 1,
		},
		{
			a:    "1.2.10",
			b:    "1.2.9",
			want: 1,
		},
	}
	for _, tt := range tests {
		a, err := ParseVersion(tt.a)
		if err != nil {
			t.Fatal(err)
		}
		b, err := ParseVersion(tt.b)
		if err != nil {
			t.Fatal(err)
		}
		if got := a.Compare(b); got != tt.want {
			t.Errorf("%s.Compare(%s) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
