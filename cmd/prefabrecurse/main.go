// Recursively expand prefabs (including nested prefabs) into a flat unit list
// with world transforms (parent_T x child_T), in RAW stingray coordinates --
// matching the existing _prefab_contents.json format.
//
// Usage: prefabrecurse -g <gamedir> -in <failed_hashes.json> -out <out.json> [-diag]
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/xypwn/filediver/app"
	"github.com/xypwn/filediver/stingray"
	"github.com/xypwn/filediver/stingray/prefab"
)

type ChildUnit struct {
	Path  string     `json:"path"`
	Pos   [3]float32 `json:"pos"`
	Rot   [4]float32 `json:"rot"`
	Scale [3]float32 `json:"scale"`
}

// quat multiply (x,y,z,w)
func qmul(a, b mgl32.Vec4) mgl32.Vec4 {
	ax, ay, az, aw := a[0], a[1], a[2], a[3]
	bx, by, bz, bw := b[0], b[1], b[2], b[3]
	return mgl32.Vec4{
		aw*bx + ax*bw + ay*bz - az*by,
		aw*by - ax*bz + ay*bw + az*bx,
		aw*bz + ax*by - ay*bx + az*bw,
		aw*bw - ax*bx - ay*by - az*bz,
	}
}

// rotate vec by quat
func qrot(q mgl32.Vec4, v mgl32.Vec3) mgl32.Vec3 {
	x, y, z, w := q[0], q[1], q[2], q[3]
	vx, vy, vz := v[0], v[1], v[2]
	tx := 2 * (y*vz - z*vy)
	ty := 2 * (z*vx - x*vz)
	tz := 2 * (x*vy - y*vx)
	return mgl32.Vec3{
		vx + w*tx + (y*tz - z*ty),
		vy + w*ty + (z*tx - x*tz),
		vz + w*tz + (x*ty - y*tx),
	}
}

var dataDir *stingray.DataDir
var diag bool
var depthLimit = 16

// expand returns the flat child-unit list for a prefab hash, with world transforms
// pre-multiplied by (pPos,pRot,pScale). visited guards against cycles.
func expand(h stingray.Hash, pPos mgl32.Vec3, pRot mgl32.Vec4, pScale mgl32.Vec3,
	visited map[uint64]bool, depth int, stats map[string]int) []ChildUnit {
	out := []ChildUnit{}
	if depth > depthLimit {
		stats["depth_exceeded"]++
		return out
	}
	if visited[h.Value] {
		stats["cycle"]++
		return out
	}
	visited[h.Value] = true
	defer delete(visited, h.Value)

	id := stingray.NewFileID(h, stingray.Sum("prefab"))
	b, err := dataDir.Read(id, stingray.DataMain)
	if err != nil {
		stats["read_fail"]++
		if diag {
			fmt.Fprintf(os.Stderr, "  %sread fail %v: %v\n", indent(depth), h.String(), err)
		}
		return out
	}
	pf, err := prefab.Load(bytes.NewReader(b))
	if err != nil {
		stats["parse_fail"]++
		return out
	}

	// direct units
	for _, u := range pf.Units {
		cp := u.PositionVec
		cr := u.RotationVec
		cs := u.ScaleVec
		// world = parent * child
		wp := pPos.Add(qrot(pRot, mgl32.Vec3{cp[0] * pScale[0], cp[1] * pScale[1], cp[2] * pScale[2]}))
		wr := qmul(pRot, cr)
		ws := mgl32.Vec3{pScale[0] * cs[0], pScale[1] * cs[1], pScale[2] * cs[2]}
		out = append(out, ChildUnit{
			Path:  u.Hash.String(),
			Pos:   [3]float32{wp[0], wp[1], wp[2]},
			Rot:   [4]float32{wr[0], wr[1], wr[2], wr[3]},
			Scale: [3]float32{ws[0], ws[1], ws[2]},
		})
		stats["units"]++
	}

	// nested prefabs (recurse)
	for _, np := range pf.NestedPrefabs {
		cp := np.PositionVec
		cr := np.RotationVec
		cs := np.ScaleVec
		wp := pPos.Add(qrot(pRot, mgl32.Vec3{cp[0] * pScale[0], cp[1] * pScale[1], cp[2] * pScale[2]}))
		wr := qmul(pRot, cr)
		ws := mgl32.Vec3{pScale[0] * cs[0], pScale[1] * cs[1], pScale[2] * cs[2]}
		stats["nested"]++
		out = append(out, expand(np.Path, wp, wr, ws, visited, depth+1, stats)...)
	}
	return out
}

func indent(d int) string {
	s := ""
	for i := 0; i < d; i++ {
		s += "  "
	}
	return s
}

func main() {
	gameDir := flag.String("g", "", "game dir")
	inPath := flag.String("in", "", "json array of failed prefab hashes")
	outPath := flag.String("out", "", "output json: {hash: [children]}")
	flag.BoolVar(&diag, "diag", false, "diagnostic stderr")
	flag.Parse()

	ctx := context.Background()
	a, err := app.OpenGameDir(ctx, *gameDir, nil, nil, stingray.ThinHash{}, func(c, t int) {})
	if err != nil {
		fmt.Fprintf(os.Stderr, "OpenGameDir: %v\n", err)
		os.Exit(1)
	}
	dataDir = a.DataDir

	var hashes []string
	hb, _ := os.ReadFile(*inPath)
	json.Unmarshal(hb, &hashes)

	identPos := mgl32.Vec3{0, 0, 0}
	identRot := mgl32.Vec4{0, 0, 0, 1}
	identScale := mgl32.Vec3{1, 1, 1}

	result := map[string][]ChildUnit{}
	for _, hs := range hashes {
		h, err := stingray.ParseHash(hs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bad hash %s\n", hs)
			continue
		}
		stats := map[string]int{}
		children := expand(h, identPos, identRot, identScale, map[uint64]bool{}, 0, stats)
		result["0x"+h.String()] = children
		fmt.Printf("0x%s -> %d units (nested=%d read_fail=%d cycle=%d)\n",
			h.String(), len(children), stats["nested"], stats["read_fail"], stats["cycle"])
	}

	ob, _ := json.MarshalIndent(result, "", " ")
	os.WriteFile(*outPath, ob, 0644)
	fmt.Printf("wrote %s (%d prefabs)\n", *outPath, len(result))
}
