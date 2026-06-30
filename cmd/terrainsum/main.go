// terrainsum: process a list of terrain unit hashes, summarize per-tile texture layers,
// and tally distinct layer-Path sets across all tiles. Also flags any non-zero UnkData.
// 用法: terrainsum <gamedir/data> <hashlist.txt>   (hashlist: one bare-or-0x hash per line)
package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/xypwn/filediver/stingray"
	"github.com/xypwn/filediver/stingray/unit"
)

func parseName(s string) stingray.Hash {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "0x") {
		s = s[2:]
	}
	if v, err := strconv.ParseUint(s, 16, 64); err == nil && len(s) == 16 {
		return stingray.Hash{Value: v}
	}
	return stingray.Sum(s)
}

func main() {
	if len(os.Args) < 3 {
		fmt.Println("usage: terrainsum <gamedir/data> <hashlist.txt>")
		os.Exit(1)
	}
	dd, err := stingray.OpenDataDir(context.Background(), os.Args[1], func(c, t int) {})
	if err != nil {
		panic(err)
	}
	f, err := os.Open(os.Args[2])
	if err != nil {
		panic(err)
	}
	defer f.Close()

	type pairKey string
	pairCount := map[pairKey]int{}
	pairExample := map[pairKey]string{}
	layerCount := map[uint64]int{} // individual Path -> #tiles using it
	unkIntPattern := map[string]int{}
	nonZeroUnk := 0
	layerHistogram := map[int]int{} // #layers -> #tiles
	processed := 0
	failed := 0

	// CSV: hash,nlayers,L0path,L0res,L0ukint,L1path,L1res,L1ukint,unkNonZero,sizeX,heightSide,quad
	csv, _ := os.Create("H:/gameres/HD2_extract/_terrain_layers.csv")
	defer csv.Close()
	fmt.Fprintln(csv, "hash,nlayers,L0path,L0res,L0uk,L1path,L1res,L1uk,unkNonZero,sizeX,heightSide,quad")

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		h := parseName(line)
		fid := stingray.FileID{Name: h, Type: stingray.Sum("unit")}
		mainBytes, err := dd.Read(fid, stingray.DataMain)
		if err != nil {
			failed++
			continue
		}
		info, err := unit.LoadInfo(bytes.NewReader(mainBytes))
		if err != nil || len(info.TerrainInfos) == 0 {
			failed++
			continue
		}
		ti := info.TerrainInfos[0]
		processed++
		layerHistogram[len(ti.Textures)]++

		// build sorted pair key from layer paths
		paths := make([]uint64, 0, len(ti.Textures))
		uks := make([]uint32, 0, len(ti.Textures))
		anyNonZero := false
		for _, tx := range ti.Textures {
			paths = append(paths, tx.Path.Value)
			uks = append(uks, tx.UnkInt)
			layerCount[tx.Path.Value]++
			for _, by := range tx.UnkData[:] {
				if by != 0 {
					anyNonZero = true
					break
				}
			}
		}
		if anyNonZero {
			nonZeroUnk++
		}
		sortedPaths := append([]uint64(nil), paths...)
		sort.Slice(sortedPaths, func(i, j int) bool { return sortedPaths[i] < sortedPaths[j] })
		var kb strings.Builder
		for _, p := range sortedPaths {
			fmt.Fprintf(&kb, "%016x|", p)
		}
		key := pairKey(kb.String())
		pairCount[key]++
		if pairExample[key] == "" {
			pairExample[key] = line
		}

		var uks2 strings.Builder
		for _, u := range uks {
			fmt.Fprintf(&uks2, "%d,", u)
		}
		unkIntPattern[uks2.String()]++

		dx := ti.Max[0] - ti.Min[0]
		hmSide := 0
		for s := 16; s <= 4096; s++ {
			if s*s*2 == len(ti.HeightmapData) {
				hmSide = s
				break
			}
		}
		l0p, l0r, l0u := uint64(0), uint32(0), uint32(0)
		l1p, l1r, l1u := uint64(0), uint32(0), uint32(0)
		if len(ti.Textures) > 0 {
			l0p, l0r, l0u = ti.Textures[0].Path.Value, ti.Textures[0].Resolution, ti.Textures[0].UnkInt
		}
		if len(ti.Textures) > 1 {
			l1p, l1r, l1u = ti.Textures[1].Path.Value, ti.Textures[1].Resolution, ti.Textures[1].UnkInt
		}
		fmt.Fprintf(csv, "%s,%d,%016x,%d,%d,%016x,%d,%d,%v,%.1f,%d,%d\n",
			line, len(ti.Textures), l0p, l0r, l0u, l1p, l1r, l1u, anyNonZero, dx, hmSide, len(ti.QuadtreeNodes))
	}

	fmt.Printf("processed=%d failed=%d\n", processed, failed)
	fmt.Printf("tiles with non-zero UnkData: %d\n", nonZeroUnk)
	fmt.Printf("\nlayer-count histogram (#layers -> #tiles):\n")
	var lcs []int
	for k := range layerHistogram {
		lcs = append(lcs, k)
	}
	sort.Ints(lcs)
	for _, k := range lcs {
		fmt.Printf("  %d layers: %d tiles\n", k, layerHistogram[k])
	}

	fmt.Printf("\nUnkInt patterns (per-layer UnkInt seq -> #tiles):\n")
	for k, v := range unkIntPattern {
		fmt.Printf("  [%s] -> %d\n", k, v)
	}

	fmt.Printf("\ndistinct layer-Path SETS: %d\n", len(pairCount))
	type pc struct {
		k pairKey
		n int
	}
	var pcs []pc
	for k, n := range pairCount {
		pcs = append(pcs, pc{k, n})
	}
	sort.Slice(pcs, func(i, j int) bool { return pcs[i].n > pcs[j].n })
	fmt.Printf("top sets (set -> #tiles, example hash):\n")
	for i, p := range pcs {
		if i >= 40 {
			fmt.Printf("  ... (%d more)\n", len(pcs)-40)
			break
		}
		fmt.Printf("  %3d x  %s   eg %s\n", p.n, string(p.k), pairExample[p.k])
	}

	fmt.Printf("\ndistinct individual layer Paths: %d\n", len(layerCount))
	type lc struct {
		p uint64
		n int
	}
	var lcl []lc
	for p, n := range layerCount {
		lcl = append(lcl, lc{p, n})
	}
	sort.Slice(lcl, func(i, j int) bool { return lcl[i].n > lcl[j].n })
	for i, l := range lcl {
		if i >= 60 {
			fmt.Printf("  ... (%d more)\n", len(lcl)-60)
			break
		}
		fmt.Printf("  %3d x  0x%016x\n", l.n, l.p)
	}
}
