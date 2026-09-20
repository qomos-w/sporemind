package main

import (
	"github.com/qomos-w/sporemind/pkg/buildinfo"
	"github.com/qomos-w/sporemind/pkg/desktop"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// releaseBuild is true for release builds. It is kept as a package-level var
// (previously a const split across build tags) so main.go can keep its
// existing branch on whether to use ClosePreviousInstance for dev hot-reload.
var releaseBuild = buildinfo.IsRelease()

// appName returns the display name used for the Wails application and window
// title. The value is driven by buildinfo.BuildType instead of the Go build tag.
func appName() string {
	if devReleaseBuild {
		return "sporemind-devrelease"
	}
	switch buildinfo.BuildType {
	case "release":
		return "sporemind"
	case "beta":
		return "sporemind-beta"
	default:
		return "sporemind-dev"
	}
}

// singleInstanceOptions returns Wails SingleInstance options for beta/release
// builds. Dev builds return nil so ClosePreviousInstance can be used for
// hot-reload instead of a named mutex. A second launch notifies this instance
// (OnSecondInstanceLaunch) and exits with code 0; the callback re-activates
// the running main window.
func singleInstanceOptions(app *desktop.App) *application.SingleInstanceOptions {
	if buildinfo.IsDev() {
		return nil
	}
	return &application.SingleInstanceOptions{
		UniqueID:               "com.qomos-w." + appName(),
		ExitCode:               0,
		OnSecondInstanceLaunch: func(application.SecondInstanceData) { app.FocusMainWindow() },
	}
}
