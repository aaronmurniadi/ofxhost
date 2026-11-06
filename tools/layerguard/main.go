// Command layerguard enforces the dependency rule of this repository.
//
// Three rules are checked:
//
//  1. Internal layers may only import the layers named in layers.go, plus the
//     standard library. A package in internal/domain may not import
//     internal/port, a package in internal/port may not import internal/service,
//     and so on.
//  2. The blank import "C" is legal only under internal/adapter/.
//  3. The Wails framework is legal only under internal/transport/ and cmd/.
//
// Run it from the repository root:
//
//	go run ./tools/layerguard
//
// It prints every violation and exits 1 when it found one. The test in
// tools/layerguard/layerguard_test.go runs it as part of go test ./..., so a
// violation fails the normal build too.
package main

import (
	"fmt"
	"os"
)

func main() {
	violations, err := Check(".")
	if err != nil {
		fmt.Fprintf(os.Stderr, "layerguard: %v\n", err)
		os.Exit(2)
	}
	if len(violations) == 0 {
		fmt.Println("layerguard: no violations")
		return
	}
	for _, v := range violations {
		fmt.Fprintln(os.Stderr, v.String())
	}
	fmt.Fprintf(os.Stderr, "layerguard: %d violation(s)\n", len(violations))
	os.Exit(1)
}
