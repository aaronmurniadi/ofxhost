// Package platform holds the operating-system services that are neither a port
// nor a use-case: the directory layout, the embedded resources, and the fatal
// error dialog.
//
// It sits beside the adapter layer rather than inside it because nothing here is
// swappable: a program has one configuration directory convention per operating
// system, and a test that needs a different one sets it through the environment
// rather than through an interface.
//
// Subpackages, added from milestone M2:
//
//	paths/      the per-user config and state directories
//	resource/   the embedded interface font and icons
//	report/     the fatal error dialog and the parent console attach
package platform
