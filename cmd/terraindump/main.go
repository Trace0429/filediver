// terraindump: 打印地形 unit 的 AABB 尺寸/高度图/四叉树, 判断地块大小
// 用法: terraindump <gamedir/data> <0xhash或路径> ...
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
		fmt.Println("usage: terraindump <gamedir/data> <0xhash或路径> ...")
		os.Exit(1)
	}
	dd, err := stingray.OpenDataDir(context.Background(), os.Args[1], func(c, t int) {})
	if err != nil {
		panic(err)
	}
	fmt.Printf("%-22s %-12s %-12s %-12s %-9s %-7s %s\n", "unit", "size_X(m)", "size_Y(m)", "size_Z(m)", "heightmap", "quad", "textures")
	for _, arg := range os.Args[2:] {
		fid := stingray.FileID{Name: parseName(arg), Type: stingray.Sum("unit")}
		mainBytes, err := dd.Read(fid, stingray.DataMain)
		if err != nil {
			continue
		}
		info, err := unit.LoadInfo(bytes.NewReader(mainBytes))
		if err != nil || len(info.TerrainInfos) == 0 {
			continue
		}
		ti := info.TerrainInfos[0]
		dx := (ti.Max[0] - ti.Min[0])
		dy := (ti.Max[1] - ti.Min[1])
		dz := (ti.Max[2] - ti.Min[2])
		hm := len(ti.HeightmapData)
		// 高度图边长 = sqrt(bytes/2)  (R16)
		side := 0
		for s := 16; s <= 2048; s++ {
			if s*s*2 == hm {
				side = s
				break
			}
		}
		label := arg
		if len(label) > 20 {
			label = label[:20]
		}
		fmt.Printf("%-22s %-12.1f %-12.1f %-12.1f %dx%d(%d) %-7d %d\n",
			label, dx, dy, dz, side, side, hm, len(ti.QuadtreeNodes), len(ti.Textures))
	}
}
