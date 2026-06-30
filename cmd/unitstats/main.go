// unitstats: 全量统计 unit 内部内容分布
// 用法: unitstats <gamedir/data>
package main

import (
	"bytes"
	"context"
	"fmt"
	"os"

	"github.com/xypwn/filediver/stingray"
	"github.com/xypwn/filediver/stingray/unit"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: unitstats <gamedir/data>")
		os.Exit(1)
	}
	dd, err := stingray.OpenDataDir(context.Background(), os.Args[1], func(c, t int) {})
	if err != nil {
		panic(err)
	}
	unitType := stingray.Sum("unit")

	var total, parseErr int
	var hasMesh, hasMat, hasSkel, hasBones, hasLight, hasTerrain, hasSharedGeo, emptyLogic, hasAnim int

	for fid := range dd.Files {
		if fid.Type != unitType {
			continue
		}
		total++
		mainBytes, err := dd.Read(fid, stingray.DataMain)
		if err != nil {
			continue
		}
		info, err := unit.LoadInfo(bytes.NewReader(mainBytes))
		if err != nil {
			parseErr++
			continue
		}
		gpuBytes, _ := dd.Read(fid, stingray.DataGPU)

		if info.NumMeshes > 0 {
			hasMesh++
		}
		if len(info.Materials) > 0 {
			hasMat++
		}
		if len(info.SkeletonMaps) > 0 {
			hasSkel++
		}
		if len(info.Bones) > 0 {
			hasBones++
		}
		if len(info.Lights) > 0 {
			hasLight++
		}
		if len(info.TerrainInfos) > 0 {
			hasTerrain++
		}
		if (info.BonesHash != stingray.Hash{}) || (info.StateMachine != stingray.Hash{}) {
			hasAnim++
		}
		if info.NumMeshes > 0 && len(gpuBytes) == 0 {
			hasSharedGeo++
		}
		if info.NumMeshes == 0 && len(info.TerrainInfos) == 0 && len(info.Lights) == 0 {
			emptyLogic++
		}
	}

	fmt.Printf("==== 全部 unit 内部内容分布 (共 %d 个, 解析失败 %d) ====\n", total, parseErr)
	p := func(name string, n int) {
		fmt.Printf("  %-28s %5d  (%4.1f%%)\n", name, n, 100*float64(n)/float64(total))
	}
	p("有 mesh (NumMeshes>0)", hasMesh)
	p("  其中共享几何(.gpu=0)", hasSharedGeo)
	p("有材质槽 (Materials>0)", hasMat)
	p("有蒙皮 (SkeletonMaps>0)", hasSkel)
	p("有节点/挂点 (Bones>0)", hasBones)
	p("有内嵌灯光 (Lights>0)", hasLight)
	p("有地形 (TerrainInfos>0)", hasTerrain)
	p("挂骨架或状态机(可动画)", hasAnim)
	p("纯逻辑件(无mesh/地形/灯)", emptyLogic)
}
