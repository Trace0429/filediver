// levelscan: scan ALL level files in the data, and for each, count how many of its
// referenced units are in the given terrain-hash set. Reports levels that contain
// terrain tiles (with counts + material-override presence). Also can dump one level's
// full unit list + transforms to JSON.
// 用法:
//   levelscan <gamedir/data> <terrain_hashes.txt>            # scan all levels, find which contain terrain
//   levelscan <gamedir/data> <terrain_hashes.txt> dump <0xlevelhash> <out.json>
package main

import (
	"bytes"
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/xypwn/filediver/stingray"
	"github.com/xypwn/filediver/stingray/level"
	"github.com/xypwn/filediver/stingray/prefab"
)

func parseHash(s string) stingray.Hash {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "0x") {
		s = s[2:]
	}
	if v, err := strconv.ParseUint(s, 16, 64); err == nil && len(s) == 16 {
		return stingray.Hash{Value: v}
	}
	return stingray.Sum(s)
}

func loadTerrainSet(path string) map[uint64]bool {
	set := map[uint64]bool{}
	f, err := os.Open(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		set[parseHash(line).Value] = true
	}
	return set
}

func main() {
	if len(os.Args) < 3 {
		fmt.Println("usage: levelscan <gamedir/data> <terrain_hashes.txt> [dump <0xlevelhash> <out.json>]")
		os.Exit(1)
	}
	dd, err := stingray.OpenDataDir(context.Background(), os.Args[1], func(c, t int) {})
	if err != nil {
		panic(err)
	}
	terrain := loadTerrainSet(os.Args[2])
	fmt.Printf("terrain set: %d hashes\n", len(terrain))

	levelType := stingray.Sum("level")

	// dump mode
	if len(os.Args) >= 6 && os.Args[3] == "dump" {
		lh := parseHash(os.Args[4])
		fid := stingray.FileID{Name: lh, Type: levelType}
		mb, err := dd.Read(fid, stingray.DataMain)
		if err != nil {
			panic(err)
		}
		lv, err := level.LoadLevel(bytes.NewReader(mb))
		if err != nil {
			panic(err)
		}
		type U struct {
			Path    string     `json:"path"`
			Pos     [3]float32 `json:"pos"`
			Rot     [4]float32 `json:"rot"`
			Scale   [3]float32 `json:"scale"`
			Terrain bool       `json:"is_terrain"`
		}
		out := struct {
			Name      string `json:"name"`
			NumUnits  int    `json:"num_units"`
			NumPrefab int    `json:"num_prefabs"`
			NumMatOvr int    `json:"num_material_overrides"`
			Units     []U    `json:"units"`
		}{Name: fmt.Sprintf("0x%016x", lv.Name.Value), NumUnits: len(lv.Units), NumPrefab: len(lv.Prefabs), NumMatOvr: len(lv.MaterialOverrides)}
		for _, u := range lv.Units {
			out.Units = append(out.Units, U{
				Path:    fmt.Sprintf("0x%016x", u.Path().Value),
				Pos:     u.PositionVec,
				Rot:     u.RotationVec,
				Scale:   u.ScaleVec,
				Terrain: terrain[u.Path().Value],
			})
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		os.WriteFile(os.Args[5], b, 0644)
		fmt.Printf("dumped %d units (%d prefabs, %d matOverrides) -> %s\n", len(lv.Units), len(lv.Prefabs), len(lv.MaterialOverrides), os.Args[5])
		return
	}

	// dumpall mode: dump every terrain-containing level into one combined JSON
	if len(os.Args) >= 4 && os.Args[3] == "dumpall" {
		outPath := "H:/gameres/HD2_extract/_all_terrain_levels.json"
		if len(os.Args) >= 5 {
			outPath = os.Args[4]
		}
		type U struct {
			Path    string     `json:"path"`
			Pos     [3]float32 `json:"pos"`
			Rot     [4]float32 `json:"rot"`
			Scale   [3]float32 `json:"scale"`
			Terrain bool       `json:"t,omitempty"`
		}
		type P struct {
			Path  string     `json:"path"`
			Pos   [3]float32 `json:"pos"`
			Rot   [4]float32 `json:"rot"`
			Scale [3]float32 `json:"scale"`
		}
		type L struct {
			Units        []U `json:"units"`
			Prefabs      []P `json:"prefabs"`
			MatOverrides int `json:"mat_overrides"`
		}
		result := map[string]L{}
		nLevels, nTerrain := 0, 0
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
			hasT := false
			var us []U
			for _, u := range lv.Units {
				t := terrain[u.Path().Value]
				if t {
					hasT = true
				}
				us = append(us, U{
					Path: fmt.Sprintf("0x%016x", u.Path().Value),
					Pos:  u.PositionVec, Rot: u.RotationVec, Scale: u.ScaleVec, Terrain: t,
				})
			}
			if !hasT {
				continue
			}
			nLevels++
			var ps []P
			for _, pf := range lv.Prefabs {
				ps = append(ps, P{Path: fmt.Sprintf("0x%016x", pf.Path.Value), Pos: pf.PositionVec, Rot: pf.RotationVec, Scale: pf.ScaleVec})
			}
			for _, u := range us {
				if u.Terrain {
					nTerrain++
				}
			}
			result[fmt.Sprintf("0x%016x", fid.Name.Value)] = L{Units: us, Prefabs: ps, MatOverrides: len(lv.MaterialOverrides)}
		}
		f, err := os.Create(outPath)
		if err != nil {
			panic(err)
		}
		enc := json.NewEncoder(f)
		enc.SetIndent("", " ")
		enc.Encode(result)
		f.Close()
		fmt.Printf("dumpall: %d terrain-levels, %d terrain-tile refs -> %s\n", nLevels, nTerrain, outPath)
		return
	}

	// dumpnoterrain mode: dump every level that does NOT contain any terrain tile (the 625
	// interior/ship/platform/logic levels). Same JSON shape as dumpall.
	if len(os.Args) >= 4 && os.Args[3] == "dumpnoterrain" {
		outPath := "H:/gameres/HD2_extract/_all_noterrain_levels.json"
		if len(os.Args) >= 5 {
			outPath = os.Args[4]
		}
		type U struct {
			Path    string     `json:"path"`
			Pos     [3]float32 `json:"pos"`
			Rot     [4]float32 `json:"rot"`
			Scale   [3]float32 `json:"scale"`
			Terrain bool       `json:"t,omitempty"`
		}
		type P struct {
			Path  string     `json:"path"`
			Pos   [3]float32 `json:"pos"`
			Rot   [4]float32 `json:"rot"`
			Scale [3]float32 `json:"scale"`
		}
		type L struct {
			Units        []U `json:"units"`
			Prefabs      []P `json:"prefabs"`
			MatOverrides int `json:"mat_overrides"`
		}
		result := map[string]L{}
		nLevels := 0
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
			hasT := false
			var us []U
			for _, u := range lv.Units {
				t := terrain[u.Path().Value]
				if t {
					hasT = true
				}
				us = append(us, U{
					Path: fmt.Sprintf("0x%016x", u.Path().Value),
					Pos:  u.PositionVec, Rot: u.RotationVec, Scale: u.ScaleVec, Terrain: t,
				})
			}
			if hasT {
				continue // skip terrain levels; those are the 1231 cells
			}
			nLevels++
			var ps []P
			for _, pf := range lv.Prefabs {
				ps = append(ps, P{Path: fmt.Sprintf("0x%016x", pf.Path.Value), Pos: pf.PositionVec, Rot: pf.RotationVec, Scale: pf.ScaleVec})
			}
			result[fmt.Sprintf("0x%016x", fid.Name.Value)] = L{Units: us, Prefabs: ps, MatOverrides: len(lv.MaterialOverrides)}
		}
		f, err := os.Create(outPath)
		if err != nil {
			panic(err)
		}
		enc := json.NewEncoder(f)
		enc.SetIndent("", " ")
		enc.Encode(result)
		f.Close()
		fmt.Printf("dumpnoterrain: %d non-terrain levels -> %s\n", nLevels, outPath)
		return
	}

	// findrefs mode: which levels/prefabs REFERENCE any hash in the target set (arg2)?
	// Use this to find the master/outer structure that places the cell-levels.
	if len(os.Args) >= 4 && os.Args[3] == "findrefs" {
		target := terrain // reuse arg2 set as the "targets" we look for as references
		levelHits, prefabHits := 0, 0
		totalRefs := 0
		prefabType := stingray.Sum("prefab")
		// scan all levels
		for fid := range dd.Files {
			if fid.Type == levelType {
				mb, err := dd.Read(fid, stingray.DataMain)
				if err != nil {
					continue
				}
				lv, err := level.LoadLevel(bytes.NewReader(mb))
				if err != nil {
					continue
				}
				hit := 0
				for _, u := range lv.Units {
					if target[u.Path().Value] {
						hit++
					}
				}
				for _, p := range lv.Prefabs {
					if target[p.Path.Value] {
						hit++
					}
				}
				for _, c := range lv.UnkExtraUnitContainers {
					for _, eu := range c.ExtraUnits {
						if target[eu.Path.Value] {
							hit++
						}
					}
					for _, ep := range c.ExtraPrefabs {
						if target[ep.Path.Value] {
							hit++
						}
					}
				}
				if hit > 0 {
					levelHits++
					totalRefs += hit
					if levelHits <= 40 {
						fmt.Printf("LEVEL  0x%016x references %d target(s)\n", fid.Name.Value, hit)
					}
				}
			}
			if fid.Type == prefabType {
				mb, err := dd.Read(fid, stingray.DataMain)
				if err != nil {
					continue
				}
				pf, err := prefab.Load(bytes.NewReader(mb))
				if err != nil {
					continue
				}
				hit := 0
				for _, u := range pf.Units {
					if target[u.Path().Value] {
						hit++
					}
				}
				if hit > 0 {
					prefabHits++
					totalRefs += hit
					if prefabHits <= 40 {
						fmt.Printf("PREFAB 0x%016x references %d target(s)\n", fid.Name.Value, hit)
					}
				}
			}
		}
		fmt.Printf("\nfindrefs: %d levels + %d prefabs reference targets; total refs=%d (target set=%d)\n",
			levelHits, prefabHits, totalRefs, len(target))
		return
	}

	// scan mode: iterate all level files
	type hit struct {
		level     uint64
		nUnits    int
		nTerrain  int
		nMatOvr   int
		nPrefab   int
	}
	var hits []hit
	scanned := 0
	failed := 0
	// gather all level FileIDs from dd.Files
	for fid := range dd.Files {
		if fid.Type != levelType {
			continue
		}
		mb, err := dd.Read(fid, stingray.DataMain)
		if err != nil {
			failed++
			continue
		}
		lv, err := level.LoadLevel(bytes.NewReader(mb))
		if err != nil {
			failed++
			continue
		}
		scanned++
		nt := 0
		for _, u := range lv.Units {
			if terrain[u.Path().Value] {
				nt++
			}
		}
		if nt > 0 {
			hits = append(hits, hit{fid.Name.Value, len(lv.Units), nt, len(lv.MaterialOverrides), len(lv.Prefabs)})
		}
	}
	fmt.Printf("scanned %d levels (%d failed). levels containing terrain tiles: %d\n", scanned, failed, len(hits))
	// write all hit level hashes to a sibling file of the terrain-hashes arg dir
	hitsPath := "H:/gameres/HD2_extract/_terrain_levels.txt"
	if hf, err := os.Create(hitsPath); err == nil {
		for _, h := range hits {
			fmt.Fprintf(hf, "0x%016x\n", h.level)
		}
		hf.Close()
		fmt.Printf("wrote %d level hashes -> %s\n", len(hits), hitsPath)
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].nTerrain > hits[j].nTerrain })
	fmt.Printf("\n%-20s %8s %8s %8s %8s\n", "level", "units", "terrain", "matOvr", "prefabs")
	totalTerrainRefs := 0
	for i, h := range hits {
		totalTerrainRefs += h.nTerrain
		if i < 60 {
			fmt.Printf("0x%016x %8d %8d %8d %8d\n", h.level, h.nUnits, h.nTerrain, h.nMatOvr, h.nPrefab)
		}
	}
	if len(hits) > 60 {
		fmt.Printf("... (%d more)\n", len(hits)-60)
	}
	fmt.Printf("\nTOTAL terrain-tile references across all levels: %d (set size %d)\n", totalTerrainRefs, len(terrain))
}
