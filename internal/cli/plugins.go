package cli

import (
	"fmt"
	"io"

	"github.com/aaronmurniadi/ofxhost/internal/adapter/ofxhost"
)

// runPlugins lists the discovered plugins and exits.
//
// It exists so a user, and a test, can see what the host found without opening a
// window: a plugin that is installed but not listed is a path problem, and this is
// the shortest way to find out.
func runPlugins(args []string, stdout, stderr io.Writer) int {
	_ = args

	host, err := ofxhost.New(ofxhost.Options{})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "plugins: %v\n", err)
		return ExitFailed
	}
	defer func() { _ = host.Close() }()

	if err := host.Discover(nil); err != nil {
		_, _ = fmt.Fprintf(stderr, "plugins: %v\n", err)
		return ExitFailed
	}

	plugins := host.Plugins()
	_, _ = fmt.Fprintf(stdout, "OpenFX %s\n", ofxhost.Version)
	_, _ = fmt.Fprintf(stdout, "searching: %v\n", ofxhost.PluginDirs())
	_, _ = fmt.Fprintf(stdout, "%d plugins\n", len(plugins))
	for _, plugin := range plugins {
		author := plugin.Author
		if author == "" {
			author = "-"
		}
		_, _ = fmt.Fprintf(stdout, "  %-28s %-40s %s\n", plugin.Label, plugin.Identifier, author)
	}
	return ExitOK
}
