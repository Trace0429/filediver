// tiletex2png: extract a terrain tile's layer textures to PNG (+DDS) by reading the
// layer Path hashes as stingray "texture" files. Mirrors extractor/texture ExtractDDSData.
// 用法: tiletex2png <gamedir/data> <outdir> <0xhash或路径> ...
package main

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"io"
	"os"
	"path/filepath"
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

// assemble DDS bytes for a texture-typed file, like ExtractDDSData but using DataDir directly.
func textureDDS(dd *stingray.DataDir, name stingray.Hash) ([]byte, error) {
	fid := stingray.FileID{Name: name, Type: stingray.Sum("texture")}
	files := dd.Files[fid]
	if len(files) == 0 {
		return nil, fmt.Errorf("texture not found")
	}
	var rs []io.Reader
	for _, dt := range []stingray.DataType{stingray.DataMain, stingray.DataStream, stingray.DataGPU} {
		b, err := dd.Read(fid, dt)
		if err != nil {
			continue // type may not exist
		}
		rs = append(rs, bytes.NewReader(b))
	}
	r := io.MultiReader(rs...)
	if _, err := sgtex.DecodeInfo(r); err != nil {
		return nil, fmt.Errorf("decodeinfo: %w", err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func dumpLayer(dd *stingray.DataDir, outdir, tag string, name stingray.Hash) {
	ddsData, err := textureDDS(dd, name)
	if err != nil {
		fmt.Printf("    %s 0x%016x: DDS ERR %v\n", tag, name.Value, err)
		return
	}
	ddsPath := filepath.Join(outdir, fmt.Sprintf("%s_0x%016x.dds", tag, name.Value))
	os.WriteFile(ddsPath, ddsData, 0644)

	info, err := dds.DecodeInfo(bytes.NewReader(ddsData))
	if err == nil {
		dxgi := "-"
		if info.DXT10Header != nil {
			dxgi = fmt.Sprintf("DXGI=%d", info.DXT10Header.DXGIFormat)
		}
		fmt.Printf("    %s 0x%016x: %dx%d fourCC=%q %s mips=%d imgs=%d (dds %d B)\n",
			tag, name.Value, info.Header.Width, info.Header.Height,
			string(info.Header.PixelFormat.FourCC[:]), dxgi, info.NumMipMaps, info.NumImages, len(ddsData))
	}
	tex, err := dds.Decode(bytes.NewReader(ddsData), false)
	if err != nil {
		fmt.Printf("    %s decode->image ERR %v (DDS saved)\n", tag, err)
		return
	}
	var img = tex.Image
	if len(tex.Images) > 1 {
		img = dds.StackLayers(tex)
	}
	pngPath := filepath.Join(outdir, fmt.Sprintf("%s_0x%016x.png", tag, name.Value))
	f, err := os.Create(pngPath)
	if err != nil {
		fmt.Printf("    %s create png ERR %v\n", tag, err)
		return
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		fmt.Printf("    %s png encode ERR %v\n", tag, err)
		return
	}
	fmt.Printf("    %s -> %s\n", tag, pngPath)
}

func main() {
	if len(os.Args) < 4 {
		fmt.Println("usage: tiletex2png <gamedir/data> <outdir> <0xhash或路径> ...")
		os.Exit(1)
	}
	dd, err := stingray.OpenDataDir(context.Background(), os.Args[1], func(c, t int) {})
	if err != nil {
		panic(err)
	}
	outdir := os.Args[2]
	os.MkdirAll(outdir, 0755)

	for _, arg := range os.Args[3:] {
		fid := stingray.FileID{Name: parseName(arg), Type: stingray.Sum("unit")}
		mainBytes, err := dd.Read(fid, stingray.DataMain)
		if err != nil {
			fmt.Printf("\n== %s : unit read err %v\n", arg, err)
			continue
		}
		info, err := unit.LoadInfo(bytes.NewReader(mainBytes))
		if err != nil || len(info.TerrainInfos) == 0 {
			fmt.Printf("\n== %s : no terrain\n", arg)
			continue
		}
		ti := info.TerrainInfos[0]
		fmt.Printf("\n== %s : %d layers ==\n", arg, len(ti.Textures))
		for li, tx := range ti.Textures {
			dumpLayer(dd, outdir, fmt.Sprintf("L%d", li), tx.Path)
		}
	}
}
