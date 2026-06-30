// terrainraw: dump the RAW on-disk bytes of a terrain unit's textures region,
// to verify the TerrainTexture struct stride (488B) and whether UnkData is truly zero.
package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/xypwn/filediver/stingray"
)

func parseName(s string) stingray.Hash {
	if strings.HasPrefix(s, "0x") {
		if v, err := strconv.ParseUint(s[2:], 16, 64); err == nil {
			return stingray.Hash{Value: v}
		}
	}
	return stingray.Sum(s)
}

// Full header mirror so we can read TerrainInfoListOffset at its real offset.
type rawHeader struct {
	Unk00                 [8]byte
	Bones                 [8]byte
	GeometryGroup         [8]byte
	UnkHash00             [8]byte
	StateMachine          [8]byte
	Unk02                 [8]byte
	LODGroupListOffset    uint32
	JointListOffset       uint32
	LightListOffset       uint32
	UnkOffset02           uint32
	Unk03                 [12]byte
	UnkOffset03           uint32
	UnkOffset04           uint32
	Unk04                 [4]byte
	SkeletonMapListOffset uint32
	MeshLayoutListOffset  uint32
	MeshDataOffset        uint32
	MeshInfoListOffset    uint32
	TerrainInfoListOffset uint32
	Unk05                 [4]byte
	MaterialListOffset    uint32
}

type rawTerrainInfo struct {
	MinX, MinY, MinZ  float32
	MaxX, MaxY, MaxZ  float32
	UnkFloat          float32
	SomeCount         uint32
	UnkInt            uint32
	ParentBone        uint32
	Name              uint32
	QuadtreeOffset    uint32
	QuadtreeNodeCount uint32
	HeightmapOffset   uint32
	HeightmapBytes    uint32
	TexturesOffset    uint32
	TexturesCount     uint32
}

func main() {
	if len(os.Args) < 3 {
		fmt.Println("usage: terrainraw <gamedir/data> <0xhash>")
		os.Exit(1)
	}
	dd, err := stingray.OpenDataDir(context.Background(), os.Args[1], func(c, t int) {})
	if err != nil {
		panic(err)
	}
	fid := stingray.FileID{Name: parseName(os.Args[2]), Type: stingray.Sum("unit")}
	b, err := dd.Read(fid, stingray.DataMain)
	if err != nil {
		panic(err)
	}
	r := bytes.NewReader(b)

	var hdr rawHeader
	r.Seek(0, 0)
	binary.Read(r, binary.LittleEndian, &hdr)
	listOff := int(hdr.TerrainInfoListOffset)
	fmt.Printf("rawHeader size=%d  TerrainInfoListOffset=%d\n", binary.Size(rawHeader{}), listOff)

	r.Seek(int64(listOff), 0)
	var count, offset uint32
	binary.Read(r, binary.LittleEndian, &count)
	binary.Read(r, binary.LittleEndian, &offset)
	fmt.Printf("listOff=%d count=%d offset=%d  rawTerrainInfo size=%d\n", listOff, count, offset, binary.Size(rawTerrainInfo{}))

	var rt rawTerrainInfo
	base := uint32(listOff) + offset
	r.Seek(int64(base), 0)
	binary.Read(r, binary.LittleEndian, &rt)
	fmt.Printf("AABB %.1f..%.1f x %.1f..%.1f x %.1f..%.1f\n", rt.MinX, rt.MaxX, rt.MinY, rt.MaxY, rt.MinZ, rt.MaxZ)
	fmt.Printf("TexturesOffset=%d TexturesCount=%d HeightmapBytes=%d QuadCount=%d\n",
		rt.TexturesOffset, rt.TexturesCount, rt.HeightmapBytes, rt.QuadtreeNodeCount)

	texBase := base + rt.TexturesOffset
	// dump 2 textures worth assuming 488 stride, plus a little extra
	stride := 488
	total := int(rt.TexturesCount)*stride + 32
	if texBase+uint32(total) > uint32(len(b)) {
		total = len(b) - int(texBase)
	}
	region := b[texBase : texBase+uint32(total)]
	fmt.Printf("\n--- raw textures region @%d, %d bytes (stride assumed %d) ---\n", texBase, total, stride)
	// print first 16 bytes of each assumed layer (Path + Resolution + UnkInt), then summarize UnkData zeros
	for li := 0; li < int(rt.TexturesCount); li++ {
		off := li * stride
		if off+16 > len(region) {
			break
		}
		path := binary.LittleEndian.Uint64(region[off : off+8])
		res := binary.LittleEndian.Uint32(region[off+8 : off+12])
		uk := binary.LittleEndian.Uint32(region[off+12 : off+16])
		// count non-zero in the 472 UnkData
		nz := 0
		udStart := off + 16
		udEnd := off + stride
		if udEnd > len(region) {
			udEnd = len(region)
		}
		for _, by := range region[udStart:udEnd] {
			if by != 0 {
				nz++
			}
		}
		fmt.Printf("layer %d: Path=0x%016x Res=%d UnkInt=%d  UnkData_nonzero_bytes=%d/%d\n",
			li, path, res, uk, nz, udEnd-udStart)
	}

	// hex of first 96 bytes so we can eyeball the layout & confirm 2nd Path at +488
	fmt.Printf("\n--- hex first 96 bytes of textures region ---\n")
	for i := 0; i < 96 && i < len(region); i += 16 {
		end := i + 16
		if end > len(region) {
			end = len(region)
		}
		fmt.Printf("%04x: % x\n", i, region[i:end])
	}
	// hex around the assumed 2nd-layer Path (offset 488)
	if int(rt.TexturesCount) >= 2 && 488+16 <= len(region) {
		fmt.Printf("\n--- hex at offset 488 (assumed layer 1 Path) ---\n")
		fmt.Printf("01e8: % x\n", region[488:488+16])
	}
}
