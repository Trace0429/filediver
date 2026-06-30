package main
import ("fmt";"os";"github.com/xypwn/filediver/stingray")
func main(){ for _,a:=range os.Args[1:]{ h:=stingray.Sum(a); fmt.Printf("0x%016x %s\n",h.Value,a) } }
