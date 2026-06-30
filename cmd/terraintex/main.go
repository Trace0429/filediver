// terraintex: dump a terrain unit's texture layers + decode UnkData[472] blend params.
// 用法: terraintex <gamedir/data> <0xhash或路径> ...
// 对每个 tile 打印: 每层 Path(打包贴图 hash)/Resolution/UnkInt + UnkData 按 float32 与 uint32 解码(只打非零带下标)。
package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/xypwn/filediver/stingray"
	"github.com/xypwn/filediver/stingray/unit"
)

func parseName(s string) stingray.Hash {
	if strings.HasPrefix(s, "0x") {
		if v, err := strconv.ParseUint(s[2:], 16, 64); err == nil {
			return stingray.Hash{Value: v}
		}
	}
	return stingray.Sum(s)
}

func main() {
	if len(os.Args) < 3 {
		fmt.Println("usage: terraintex <gamedir/data> <0xhash或路径> ...")
		os.Exit(1)
	}
	dd, err := stingray.OpenDataDir(context.Background(), os.Args[1], func(c, t int) {})
	if err != nil {
		panic(err)
	}

	for _, arg := range os.Args[2:] {
		fid := stingray.FileID{Name: parseName(arg), Type: stingray.Sum("unit")}
		mainBytes, err := dd.Read(fid, stingray.DataMain)
		if err != nil {
			fmt.Printf("\n==== %s : READ ERR %v ====\n", arg, err)
			continue
		}
		info, err := unit.LoadInfo(bytes.NewReader(mainBytes))
		if err != nil {
			fmt.Printf("\n==== %s : LOADINFO ERR %v ====\n", arg, err)
			continue
		}
		if len(info.TerrainInfos) == 0 {
			fmt.Printf("\n==== %s : no TerrainInfos ====\n", arg)
			continue
		}
		ti := info.TerrainInfos[0]
		dx := ti.Max[0] - ti.Min[0]
		dy := ti.Max[1] - ti.Min[1]
		dz := ti.Max[2] - ti.Min[2]
		hm := len(ti.HeightmapData)
		side := 0
		for s := 16; s <= 4096; s++ {
			if s*s*2 == hm {
				side = s
				break
			}
		}
		fmt.Printf("\n==== %s ====\n", arg)
		fmt.Printf("AABB %.1f x %.1f x %.1f m | heightmap %dx%d (%d B) | quad %d | layers %d\n",
			dx, dy, dz, side, side, hm, len(ti.QuadtreeNodes), len(ti.Textures))
		fmt.Printf("UnkFloat=%g SomeCount=%d UnkInt=%d ParentBone=%08x Name=%08x\n",
			ti.UnkFloat, ti.SomeCount, ti.UnkInt, ti.ParentBone.Value, ti.Name.Value)

		for li, tex := range ti.Textures {
			fmt.Printf("\n  --- layer %d ---\n", li)
			fmt.Printf("  Path=0x%016x  Resolution=%d  UnkInt=%d\n", tex.Path.Value, tex.Resolution, tex.UnkInt)

			// UnkData[472] -> as float32 (118 floats) and uint32 (118 ints)
			ud := tex.UnkData[:]
			nF := len(ud) / 4
			floats := make([]float32, nF)
			ints := make([]uint32, nF)
			_ = binary.Read(bytes.NewReader(ud), binary.LittleEndian, floats)
			_ = binary.Read(bytes.NewReader(ud), binary.LittleEndian, ints)

			fmt.Printf("  UnkData[472] non-zero (idx: float | uint | hex):\n")
			any := false
			for i := 0; i < nF; i++ {
				if ints[i] == 0 {
					continue
				}
				any = true
				f := floats[i]
				fstr := "NaN/inf"
				if !math.IsNaN(float64(f)) && !math.IsInf(float64(f), 0) {
					fstr = fmt.Sprintf("%g", f)
				}
				// byte offset = i*4
				fmt.Printf("    [%3d @0x%03x]  %-14s | %-11d | 0x%08x\n", i, i*4, fstr, ints[i], ints[i])
			}
			if !any {
				fmt.Printf("    (all zero)\n")
			}
		}
	}
}
