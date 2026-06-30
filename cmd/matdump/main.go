// matdump: dump a material file's BaseMaterial + texture bindings + settings.
// 用法: matdump <gamedir/data> <0xhash或material路径> ...
package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/xypwn/filediver/stingray"
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

func texInfo(dd *stingray.DataDir, h stingray.Hash) string {
	fid := stingray.FileID{Name: h, Type: stingray.Sum("texture")}
	files := dd.Files[fid]
	if len(files) == 0 {
		return "(not a texture file)"
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

func main() {
	if len(os.Args) < 3 {
		fmt.Println("usage: matdump <gamedir/data> <0xhash或material路径> ...")
		os.Exit(1)
	}
	dd, err := stingray.OpenDataDir(context.Background(), os.Args[1], func(c, t int) {})
	if err != nil {
		panic(err)
	}
	for _, arg := range os.Args[2:] {
		h := parseName(arg)
		fid := stingray.FileID{Name: h, Type: stingray.Sum("material")}
		mb, err := dd.Read(fid, stingray.DataMain)
		if err != nil {
			fmt.Printf("\n== %s : material main read err %v\n", arg, err)
			continue
		}
		mat, err := material.LoadMain(bytes.NewReader(mb))
		if err != nil {
			fmt.Printf("\n== %s : LoadMain err %v\n", arg, err)
			continue
		}
		fmt.Printf("\n==== %s (0x%016x) ====\n", arg, h.Value)
		fmt.Printf("BaseMaterial = 0x%016x\n", mat.BaseMaterial.Value)
		fmt.Printf("Textures (%d):  [bindNameHash -> textureHash  info]\n", len(mat.Textures))
		for bind, texHash := range mat.Textures {
			fmt.Printf("  0x%08x -> 0x%016x   %s\n", bind.Value, texHash.Value, texInfo(dd, texHash))
		}
		fmt.Printf("Settings (%d):  [usageHash -> values]\n", len(mat.Settings))
		for usage, vals := range mat.Settings {
			fmt.Printf("  0x%08x = %v\n", usage.Value, vals)
		}
	}
}
