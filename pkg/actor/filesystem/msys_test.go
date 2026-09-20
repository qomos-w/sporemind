package filesystem

import "testing"

func TestFromMSYSPath(t *testing.T) {
	tests := []struct{ in, want string }{
		{"/d/dev/project", "d:/dev/project"},
		{"/D/dev/project", "D:/dev/project"},
		{"/c/Users/foo", "c:/Users/foo"},
		{"/d/dev/project/prompts/architect-system.md", "d:/dev/project/prompts/architect-system.md"},
		{"D:/dev/project", "D:/dev/project"},
		{"relative/path", "relative/path"},
		{"/notdrive/foo", "/notdrive/foo"},
		{"/a", "/a"},
		{"", ""},
	}
	for _, tt := range tests {
		got := fromMSYSPath(tt.in)
		if got != tt.want {
			t.Errorf("fromMSYSPath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
