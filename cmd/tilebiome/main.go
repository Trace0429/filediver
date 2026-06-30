// tilebiome: for a terrain tile unit, find which archive(s) it lives in, then list which
// terrain biome textures (by known hash->name map) co-occur in those same archives.
// Goal: infer per-tile biome by archive co-location.
// 用法: tilebiome <gamedir/data> <biome_tex_hashnames.txt> <0xtilehash> ...
//   biome_tex_hashnames.txt lines: "<16hex> <name>"
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/xypwn/filediver/stingray"
)

func parseHash(s string) stingray.Hash {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "0x") {
		s = s[2:]
	}
	v, _ := strconv.ParseUint(s, 16, 64)
	return stingray.Hash{Value: v}
}

func main() {
	if len(os.Args) < 4 {
		fmt.Println("usage: tilebiome <gamedir/data> <biome_tex_hashnames.txt> <0xtilehash> ...")
		os.Exit(1)
	}
	dd, err := stingray.OpenDataDir(context.Background(), os.Args[1], func(c, t int) {})
	if err != nil {
		panic(err)
	}

	// load biome texture hash -> name
	biome := map[uint64]string{}
	f, err := os.Open(os.Args[2])
	if err != nil {
		panic(err)
	}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		parts := strings.Fields(sc.Text())
		if len(parts) >= 2 {
			biome[parseHash(parts[0]).Value] = parts[1]
		}
	}
	f.Close()
	fmt.Printf("loaded %d biome texture names\n", len(biome))

	// build FileID.Name(value) -> set of archives it appears in
	// dd.Archives: archiveHash -> []FileID
	nameToArchives := map[uint64][]stingray.Hash{}
	archiveFiles := dd.Archives
	for arc, fids := range archiveFiles {
		for _, fid := range fids {
			nameToArchives[fid.Name.Value] = append(nameToArchives[fid.Name.Value], arc)
		}
	}

	textureType := stingray.Sum("texture")
	_ = textureType

	for _, arg := range os.Args[3:] {
		tileName := parseHash(arg).Value
		arcs := nameToArchives[tileName]
		fmt.Printf("\n==== tile %s : in %d archive(s) ====\n", arg, len(arcs))
		// collect set of archive hashes the tile is in
		arcSet := map[stingray.Hash]bool{}
		for _, a := range arcs {
			arcSet[a] = true
		}
		// for each such archive, scan its files for biome textures
		biomeHits := map[string]int{}
		totalFilesInArcs := 0
		for a := range arcSet {
			fids := archiveFiles[a]
			totalFilesInArcs += len(fids)
			for _, fid := range fids {
				if name, ok := biome[fid.Name.Value]; ok {
					biomeHits[name]++
				}
			}
		}
		fmt.Printf("  archives hold %d files total; biome-texture hits: %d distinct\n", totalFilesInArcs, len(biomeHits))
		// print biome hits grouped by prefix (arctic_/forest_/neutral_...)
		prefixCount := map[string]int{}
		for name := range biomeHits {
			pfx := name
			if i := strings.Index(name, "_"); i > 0 {
				// take up to second underscore for biome grouping
				rest := name[i+1:]
				if j := strings.Index(rest, "_"); j > 0 {
					pfx = name[:i+1+j]
				} else {
					pfx = name[:i]
				}
			}
			prefixCount[pfx]++
		}
		for pfx, n := range prefixCount {
			fmt.Printf("    biome-group %-28s : %d textures\n", pfx, n)
		}
		// list a few specific names
		shown := 0
		for name := range biomeHits {
			fmt.Printf("      - %s\n", name)
			shown++
			if shown >= 20 {
				fmt.Printf("      ... (%d more)\n", len(biomeHits)-20)
				break
			}
		}
	}
}
