// shaderdump: load a material's GPU segment, report shader programs, dump pixel-shader
// texture/sampler metadata (Name/Register/Bindcount), and attempt DXBC->GLSL.
// 用法: shaderdump <gamedir/data> <outdir> <0xmaterialhash或路径>
package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/xypwn/filediver/stingray"
	"github.com/xypwn/filediver/stingray/unit/material"
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

func dumpShader(outdir, tag string, sh *material.Shader) {
	if sh == nil {
		return
	}
	fmt.Printf("    [%s] name=0x%08x  texMeta=%d sampMeta=%d cbuf=%d\n",
		tag, sh.Name.Value, len(sh.TexMetadata), len(sh.SampMetadata), len(sh.CBufferMetadata))
	for _, tm := range sh.TexMetadata {
		fmt.Printf("        TEX  name=0x%08x  register=%d  bindcount=%d\n", tm.Name.Value, tm.Register, tm.Bindcount)
	}
	for _, sm := range sh.SampMetadata {
		fmt.Printf("        SAMP name=0x%08x  register=%d  bindcount=%d\n", sm.Name.Value, sm.Register, sm.Bindcount)
	}
	for _, cb := range sh.CBufferMetadata {
		fmt.Printf("        CBUF size=%d index=%d\n", cb.Size, cb.Index)
	}
	// attempt GLSL
	func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Printf("        ToGLSL PANIC: %v\n", r)
			}
		}()
		glsl := sh.ToGLSL()
		path := filepath.Join(outdir, fmt.Sprintf("%s_0x%08x.glsl", tag, sh.Name.Value))
		os.WriteFile(path, []byte(glsl), 0644)
		fmt.Printf("        ToGLSL OK -> %s (%d bytes)\n", path, len(glsl))
	}()
}

func main() {
	if len(os.Args) < 4 {
		fmt.Println("usage: shaderdump <gamedir/data> <outdir> <0xmaterialhash或路径>")
		os.Exit(1)
	}
	dd, err := stingray.OpenDataDir(context.Background(), os.Args[1], func(c, t int) {})
	if err != nil {
		panic(err)
	}
	outdir := os.Args[2]
	os.MkdirAll(outdir, 0755)
	h := parseName(os.Args[3])

	// load main to know BaseMaterial
	mainFid := stingray.FileID{Name: h, Type: stingray.Sum("material")}
	mb, err := dd.Read(mainFid, stingray.DataMain)
	if err != nil {
		panic(fmt.Errorf("read main: %w", err))
	}
	mat, err := material.LoadMain(bytes.NewReader(mb))
	if err != nil {
		panic(fmt.Errorf("loadmain: %w", err))
	}
	fmt.Printf("material 0x%016x  BaseMaterial=0x%016x\n", h.Value, mat.BaseMaterial.Value)

	// pick which GPU segment to read (own if BaseMaterial==0, else base)
	gpuHash := h
	if mat.BaseMaterial.Value != 0 {
		gpuHash = mat.BaseMaterial
		fmt.Printf("  (using base material GPU segment)\n")
	}
	gpuFid := stingray.FileID{Name: gpuHash, Type: stingray.Sum("material")}
	files := dd.Files[gpuFid]
	if len(files) == 0 {
		fmt.Printf("GPU material file not found for 0x%016x\n", gpuHash.Value)
		return
	}
	if !files[0].Files[stingray.DataGPU].Exists() {
		fmt.Printf("material 0x%016x has NO DataGPU segment (size 0) -> no shader here\n", gpuHash.Value)
		// still report main/stream sizes
		for _, dt := range []struct{ t stingray.DataType; n string }{{stingray.DataMain,"main"},{stingray.DataStream,"stream"},{stingray.DataGPU,"gpu"}} {
			fmt.Printf("   %s exists=%v size=%d\n", dt.n, files[0].Files[dt.t].Exists(), files[0].Files[dt.t].Size)
		}
		return
	}
	gb, err := dd.Read(gpuFid, stingray.DataGPU)
	if err != nil {
		panic(fmt.Errorf("read gpu: %w", err))
	}
	fmt.Printf("GPU segment %d bytes\n", len(gb))

	matGpu, err := material.LoadGPU(bytes.NewReader(gb))
	if err != nil {
		panic(fmt.Errorf("loadgpu: %w", err))
	}
	if matGpu.ShaderPrograms == nil {
		fmt.Printf("ShaderPrograms == nil\n")
		return
	}
	fmt.Printf("ShaderPrograms: NumPrograms=%d  ProgramBlocks=%d\n",
		matGpu.ShaderPrograms.NumPrograms, len(matGpu.ShaderPrograms.ProgramBlocks))

	for blk, block := range matGpu.ShaderPrograms.ProgramBlocks {
		fmt.Printf("\n== block %d: %d programs, %d headers ==\n", blk, len(block.Programs), len(block.Headers))
		for i := range block.Programs {
			p := &block.Programs[i]
			fmt.Printf("  program %d: textureAttrs=%d samplerAttrs=%d\n", i, len(p.TextureAttrs), len(p.SamplerAttrs))
			// the TextureAttrs hold TextureHash bindings used by this program
			for ti, ta := range p.TextureAttrs {
				fmt.Printf("      texAttr[%d] hash=0x%08x samplerParamCount=%d\n", ti, ta.TextureHash.Value, ta.SamplerParamCount)
			}
			dumpShader(outdir, fmt.Sprintf("blk%d_p%d_pixel", blk, i), p.PixelShader)
			dumpShader(outdir, fmt.Sprintf("blk%d_p%d_vertex", blk, i), p.VertexShader)
			dumpShader(outdir, fmt.Sprintf("blk%d_p%d_domain", blk, i), p.DomainShader)
		}
	}
}
