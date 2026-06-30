// speedtreescan — READ-ONLY probe: scan every level for Speedtrees[] placement.
// Does NOT modify any filediver logic; standalone command mirroring levelscan's
// level-enumeration skeleton. Answers "how many trees are placed per cell, and
// which .speedtree assets / how many instances".
//
// usage: speedtreescan <gamedir/data> [summary | dump <out.json>]
//   summary           print per-level speedtree counts + totals to stdout
//   dump <out.json>   write {levelHash: [{path, instances, positions...}]} JSON
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/xypwn/filediver/stingray"
	"github.com/xypwn/filediver/stingray/level"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: speedtreescan <gamedir/data> [summary | dump <out.json>]")
		os.Exit(1)
	}
	mode := "summary"
	if len(os.Args) >= 3 {
		mode = os.Args[2]
	}

	dd, err := stingray.OpenDataDir(context.Background(), os.Args[1], func(c, t int) {})
	if err != nil {
		panic(err)
	}
	levelType := stingray.Sum("level")

	type TreeRef struct {
		Path      string       `json:"path"`       // 0x<hash> of the .speedtree
		Instances int          `json:"instances"`  // len(Transforms)
		Layers    int          `json:"layers"`
		Positions [][4]float32 `json:"positions,omitempty"`
	}

	result := map[string][]TreeRef{}
	var (
		nLevels         = 0
		nLevelsWithTree = 0
		nTreeRefs       = 0 // distinct speedtree entries (path occurrences)
		nInstances      = 0 // total tree instances (sum of all Transforms)
	)
	distinctTrees := map[uint64]int{} // tree path -> instance count

	for fid := range dd.Files {
		if fid.Type != levelType {
			continue
		}
		mb, err := dd.Read(fid, stingray.DataMain)
		if err != nil {
			continue
		}
		lv, err := level.LoadLevel(bytes.NewReader(mb))
		if err != nil {
			continue
		}
		nLevels++
		if len(lv.Speedtrees) == 0 {
			continue
		}
		nLevelsWithTree++
		refs := make([]TreeRef, 0, len(lv.Speedtrees))
		for _, st := range lv.Speedtrees {
			inst := len(st.Transforms)
			nTreeRefs++
			nInstances += inst
			distinctTrees[st.Path().Value] += inst
			tr := TreeRef{
				Path:      fmt.Sprintf("0x%016x", st.Path().Value),
				Instances: inst,
				Layers:    len(st.Layers),
			}
			if mode == "dump" {
				for _, t := range st.Transforms {
					tr.Positions = append(tr.Positions, [4]float32{t.Position[0], t.Position[1], t.Position[2], t.Position[3]})
				}
			}
			refs = append(refs, tr)
		}
		result[fmt.Sprintf("0x%016x", fid.Name.Value)] = refs
	}

	fmt.Printf("=== speedtree scan ===\n")
	fmt.Printf("levels scanned        : %d\n", nLevels)
	fmt.Printf("levels WITH speedtree : %d\n", nLevelsWithTree)
	fmt.Printf("speedtree refs (total): %d\n", nTreeRefs)
	fmt.Printf("tree INSTANCES (total): %d\n", nInstances)
	fmt.Printf("distinct .speedtree   : %d\n", len(distinctTrees))

	// top trees by instance count
	type kv struct {
		h uint64
		n int
	}
	var tops []kv
	for h, n := range distinctTrees {
		tops = append(tops, kv{h, n})
	}
	sort.Slice(tops, func(i, j int) bool { return tops[i].n > tops[j].n })
	fmt.Printf("--- top 15 trees by instance count ---\n")
	for i, t := range tops {
		if i >= 15 {
			break
		}
		fmt.Printf("  0x%016x : %d instances\n", t.h, t.n)
	}

	if mode == "dump" {
		out := os.Args[3]
		b, _ := json.MarshalIndent(result, "", " ")
		os.WriteFile(out, b, 0644)
		fmt.Printf("wrote %s (%d levels with trees)\n", out, len(result))
	}
}
