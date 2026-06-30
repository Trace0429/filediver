// cmpheight: compare a tile's HeightmapData (R16, used for mesh) against its L0 texture
// (R16_UNORM) pixel values, to determine if L0 == heightmap or an independent map.
// Also report L1 (R32_FLOAT) value stats. 用法: cmpheight <gamedir/data> <0xhash>
package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/xypwn/filediver/dds"
	"github.com/xypwn/filediver/stingray"
	"github.com/xypwn/filediver/stingray/unit"
	sgtex "github.com/xypwn/filediver/stingray/unit/texture"
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

func textureDDS(dd *stingray.DataDir, name stingray.Hash) ([]byte, error) {
	fid := stingray.FileID{Name: name, Type: stingray.Sum("texture")}
	var rs []io.Reader
	for _, dt := range []stingray.DataType{stingray.DataMain, stingray.DataStream, stingray.DataGPU} {
		b, err := dd.Read(fid, dt)
		if err != nil {
			continue
		}
		rs = append(rs, bytes.NewReader(b))
	}
	r := io.MultiReader(rs...)
	if _, err := sgtex.DecodeInfo(r); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.Bytes(), nil
}

// read the top-mip raw pixel bytes out of a DDS (skip 128-byte DDS header + 20-byte DXT10 header)
func topMipBytes(ddsData []byte) ([]byte, error) {
	if !bytes.HasPrefix(ddsData, []byte("DDS ")) {
		return nil, fmt.Errorf("no DDS magic")
	}
	off := 128
	// detect DX10 extended header (fourCC at 84..88 == "DX10")
	if len(ddsData) >= 88 && string(ddsData[84:88]) == "DX10" {
		off += 20
	}
	return ddsData[off:], nil
}

func main() {
	if len(os.Args) < 3 {
		fmt.Println("usage: cmpheight <gamedir/data> <0xhash>")
		os.Exit(1)
	}
	dd, err := stingray.OpenDataDir(context.Background(), os.Args[1], func(c, t int) {})
	if err != nil {
		panic(err)
	}
	fid := stingray.FileID{Name: parseName(os.Args[2]), Type: stingray.Sum("unit")}
	mb, err := dd.Read(fid, stingray.DataMain)
	if err != nil {
		panic(err)
	}
	info, err := unit.LoadInfo(bytes.NewReader(mb))
	if err != nil || len(info.TerrainInfos) == 0 {
		panic("no terrain")
	}
	ti := info.TerrainInfos[0]
	res := int(ti.Textures[0].Resolution)
	n := res * res
	fmt.Printf("tile %s  res=%d  n=%d  AABB z=[%.2f..%.2f]\n", os.Args[2], res, n, ti.Min[2], ti.Max[2])

	// HeightmapData R16 values
	hm := make([]uint16, len(ti.HeightmapData)/2)
	binary.Decode(ti.HeightmapData, binary.LittleEndian, hm)

	// L0 texture R16_UNORM top mip
	dds0, err := textureDDS(dd, ti.Textures[0].Path)
	if err != nil {
		fmt.Printf("L0 dds err: %v\n", err)
		return
	}
	info0, _ := dds.DecodeInfo(bytes.NewReader(dds0))
	px0, _ := topMipBytes(dds0)
	l0 := make([]uint16, 0, n)
	if len(px0) >= n*2 {
		tmp := make([]uint16, n)
		binary.Decode(px0[:n*2], binary.LittleEndian, tmp)
		l0 = tmp
	}
	fmt.Printf("L0 %dx%d fmt(DXGI)=%v mips=%d\n", info0.Header.Width, info0.Header.Height,
		func() interface{} {
			if info0.DXT10Header != nil {
				return info0.DXT10Header.DXGIFormat
			}
			return "?"
		}(), info0.NumMipMaps)

	// compare hm vs l0
	if len(l0) == len(hm) && len(hm) == n {
		var maxAbs, sumAbs float64
		eq := 0
		for i := 0; i < n; i++ {
			d := math.Abs(float64(int(hm[i]) - int(l0[i])))
			if d == 0 {
				eq++
			}
			if d > maxAbs {
				maxAbs = d
			}
			sumAbs += d
		}
		fmt.Printf("HM vs L0: identical_texels=%d/%d  maxAbsDiff=%.0f  meanAbsDiff=%.2f\n", eq, n, maxAbs, sumAbs/float64(n))
		fmt.Printf("HM[0..8]=%v\n", hm[:min(8, n)])
		fmt.Printf("L0[0..8]=%v\n", l0[:min(8, n)])
		// also test: is L0 the SNORM-style or different scaling? print min/max of each
		hmMin, hmMax := uint16(65535), uint16(0)
		l0Min, l0Max := uint16(65535), uint16(0)
		for i := 0; i < n; i++ {
			if hm[i] < hmMin {
				hmMin = hm[i]
			}
			if hm[i] > hmMax {
				hmMax = hm[i]
			}
			if l0[i] < l0Min {
				l0Min = l0[i]
			}
			if l0[i] > l0Max {
				l0Max = l0[i]
			}
		}
		fmt.Printf("HM range [%d..%d]  L0 range [%d..%d]\n", hmMin, hmMax, l0Min, l0Max)
	} else {
		fmt.Printf("size mismatch: len(l0)=%d len(hm)=%d n=%d (L0 not plain R16 top mip?)\n", len(l0), len(hm), n)
	}

	// L1 R32_FLOAT stats
	if len(ti.Textures) > 1 {
		dds1, err := textureDDS(dd, ti.Textures[1].Path)
		if err == nil {
			px1, _ := topMipBytes(dds1)
			if len(px1) >= n*4 {
				f := make([]float32, n)
				binary.Decode(px1[:n*4], binary.LittleEndian, f)
				var mn, mx float32 = math.MaxFloat32, -math.MaxFloat32
				nz := 0
				hist := map[string]int{}
				for _, v := range f {
					if v != 0 {
						nz++
					}
					if v < mn {
						mn = v
					}
					if v > mx {
						mx = v
					}
					// bucket
					switch {
					case v == 0:
						hist["==0"]++
					case v > 0 && v <= 1:
						hist["(0,1]"]++
					case v > 1:
						hist[">1"]++
					default:
						hist["<0"]++
					}
				}
				fmt.Printf("L1 R32F: nonzero=%d/%d  range[%g..%g]  buckets=%v\n", nz, n, mn, mx, hist)
				fmt.Printf("L1[0..8]=%v\n", f[:min(8, n)])
			}
		}
	}
}
