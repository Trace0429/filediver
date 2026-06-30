// terrainmat: for a terrain tile unit, dump the material(s) it references:
// BaseMaterial + texture bindings (bindingNameHash -> textureHash) + settings.
// Resolves hashes to names via hashes/thinhashes dict if loadable.
// 用法: terrainmat <gamedir/data> <0xhash或路径> ...
package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/xypwn/filediver/stingray"
	"github.com/xypwn/filediver/stingray/unit"
	"github.com/xypwn/filediver/stingray/unit/material"
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

func texExists(dd *stingray.DataDir, h stingray.Hash) string {
	fid := stingray.FileID{Name: h, Type: stingray.Sum("texture")}
	files := dd.Files[fid]
	if len(files) == 0 {
		return "NOT-a-texture"
	}
	fi := files[0]
	var parts []string
	for _, dt := range []struct {
		t stingray.DataType
		n string
	}{{stingray.DataMain, "m"}, {stingray.DataStream, "s"}, {stingray.DataGPU, "g"}} {
		if fi.Files[dt.t].Exists() {
			parts = append(parts, fmt.Sprintf("%s=%d", dt.n, fi.Files[dt.t].Size))
		}
	}
	return "texture[" + strings.Join(parts, " ") + "]"
}

func loadMaterial(dd *stingray.DataDir, matHash stingray.Hash) (*material.Material, error) {
	fid := stingray.FileID{Name: matHash, Type: stingray.Sum("material")}
	mb, err := dd.Read(fid, stingray.DataMain)
	if err != nil {
		return nil, fmt.Errorf("read main: %w", err)
	}
	return material.LoadMain(bytes.NewReader(mb))
}

func main() {
	if len(os.Args) < 3 {
		fmt.Println("usage: terrainmat <gamedir/data> <0xhash或路径> ...")
		os.Exit(1)
	}
	dd, err := stingray.OpenDataDir(context.Background(), os.Args[1], func(c, t int) {})
	if err != nil {
		panic(err)
	}
	for _, arg := range os.Args[2:] {
		fid := stingray.FileID{Name: parseName(arg), Type: stingray.Sum("unit")}
		mb, err := dd.Read(fid, stingray.DataMain)
		if err != nil {
			fmt.Printf("\n== %s : unit read err %v\n", arg, err)
			continue
		}
		info, err := unit.LoadInfo(bytes.NewReader(mb))
		if err != nil {
			fmt.Printf("\n== %s : loadinfo err %v\n", arg, err)
			continue
		}
		fmt.Printf("\n==== %s ====\n", arg)
		fmt.Printf("terrainInfos=%d  unit.Materials count=%d\n", len(info.TerrainInfos), len(info.Materials))
		if len(info.TerrainInfos) > 0 {
			fmt.Printf("terrain layers (texture Paths): ")
			for _, tx := range info.TerrainInfos[0].Textures {
				fmt.Printf("0x%016x ", tx.Path.Value)
			}
			fmt.Println()
		}
		// dump each material the unit references
		for nameHash, matHash := range info.Materials {
			fmt.Printf("\n  unit.Material binding 0x%08x -> material 0x%016x\n", nameHash.Value, matHash.Value)
			mat, err := loadMaterial(dd, matHash)
			if err != nil {
				fmt.Printf("    load material err: %v\n", err)
				continue
			}
			fmt.Printf("    BaseMaterial = 0x%016x\n", mat.BaseMaterial.Value)
			fmt.Printf("    Textures (%d):\n", len(mat.Textures))
			for bind, texHash := range mat.Textures {
				fmt.Printf("      bind 0x%08x -> 0x%016x   %s\n", bind.Value, texHash.Value, texExists(dd, texHash))
			}
			fmt.Printf("    Settings (%d):\n", len(mat.Settings))
			for usage, vals := range mat.Settings {
				fmt.Printf("      0x%08x = %v\n", usage.Value, vals)
			}
		}
	}
}
