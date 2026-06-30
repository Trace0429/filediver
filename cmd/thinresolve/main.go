// thinresolve: compute thinhash (murmur64>>32) for each name in a wordlist, then
// resolve given 0x???????? thinhashes to names. 用法: thinresolve <thinhashes.txt> <0xhash> ...
package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/xypwn/filediver/stingray"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("usage: thinresolve <wordlist.txt> <0xhash> ...")
		os.Exit(1)
	}
	rev := map[uint32]string{}
	f, _ := os.Open(os.Args[1])
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		w := strings.TrimSpace(sc.Text())
		if w == "" || strings.HasPrefix(w, "//") {
			continue
		}
		th := stingray.Sum(w).Thin().Value
		rev[th] = w
	}
	f.Close()
	fmt.Printf("loaded %d words\n", len(rev))
	for _, arg := range os.Args[2:] {
		s := strings.TrimPrefix(strings.TrimSpace(arg), "0x")
		v, err := strconv.ParseUint(s, 16, 32)
		if err != nil {
			fmt.Printf("%s : bad hash\n", arg)
			continue
		}
		if name, ok := rev[uint32(v)]; ok {
			fmt.Printf("0x%08x = %s\n", v, name)
		} else {
			fmt.Printf("0x%08x = (unknown)\n", v)
		}
	}
}
