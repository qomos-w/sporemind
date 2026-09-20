//go:build !devrelease

package main

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/buildinfo"
)

func TestAppName(t *testing.T) {
	// Save and restore BuildType + releaseBuild
	origType := buildinfo.BuildType
	origRel := releaseBuild
	defer func() {
		buildinfo.BuildType = origType
		releaseBuild = origRel
	}()

	tests := []struct {
		buildType string
		want      string
		release   bool
	}{
		{"release", "sporemind", true},
		{"beta", "sporemind-beta", false},
		{"dev", "sporemind-dev", false},
	}

	for _, tt := range tests {
		buildinfo.BuildType = tt.buildType
		releaseBuild = buildinfo.IsRelease()
		got := appName()
		if got != tt.want {
			t.Errorf("appName() with BuildType=%q = %q, want %q", tt.buildType, got, tt.want)
		}
		if releaseBuild != tt.release {
			t.Errorf("releaseBuild with BuildType=%q = %v, want %v", tt.buildType, releaseBuild, tt.release)
		}
	}
}

func TestSingleInstanceOptions(t *testing.T) {
	origType := buildinfo.BuildType
	origRel := releaseBuild
	defer func() {
		buildinfo.BuildType = origType
		releaseBuild = origRel
	}()

	// Dev: nil
	buildinfo.BuildType = "dev"
	releaseBuild = buildinfo.IsRelease()
	if opts := singleInstanceOptions(nil); opts != nil {
		t.Errorf("singleInstanceOptions(nil) for dev = %v, want nil", opts)
	}

	// Beta: non-nil with beta suffix
	buildinfo.BuildType = "beta"
	releaseBuild = buildinfo.IsRelease()
	opts := singleInstanceOptions(nil)
	if opts == nil {
		t.Fatal("singleInstanceOptions(nil) for beta = nil, want non-nil")
	}
	if opts.UniqueID != "com.qomos-w.sporemind-beta" {
		t.Errorf("UniqueID for beta = %q, want %q", opts.UniqueID, "com.qomos-w.sporemind-beta")
	}
	if opts.ExitCode != 0 {
		t.Errorf("ExitCode for beta = %d, want 0", opts.ExitCode)
	}
	if opts.OnSecondInstanceLaunch == nil {
		t.Error("OnSecondInstanceLaunch for beta = nil, want non-nil")
	}

	// Release: non-nil, no suffix
	buildinfo.BuildType = "release"
	releaseBuild = buildinfo.IsRelease()
	opts = singleInstanceOptions(nil)
	if opts == nil {
		t.Fatal("singleInstanceOptions(nil) for release = nil, want non-nil")
	}
	if opts.UniqueID != "com.qomos-w.sporemind" {
		t.Errorf("UniqueID for release = %q, want %q", opts.UniqueID, "com.qomos-w.sporemind")
	}
	if opts.ExitCode != 0 {
		t.Errorf("ExitCode for release = %d, want 0", opts.ExitCode)
	}
	if opts.OnSecondInstanceLaunch == nil {
		t.Error("OnSecondInstanceLaunch for release = nil, want non-nil")
	}
}