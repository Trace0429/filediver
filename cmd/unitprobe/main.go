// unitprobe: 一次性探针 — 打印一个 unit 的完整内部结构 (段/几何/材质/骨架/地形)
// 用法: unitprobe <gamedir> <unit路径或0xhash> [更多unit...]
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
		v, err := strconv.ParseUint(s[2:], 16, 64)
		if err == nil {
			return stingray.Hash{Value: v}
		}
	}
	return stingray.Sum(s)
}

func main() {
	if len(os.Args) < 3 {
		fmt.Println("usage: unitprobe <gamedir> <unit路径或0xhash> [...]")
		os.Exit(1)
	}
	gamedir := os.Args[1]
	dd, err := stingray.OpenDataDir(context.Background(), gamedir, func(c, t int) {})
	if err != nil {
		panic(err)
	}
	unitTypeHash := stingray.Sum("unit").Thin()

	for _, arg := range os.Args[2:] {
		nameHash := parseName(arg)
		fid := stingray.FileID{Name: nameHash, Type: stingray.Sum("unit")}
		_ = unitTypeHash
		mainBytes, err := dd.Read(fid, stingray.DataMain)
		if err != nil {
			fmt.Printf("\n### %s -> READ ERR: %v\n", arg, err)
			continue
		}
		gpuBytes, _ := dd.Read(fid, stingray.DataGPU)
		info, err := unit.LoadInfo(bytes.NewReader(mainBytes))
		if err != nil {
			fmt.Printf("\n### %s -> PARSE ERR: %v\n", arg, err)
			continue
		}
		fmt.Printf("\n========================================================\n")
		fmt.Printf("### UNIT: %s\n", arg)
		fmt.Printf("    .main = %d bytes,  .gpu = %d bytes\n", len(mainBytes), len(gpuBytes))
		fmt.Printf("--- 外部引用 (Header 顶部的资源 Hash) ---\n")
		fmt.Printf("    Bones(骨架)      = %s\n", info.BonesHash.String())
		fmt.Printf("    StateMachine(状态机) = %s\n", info.StateMachine.String())
		fmt.Printf("    GeometryGroup    = %s\n", info.GeometryGroup.String())
		fmt.Printf("--- 段统计 ---\n")
		fmt.Printf("    LODGroups   = %d\n", len(info.LODGroups))
		fmt.Printf("    SkeletonMaps= %d\n", len(info.SkeletonMaps))
		fmt.Printf("    Bones(关节) = %d\n", len(info.Bones))
		fmt.Printf("    Lights(灯光)= %d\n", len(info.Lights))
		fmt.Printf("    Materials   = %d (槽位)\n", len(info.Materials))
		fmt.Printf("    NumMeshes   = %d\n", info.NumMeshes)
		fmt.Printf("    MeshInfos   = %d\n", len(info.MeshInfos))
		fmt.Printf("    MeshLayouts = %d\n", len(info.MeshLayouts))
		fmt.Printf("    TerrainInfos= %d\n", len(info.TerrainInfos))
		fmt.Printf("    GroupBones  = %d\n", len(info.GroupBones))

		// 材质槽
		if len(info.Materials) > 0 {
			fmt.Printf("--- 材质槽 (usage thinhash -> material hash) ---\n")
			for usage, mat := range info.Materials {
				fmt.Printf("    %s -> %s\n", usage.String(), mat.String())
			}
		}
		// LOD 组
		for i, lg := range info.LODGroups {
			fmt.Printf("--- LODGroup[%d]: %d entries, %d footers ---\n", i, len(lg.Entries), len(lg.Footers))
			for j, e := range lg.Entries {
				fmt.Printf("      LOD%d: detail %.1f..%.1f, %d indices\n", j, e.Detail.Min, e.Detail.Max, len(e.Indices))
			}
		}
		// Mesh 布局 (顶点属性)
		for i, ml := range info.MeshLayouts {
			items := []string{}
			for k := 0; k < int(ml.NumItems); k++ {
				it := ml.Items[k]
				items = append(items, fmt.Sprintf("%v(%v,L%d)", it.Type, it.Format, it.Layer))
			}
			skin := "无蒙皮"
			for k := 0; k < int(ml.NumItems); k++ {
				if ml.Items[k].Type == unit.ItemBoneWeight {
					skin = "有蒙皮"
				}
			}
			fmt.Printf("--- MeshLayout[%d]: %d verts, stride %d, %d indices [%s] %s\n      attrs: %s\n",
				i, ml.NumVertices, ml.VertexStride, ml.NumIndices, skin,
				"", strings.Join(items, " "))
		}
		// MeshInfo (每段 mesh 用哪些材质/几何组)
		for i, mi := range info.MeshInfos {
			fmt.Printf("--- MeshInfo[%d]: type=%v, %d materials, %d groups, layoutIdx=%d, skeletonMapIdx=%d\n",
				i, mi.Header.MeshType, len(mi.Materials), len(mi.Groups), mi.Header.LayoutIdx, mi.Header.SkeletonMapIdx)
		}
		// 地形
		for i, ti := range info.TerrainInfos {
			fmt.Printf("--- TerrainInfo[%d]: parentBone=%s name=%s, quadtree %d nodes, heightmap %d bytes, %d textures\n",
				i, ti.ParentBone.String(), ti.Name.String(), len(ti.QuadtreeNodes), len(ti.HeightmapData), len(ti.Textures))
			for j, tx := range ti.Textures {
				if j >= 4 {
					fmt.Printf("      ... (+%d more)\n", len(ti.Textures)-4)
					break
				}
				fmt.Printf("      tex[%d]: %s, res=%d\n", j, tx.Path.String(), tx.Resolution)
			}
		}
		// 灯光
		for i, l := range info.Lights {
			if i >= 3 {
				fmt.Printf("    ... (+%d more lights)\n", len(info.Lights)-3)
				break
			}
			fmt.Printf("--- Light[%d]: type=%v color=%v intensity=%.1f bone=%d\n", i, l.Type, l.Color, l.Intensity, l.BoneIndex)
		}
	}
}
