package main
import ("bufio";"bytes";"context";"fmt";"os";"sort";"strconv";"strings"
 "github.com/xypwn/filediver/stingray";"github.com/xypwn/filediver/stingray/level")
func parseHash(s string) uint64 { s=strings.TrimSpace(s); if strings.HasPrefix(s,"0x"){s=s[2:]}; v,_:=strconv.ParseUint(s,16,64); return v }
func main(){
 dd,err:=stingray.OpenDataDir(context.Background(),os.Args[1],func(c,t int){}); if err!=nil{panic(err)}
 terr:=map[uint64]bool{}
 if len(os.Args)>2 { f,_:=os.Open(os.Args[2]); sc:=bufio.NewScanner(f); for sc.Scan(){ l:=strings.TrimSpace(sc.Text()); if l!=""{terr[parseHash(l)]=true} }; f.Close() }
 lt:=stingray.Sum("level")
 type R struct{H uint64; NU,NT,NP int}
 var rows []R
 for fid:=range dd.Files {
  if fid.Type!=lt {continue}
  mb,err:=dd.Read(fid,stingray.DataMain); if err!=nil{continue}
  lv,err:=level.LoadLevel(bytes.NewReader(mb)); if err!=nil{continue}
  nt:=0; for _,u:=range lv.Units{ if terr[u.Path().Value]{nt++} }
  rows=append(rows,R{fid.Name.Value,len(lv.Units),nt,len(lv.Prefabs)})
 }
 sort.Slice(rows,func(i,j int)bool{return rows[i].NU>rows[j].NU})
 n:=0
 fmt.Printf("%-20s %8s %8s %8s\n","level(noTerrain)","units","prefabs","terrain")
 for _,r:=range rows { if r.NT==0 { n++; if n<=40 {fmt.Printf("0x%016x %8d %8d %8d\n",r.H,r.NU,r.NP,r.NT)} } }
 fmt.Printf("\nNON-terrain levels: %d  (total=%d, terrain-cells=%d)\n",n,len(rows),len(rows)-n)
 b:=map[string]int{}
 for _,r:=range rows{ if r.NT>0 {continue}; switch{ case r.NU==0:b["0 units"]++; case r.NU<10:b["1-9"]++; case r.NU<100:b["10-99"]++; case r.NU<1000:b["100-999"]++; default:b["1000+"]++ } }
 fmt.Println("\nnon-terrain unit-count buckets:"); for k,v:=range b{ fmt.Printf("  %-10s %d\n",k,v) }
}
