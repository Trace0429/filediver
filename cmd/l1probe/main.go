// l1probe: decode tile layer1 (DXGI R32_FLOAT container) as RGBA8 bytes to see its real content.
// Also re-export L1 as an RGBA PNG (treating 4 bytes/texel as R,G,B,A).
// 用法: l1probe <gamedir/data> <outdir> <0xhash>
package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
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

func topMipBytes(ddsData []byte) ([]byte, int) {
	off := 128
	if len(ddsData) >= 88 && string(ddsData[84:88]) == "DX10" {
		off += 20
	}
	return ddsData[off:], off
}

func main() {
	if len(os.Args) < 4 {
		fmt.Println("usage: l1probe <gamedir/data> <outdir> <0xhash>")
		os.Exit(1)
	}
	dd, err := stingray.OpenDataDir(context.Background(), os.Args[1], func(c, t int) {})
	if err != nil {
		panic(err)
	}
	outdir := os.Args[2]
	os.MkdirAll(outdir, 0755)
	fid := stingray.FileID{Name: parseName(os.Args[3]), Type: stingray.Sum("unit")}
	mb, _ := dd.Read(fid, stingray.DataMain)
	info, err := unit.LoadInfo(bytes.NewReader(mb))
	if err != nil || len(info.TerrainInfos) == 0 || len(info.TerrainInfos[0].Textures) < 2 {
		panic("no terrain/L1")
	}
	ti := info.TerrainInfos[0]
	res := int(ti.Textures[0].Resolution)
	n := res * res

	dds1, err := textureDDS(dd, ti.Textures[1].Path)
	if err != nil {
		panic(err)
	}
	info1, _ := dds.DecodeInfo(bytes.NewReader(dds1))
	px, hdrOff := topMipBytes(dds1)
	fmt.Printf("L1 0x%016x  %dx%d  DXGI=%v  hdrOff=%d  pxbytes=%d  need(RGBA8)=%d\n",
		ti.Textures[1].Path.Value, info1.Header.Width, info1.Header.Height,
		func() interface{} { if info1.DXT10Header != nil { return info1.DXT10Header.DXGIFormat }; return "?" }(),
		hdrOff, len(px), n*4)

	// dump first 8 texels as raw 4 bytes
	fmt.Printf("first 8 texels (raw 4 bytes each):\n")
	for i := 0; i < 8 && (i*4+4) <= len(px); i++ {
		b := px[i*4 : i*4+4]
		fmt.Printf("  texel %d: % 02x   R=%d G=%d B=%d A=%d\n", i, b, b[0], b[1], b[2], b[3])
	}
	// sample center and a few scattered texels
	fmt.Printf("scattered samples:\n")
	for _, idx := range []int{n / 2, n/2 + res/2, n / 4, 3 * n / 4, n - 1} {
		if idx*4+4 <= len(px) {
			b := px[idx*4 : idx*4+4]
			fmt.Printf("  idx %d: % 02x  R=%d G=%d B=%d A=%d\n", idx, b, b[0], b[1], b[2], b[3])
		}
	}

	// histogram each byte-channel (how many texels have nonzero R/G/B/A)
	var nzR, nzG, nzB, nzA int
	var sumR, sumG, sumB, sumA int
	for i := 0; i+4 <= len(px) && i/4 < n; i += 4 {
		if px[i] != 0 { nzR++ }
		if px[i+1] != 0 { nzG++ }
		if px[i+2] != 0 { nzB++ }
		if px[i+3] != 0 { nzA++ }
		sumR += int(px[i]); sumG += int(px[i+1]); sumB += int(px[i+2]); sumA += int(px[i+3])
	}
	fmt.Printf("channel nonzero counts (of %d): R=%d G=%d B=%d A=%d\n", n, nzR, nzG, nzB, nzA)
	fmt.Printf("channel means: R=%.1f G=%.1f B=%.1f A=%.1f\n",
		float64(sumR)/float64(n), float64(sumG)/float64(n), float64(sumB)/float64(n), float64(sumA)/float64(n))

	// write RGBA PNG
	img := image.NewNRGBA(image.Rect(0, 0, res, res))
	for y := 0; y < res; y++ {
		for x := 0; x < res; x++ {
			i := (y*res + x) * 4
			if i+4 <= len(px) {
				img.SetNRGBA(x, y, color.NRGBA{px[i], px[i+1], px[i+2], 255}) // ignore A for visibility
			}
		}
	}
	out := filepath.Join(outdir, fmt.Sprintf("L1rgba_0x%016x.png", ti.Textures[1].Path.Value))
	f, _ := os.Create(out)
	png.Encode(f, img)
	f.Close()
	fmt.Printf("wrote %s\n", out)

	// also write alpha-only PNG
	imgA := image.NewGray(image.Rect(0, 0, res, res))
	for y := 0; y < res; y++ {
		for x := 0; x < res; x++ {
			i := (y*res+x)*4 + 3
			if i < len(px) {
				imgA.SetGray(x, y, color.Gray{px[i]})
			}
		}
	}
	outA := filepath.Join(outdir, fmt.Sprintf("L1alpha_0x%016x.png", ti.Textures[1].Path.Value))
	fa, _ := os.Create(outA)
	png.Encode(fa, imgA)
	fa.Close()
	fmt.Printf("wrote %s\n", outA)
}
