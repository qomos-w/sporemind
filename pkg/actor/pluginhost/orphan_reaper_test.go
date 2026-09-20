package pluginhost

import "testing"

func TestOrphanPluginDecision(t *testing.T) {
	alive := map[uint32]bool{10: true, 20: true}
	const self = uint32(20)
	cases := []struct {
		name   string
		exe    string
		parent uint32
		want   bool
	}{
		{"orphan plugin exe", "plugin-01a07badbb2c00000000000000000053-80a03df2f27d1ff0.exe", 99, true},
		{"parent alive keeps", "plugin-x-abcdef1234567890.exe", 10, false},
		{"own child keeps", "plugin-x-abcdef1234567890.exe", 20, false},
		{"zero parent unknown keeps", "plugin-x-abcdef1234567890.exe", 0, false},
		{"non-plugin name keeps", "cmd.exe", 99, false},
		{"plugin without hash keeps", "plugin-x.exe", 99, false},
		{"plugin dir name keeps", "plugin-src.exe", 99, false},
		{"case-insensitive exe", "PLUGIN-X-abcdef1234567890.EXE", 99, true},
	}
	for _, tc := range cases {
		if got := orphanPluginDecision(tc.exe, tc.parent, alive, self); got != tc.want {
			t.Errorf("%s: orphanPluginDecision(%q, %d) = %v, want %v", tc.name, tc.exe, tc.parent, got, tc.want)
		}
	}
}
