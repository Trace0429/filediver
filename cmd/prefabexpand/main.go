// prefabexpand: given prefab hash(es), recursively list all referenced unit hashes + transforms.
// 用法: prefabexpand <gamedir/data> <0xprefabhash> ...
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/xypwn/filediver/stingray"
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

func main() {
	if len(os.Args) < 3 {
		fmt.Println("usage: prefabexpand <gamedir/data> <0xprefabhash> ...")
		os.Exit(1)
	}
	dd, err := stingray.OpenDataDir(context.Background(), os.Args[1], func(c, t int) {})
	if err != nil {
		panic(err)
	}
	prefabType := stingray.Sum("prefab")
	type Child struct {
		Path  string     `json:"path"`
		Pos   [3]float32 `json:"pos"`
		Rot   [4]float32 `json:"rot"`
		Scale [3]float32 `json:"scale"`
	}
	out := map[string][]Child{}
	for _, arg := range os.Args[2:] {
		h := parseHash(arg)
		fid := stingray.FileID{Name: h, Type: prefabType}
		mb, err := dd.Read(fid, stingray.DataMain)
		if err != nil {
			fmt.Printf("0x%016x : read err %v\n", h.Value, err)
			continue
		}
		pf, err := prefab.Load(bytes.NewReader(mb))
		if err != nil {
			fmt.Printf("0x%016x : parse err %v\n", h.Value, err)
			continue
		}
		key := fmt.Sprintf("0x%016x", h.Value)
		var children []Child
		for _, u := range pf.Units {
			children = append(children, Child{
				Path:  fmt.Sprintf("0x%016x", u.Path().Value),
				Pos:   u.PositionVec, Rot: u.RotationVec, Scale: u.ScaleVec,
			})
		}
		out[key] = children
		fmt.Printf("prefab %s : %d units, %d nested\n", key, len(pf.Units), len(pf.NestedPrefabs))
	}
	// emit JSON to fixed path
	jf, _ := os.Create("H:/gameres/HD2_extract/_prefab_contents.json")
	enc := json.NewEncoder(jf)
	enc.SetIndent("", " ")
	enc.Encode(out)
	jf.Close()
	fmt.Printf("wrote _prefab_contents.json (%d prefabs)\n", len(out))
}
