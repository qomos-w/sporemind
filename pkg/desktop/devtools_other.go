//go:build !windows

package desktop

func init() {
	openDevToolsImpl = func() {}
}
