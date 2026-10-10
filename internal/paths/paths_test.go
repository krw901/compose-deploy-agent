package paths

import "testing"

func Test_SocketPath(t *testing.T) {
	cases := map[string]string{
		DefaultDataDir:         DefaultSocketPath,
		DefaultDataDir + "/":   DefaultSocketPath,
		"/custom/data-dir":     "/custom/data-dir/agent.sock",
		"/custom/data-dir/sub": "/custom/data-dir/sub/agent.sock",
	}
	for dataDir, want := range cases {
		got := SocketPath(dataDir)
		if got != want {
			t.Errorf("SocketPath(%q) = %q, want %q", dataDir, got, want)
		}
	}
}
