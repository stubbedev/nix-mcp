// Package version holds the build version, overridden at release time via
// -ldflags "-X .../version.Version=X.Y.Z".
package version

var Version = "dev"
