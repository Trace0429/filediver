// hashfind: search raw bytes of all files of given type(s) for any of the target 8-byte hashes (LE).
// 用法: hashfind <gamedir/data> <targets.txt> <type1,type2,...>
package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/xypwn/filediver/stingray"
)

func ph(s string) uint64 {
	s = strings.TrimPrefix(strings.TrimSpace(s), "0x")
	v, _ := strconv.ParseUint(s, 16, 64)
	return v
}

func main() {
	if len(os.Args) < 4 {
		fmt.Println("usage: hashfind <gamedir/data> <targets.txt> <type1,type2,...>")
		os.Exit(1)
	}
	dd, err := stingray.OpenDataDir(context.Background(), os.Args[1], func(c, t int) {})
	if err != nil {
		panic(err)
	}
	tf, _ := os.Open(os.Args[2])
	sc := bufio.NewScanner(tf)
	targets := map[uint64]bool{}
	for sc.Scan() {
		h := ph(sc.Text())
		if h != 0 {
			targets[h] = true
		}
	}
	tf.Close()
	needle := map[string]uint64{}
	for h := range targets {
		b := make([]byte, 8)
		binary.LittleEndian.PutUint64(b, h)
		needle[string(b)] = h
	}
	types := strings.Split(os.Args[3], ",")
	typeSet := map[stingray.Hash]string{}
	for _, t := range types {
		typeSet[stingray.Sum(t)] = t
	}
	fmt.Printf("targets=%d, scanning types=%v\n", len(targets), types)
	found := map[uint64]map[string]bool{}
	scanned := 0
	for fid := range dd.Files {
		tn, ok := typeSet[fid.Type]
		if !ok {
			continue
		}
		for _, dt := range []stingray.DataType{stingray.DataMain, stingray.DataGPU, stingray.DataStream} {
			b, err := dd.Read(fid, dt)
			if err != nil {
				continue
			}
			scanned++
			for i := 0; i+8 <= len(b); i++ {
				if h, ok := needle[string(b[i:i+8])]; ok {
					if found[h] == nil {
						found[h] = map[string]bool{}
					}
					found[h][fmt.Sprintf("0x%016x.%s", fid.Name.Value, tn)] = true
				}
			}
		}
	}
	fmt.Printf("scanned %d file-streams. targets found: %d/%d\n", scanned, len(found), len(targets))
	cnt := 0
	for h, locs := range found {
		if cnt < 40 {
			var uniq []string
			for l := range locs {
				uniq = append(uniq, l)
			}
			show := uniq
			if len(show) > 4 {
				show = show[:4]
			}
			fmt.Printf("  0x%016x in %d files: %v\n", h, len(uniq), show)
		}
		cnt++
	}
}
