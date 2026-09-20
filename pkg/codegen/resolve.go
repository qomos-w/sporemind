package codegen

// ResolveSDKDir resolves the host SDK directory path. In development mode
// (sporemind repo checkout), it points to the sporemind-plugin-sdk directory
// within the repository. In release mode, it resolves to an extracted copy.
// Returns (sdkPath, freshRelease, error).
func ResolveSDKDir(projectDir string) (string, bool, error) {
	sdkPath, err := EnsureDevSDKWorkspace(projectDir)
	if err != nil {
		return "", false, err
	}
	return sdkPath, false, nil
}
