//go:build !darwin && !windows && !linux

package paths

// appDirName is the directory name used under each base directory on a platform
// with no convention of its own.
const appDirName = "ofxhost"
