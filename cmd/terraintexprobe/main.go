// terraintexprobe: for given tile(s), take each terrain layer's Path hash and check
// whether it exists as a "texture" file (and report main/gpu/stream sizes + DDS header info).
// This decides whether per-tile terrain textures can be extracted directly.
// 用法: terraintexprobe <gamedir/data> <0xhash或路径> ...
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
	"github.com/xypwn/filediver/stingray/unit"
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

func probeType(dd *stingray.DataDir, name stingray.Hash, typeName string) {
	fid := stingray.FileID{Name: name, Type: stingray.Sum(typeName)}
	files := dd.Files[fid]
	if len(files) == 0 {
		fmt.Printf("      [%-10s] NOT FOUND\n", typeName)
		return
	}
	fi := files[0]
	var parts []string
	for _, dt := range []struct {
		t stingray.DataType
		n string
	}{{stingray.DataMain, "main"}, {stingray.DataStream, "stream"}, {stingray.DataGPU, "gpu"}} {
		if fi.Files[dt.t].Exists() {
			parts = append(parts, fmt.Sprintf("%s=%d", dt.n, fi.Files[dt.t].Size))
		}
	}
	fmt.Printf("      [%-10s] EXISTS  %s\n", typeName, strings.Join(parts, " "))

	// if texture, peek at main bytes (stingray texture header) to get dims/format if possible
	if typeName == "texture" {
		mb, err := dd.Read(fid, stingray.DataMain)
		if err == nil && len(mb) >= 16 {
			// stingray texture main starts with a small header; dump first 32 bytes hex
			n := 48
			if n > len(mb) {
				n = len(mb)
			}
			fmt.Printf("        main[0:%d] % x\n", n, mb[:n])
			// many stingray textures embed a DDS header after a small prefix.
			// search for 'DDS ' magic
			idx := bytes.Index(mb, []byte("DDS "))
			if idx >= 0 && idx+128 <= len(mb) {
				h := mb[idx:]
				height := binary.LittleEndian.Uint32(h[12:16])
				width := binary.LittleEndian.Uint32(h[16:20])
				// fourCC at offset 84
				fourcc := h[84:88]
				fmt.Printf("        DDS@%d  %dx%d  fourCC=%q\n", idx, width, height, string(fourcc))
			}
		}
	}
}

func main() {
	if len(os.Args) < 3 {
		fmt.Println("usage: terraintexprobe <gamedir/data> <0xhash或路径> ...")
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
			fmt.Printf("\n==== %s : unit READ ERR %v ====\n", arg, err)
			continue
		}
		info, err := unit.LoadInfo(bytes.NewReader(mainBytes))
		if err != nil || len(info.TerrainInfos) == 0 {
			fmt.Printf("\n==== %s : no terrain ====\n", arg)
			continue
		}
		ti := info.TerrainInfos[0]
		fmt.Printf("\n==== %s : %d layers ====\n", arg, len(ti.Textures))
		for li, tx := range ti.Textures {
			fmt.Printf("  layer %d  Path=0x%016x  Resolution=%d  UnkInt=%d\n", li, tx.Path.Value, tx.Resolution, tx.UnkInt)
			// probe several plausible types
			for _, tn := range []string{"texture", "material", "unit", "bik_video", "raw_data"} {
				probeType(dd, tx.Path, tn)
			}
		}
	}
}
