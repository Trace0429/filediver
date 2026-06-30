// l1hist: histogram each channel (R,G,B,A) of a tile's material_map (L1) to decide
// whether it encodes discrete material IDs (few distinct values) or continuous weights.
// 用法: l1hist <gamedir/data> <0xtilehash> ...
package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

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

func topMip(ddsData []byte) []byte {
	off := 128
	if len(ddsData) >= 88 && string(ddsData[84:88]) == "DX10" {
		off += 20
	}
	return ddsData[off:]
}

func histChannel(px []byte, n, ch int) map[byte]int {
	h := map[byte]int{}
	for i := 0; i < n; i++ {
		idx := i*4 + ch
		if idx < len(px) {
			h[px[idx]]++
		}
	}
	return h
}

func printHist(name string, h map[byte]int, n int) {
	type kv struct {
		v byte
		c int
	}
	var arr []kv
	for v, c := range h {
		arr = append(arr, kv{v, c})
	}
	sort.Slice(arr, func(i, j int) bool { return arr[i].c > arr[j].c })
	fmt.Printf("  %s: %d distinct values. top:\n", name, len(arr))
	shown := 0
	for _, e := range arr {
		pct := 100.0 * float64(e.c) / float64(n)
		fmt.Printf("      val %3d (0x%02x): %8d  %5.1f%%\n", e.v, e.v, e.c, pct)
		shown++
		if shown >= 12 {
			fmt.Printf("      ... (%d more distinct)\n", len(arr)-12)
			break
		}
	}
}

func main() {
	if len(os.Args) < 3 {
		fmt.Println("usage: l1hist <gamedir/data> <0xtilehash> ...")
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
			fmt.Printf("\n== %s : unit err %v\n", arg, err)
			continue
		}
		info, err := unit.LoadInfo(bytes.NewReader(mb))
		if err != nil || len(info.TerrainInfos) == 0 || len(info.TerrainInfos[0].Textures) < 2 {
			fmt.Printf("\n== %s : no L1\n", arg)
			continue
		}
		ti := info.TerrainInfos[0]
		res := int(ti.Textures[0].Resolution)
		n := res * res
		dds1, err := textureDDS(dd, ti.Textures[1].Path)
		if err != nil {
			fmt.Printf("\n== %s : L1 dds err %v\n", arg, err)
			continue
		}
		px := topMip(dds1)
		fmt.Printf("\n==== %s : material_map %dx%d (n=%d, pxbytes=%d) ====\n", arg, res, res, n, len(px))
		printHist("R", histChannel(px, n, 0), n)
		printHist("G", histChannel(px, n, 1), n)
		printHist("B", histChannel(px, n, 2), n)
		printHist("A", histChannel(px, n, 3), n)
	}
}
