package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/xypwn/filediver/stingray"
)

// Usage:
//
//	namehash [-raw] <path>...
//
// For each path, prints "0x<hash> <summed-path>". A trailing ".unit" is always
// stripped before hashing (filediver outputs "<name>.unit.fbx").
//
// By default a missing "content/" prefix is prepended, matching the most common
// stingray unit layout (content/...). Pass -raw to hash the path EXACTLY as given
// with no prefix munging -- required for units rooted in other top-level packages
// (e.g. core/stingray_renderer/...), whose true hash is Sum("core/...") and would
// be wrong if "content/" were prepended.
func main() {
	raw := false
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "-raw" {
		raw = true
		args = args[1:]
	}
	for _, a := range args {
		p := strings.TrimSuffix(a, ".unit")
		if !raw && !strings.HasPrefix(p, "content/") {
			p = "content/" + p
		}
		h := stingray.Sum(p)
		fmt.Printf("0x%016x %s\n", h.Value, p)
	}
}
