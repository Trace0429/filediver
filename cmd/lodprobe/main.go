// lodprobe: for each unit hash, read the unit and report which render-LOD levels it has,
// by parsing the GroupBones thinhash names for _LODN suffixes. No geometry baking.
// 用法: lodprobe <gamedir/data> <hashlist.txt>
// 输出: 每件 hash + 它的 LOD level 集合, 末尾统计分布. 写 _lod_probe.json.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/xypwn/filediver/hashes"
	"github.com/xypwn/filediver/stingray"
	"github.com/xypwn/filediver/stingray/unit"
)

// build thinhash->name map from the embedded thinhashes dictionary
func buildThinMap() map[stingray.ThinHash]string {
	m := map[stingray.ThinHash]string{}
	for _, line := range strings.Split(hashes.ThinHashes, "\n") {
		name := strings.TrimSpace(line)
		if name == "" || strings.HasPrefix(name, "//") {
			continue
		}
		m[stingray.Sum(name).Thin()] = name
	}
	return m
}

var lodRe = regexp.MustCompile(`(?i)_LOD(\d+)`)

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

// names that are proxy/collision/shadow/debris, not render LODs
func isNonRender(n string) bool {
	low := strings.ToLower(n)
	for _, k := range []string{"shadow", "collision", "navmesh", "navigation", "movement", "bullet", "debris", "rubble", "destruct", "proxy", "cull", "_vfx", "occluder"} {
		if strings.Contains(low, k) {
			return true
		}
	}
	return false
}

func main() {
	if len(os.Args) < 3 {
		fmt.Println("usage: lodprobe <gamedir/data> <hashlist.txt>")
		os.Exit(1)
	}
	dd, err := stingray.OpenDataDir(context.Background(), os.Args[1], func(c, t int) {})
	if err != nil {
		panic(err)
	}
	thinMap := buildThinMap()
	fmt.Printf("thinhash dict: %d names\n", len(thinMap))
	f, err := os.Open(os.Args[2])
	if err != nil {
		panic(err)
	}
	defer f.Close()

	type rec struct {
		Hash      string   `json:"hash"`
		RenderLODs []int   `json:"render_lods"`
		HasBase   bool     `json:"has_base"`   // a render group with no _LOD suffix (=LOD0)
		Groups    []string `json:"groups"`
	}
	var recs []rec
	distrib := map[string]int{} // sorted-lod-set string -> count
	failed := 0

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		h := parseName(line)
		mb, err := dd.Read(stingray.FileID{Name: h, Type: stingray.Sum("unit")}, stingray.DataMain)
		if err != nil {
			failed++
			continue
		}
		info, err := unit.LoadInfo(bytes.NewReader(mb))
		if err != nil {
			failed++
			continue
		}
		levelSet := map[int]bool{}
		hasBase := false
		var groupNames []string
		for _, bone := range info.GroupBones {
			name := bone.String()
			// try to resolve to readable name via built-in thin hashes
			if rn, ok := thinMap[bone]; ok {
				name = rn
			}
			groupNames = append(groupNames, name)
			if isNonRender(name) {
				continue
			}
			m := lodRe.FindStringSubmatch(name)
			if m != nil {
				if lv, e := strconv.Atoi(m[1]); e == nil {
					levelSet[lv] = true
				}
			} else {
				// a render group with no _LOD suffix = base mesh (treat as LOD0)
				hasBase = true
			}
		}
		var lods []int
		for lv := range levelSet {
			lods = append(lods, lv)
		}
		sort.Ints(lods)
		// build distribution key
		key := fmt.Sprintf("base=%v lods=%v", hasBase, lods)
		distrib[key]++
		if len(recs) < 60 { // keep a sample of detailed groups
			recs = append(recs, rec{Hash: line, RenderLODs: lods, HasBase: hasBase, Groups: groupNames})
		} else {
			recs = append(recs, rec{Hash: line, RenderLODs: lods, HasBase: hasBase})
		}
	}

	out := map[string]any{"failed": failed, "total": len(recs), "distribution": distrib, "records": recs}
	b, _ := json.MarshalIndent(out, "", " ")
	os.WriteFile("H:/gameres/HD2_extract/_lod_probe.json", b, 0644)

	fmt.Printf("probed %d units, failed %d\n", len(recs), failed)
	fmt.Println("LOD distribution (base + render-lod-set -> #units):")
	type kv struct {
		k string
		v int
	}
	var arr []kv
	for k, v := range distrib {
		arr = append(arr, kv{k, v})
	}
	sort.Slice(arr, func(i, j int) bool { return arr[i].v > arr[j].v })
	for _, e := range arr {
		fmt.Printf("  %-40s %d\n", e.k, e.v)
	}
}
