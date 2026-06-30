// hashprobe: report what filetype(s) a given hash exists as in the HD2 data package.
// 用法: hashprobe <gamedir/data> <0xhash> ...
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/xypwn/filediver/stingray"
)

func pn(s string) stingray.Hash {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "0x") {
		s = s[2:]
	}
	if v, e := strconv.ParseUint(s, 16, 64); e == nil && len(s) == 16 {
		return stingray.Hash{Value: v}
	}
	return stingray.Sum(s)
}

func main() {
	dd, e := stingray.OpenDataDir(context.Background(), os.Args[1], func(c, t int) {})
	if e != nil {
		panic(e)
	}
	types := []string{"unit", "material", "texture", "prefab", "bik_video", "wwise_bank", "particles", "havok_physics_data", "state_machine", "animation", "speedtree", "entity"}
	for _, arg := range os.Args[2:] {
		h := pn(arg)
		found := []string{}
		for _, tn := range types {
			fid := stingray.FileID{Name: h, Type: stingray.Sum(tn)}
			if fi, ok := dd.Files[fid]; ok && len(fi) > 0 {
				var parts []string
				for _, dt := range []stingray.DataType{stingray.DataMain, stingray.DataStream, stingray.DataGPU} {
					if fi[0].Files[dt].Exists() {
						parts = append(parts, fmt.Sprintf("%v=%d", dt, fi[0].Files[dt].Size))
					}
				}
				found = append(found, fmt.Sprintf("%s(%s)", tn, strings.Join(parts, " ")))
			}
		}
		if len(found) == 0 {
			fmt.Printf("%s: NOT FOUND in any type\n", arg)
		} else {
			fmt.Printf("%s: %s\n", arg, strings.Join(found, ", "))
		}
	}
}
