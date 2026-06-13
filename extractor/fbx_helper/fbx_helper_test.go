package fbx_helper

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/qmuntal/gltf"
)

func TestLODBaseAndLevel(t *testing.T) {
	cases := []struct {
		name      string
		wantBase  string
		wantLevel int
	}{
		{"g_body", "g_body", 0},
		{"g_body_LOD1", "g_body", 1},
		{"g_body_LOD4", "g_body", 4},
		{"g_body_lod2", "g_body", 2}, // case-insensitive
		{"g_body_shadow", "g_body_shadow", 0},
		{"g_body_shadow_LOD3", "g_body_shadow", 3},
		{"l_claw_r", "l_claw_r", 0},
		{"0x1234abcd", "0x1234abcd", 0},
		{"weird_LOD", "weird_LOD", 0}, // no digits -> not a LOD suffix
	}
	for _, c := range cases {
		base, level := lodBaseAndLevel(c.name)
		if base != c.wantBase || level != c.wantLevel {
			t.Errorf("lodBaseAndLevel(%q) = (%q, %d), want (%q, %d)", c.name, base, level, c.wantBase, c.wantLevel)
		}
	}
}

// newLODTestExporter builds a minimal exporter whose nodes are all root-level
// mesh nodes with the given names, with the export sets populated as
// prepareExportSets would. This exercises detectLODGroups without needing real
// geometry buffers.
func newLODTestExporter(names []string) (*exporter, []meshObject) {
	doc := &gltf.Document{}
	e := &exporter{
		doc:            doc,
		nodeIDs:        make(map[uint32]int64),
		nodeAttrIDs:    make(map[uint32]int64),
		meshIDs:        make(map[uint32]int64),
		matIDs:         make(map[uint32]int64),
		skinIDs:        make(map[uint32]int64),
		poseIDs:        make(map[uint32]int64),
		exportJoints:   make(map[uint32]bool),
		exportModels:   make(map[uint32]bool),
		lodGroupByNode: make(map[uint32]int),
		nextID:         100000,
	}
	meshObjects := make([]meshObject, 0, len(names))
	for i, name := range names {
		doc.Meshes = append(doc.Meshes, &gltf.Mesh{Name: name})
		mi := uint32(len(doc.Meshes) - 1)
		doc.Nodes = append(doc.Nodes, &gltf.Node{Name: name, Mesh: &mi})
		nodeIdx := uint32(i)
		e.exportModels[nodeIdx] = true
		meshObjects = append(meshObjects, meshObject{
			nodeIndex: nodeIdx,
			meshIndex: mi,
			nodeID:    e.idForNode(nodeIdx),
			meshID:    e.idForMesh(nodeIdx),
			name:      name,
		})
	}
	return e, meshObjects
}

func TestDetectLODGroupsGroupsSameBaseChain(t *testing.T) {
	// Mixed scene: a real LOD chain, a separate shadow chain, and unrelated
	// meshes that must NOT be pulled into any group.
	names := []string{
		"g_body", "g_body_LOD1", "g_body_LOD2", "g_body_LOD3", "g_body_LOD4",
		"g_body_shadow", "g_body_shadow_LOD1", "g_body_shadow_LOD2", "g_body_shadow_LOD3",
		"0x1234abcd", "l_claw_r", "l_claw_l",
	}
	e, meshObjects := newLODTestExporter(names)
	e.detectLODGroups(meshObjects)

	if len(e.lodGroups) != 2 {
		t.Fatalf("expected 2 LOD groups (g_body, g_body_shadow), got %d", len(e.lodGroups))
	}

	byBase := map[string]lodGroup{}
	for _, g := range e.lodGroups {
		byBase[g.base] = g
	}
	body, ok := byBase["g_body"]
	if !ok {
		t.Fatalf("missing g_body group; groups=%v", byBase)
	}
	if len(body.members) != 5 {
		t.Fatalf("g_body group should have 5 members, got %d", len(body.members))
	}
	// Members must be ordered LOD0 -> LOD4.
	wantOrder := []string{"g_body", "g_body_LOD1", "g_body_LOD2", "g_body_LOD3", "g_body_LOD4"}
	for i, m := range body.members {
		if m.name != wantOrder[i] {
			t.Fatalf("g_body member %d = %q, want %q (LOD order broken)", i, m.name, wantOrder[i])
		}
	}
	shadow, ok := byBase["g_body_shadow"]
	if !ok {
		t.Fatalf("missing g_body_shadow group")
	}
	if len(shadow.members) != 4 {
		t.Fatalf("g_body_shadow group should have 4 members, got %d", len(shadow.members))
	}

	// Unrelated meshes must not be in any group.
	for _, name := range []string{"0x1234abcd", "l_claw_r", "l_claw_l"} {
		for _, g := range e.lodGroups {
			for _, m := range g.members {
				if m.name == name {
					t.Fatalf("unrelated mesh %q was pulled into LOD group %q", name, g.base)
				}
			}
		}
	}
	// g_body (LOD0) must NOT be grouped with g_body_shadow.
	for _, m := range body.members {
		if m.name == "g_body_shadow" {
			t.Fatalf("g_body group wrongly absorbed g_body_shadow")
		}
	}
}

func TestDetectLODGroupsIgnoresLoneMesh(t *testing.T) {
	// Single mesh with no LOD siblings -> no group, original behavior preserved.
	e, meshObjects := newLODTestExporter([]string{"g_body"})
	e.detectLODGroups(meshObjects)
	if len(e.lodGroups) != 0 {
		t.Fatalf("lone mesh must not form a LOD group, got %d groups", len(e.lodGroups))
	}
	if len(e.lodGroupByNode) != 0 {
		t.Fatalf("lodGroupByNode should be empty for lone mesh")
	}
}

func TestDetectLODGroupsRequiresLODSuffixSibling(t *testing.T) {
	// Two meshes share a base only by coincidence but neither carries a _LODn
	// suffix -> must not be grouped.
	e, meshObjects := newLODTestExporter([]string{"head", "tail"})
	e.detectLODGroups(meshObjects)
	if len(e.lodGroups) != 0 {
		t.Fatalf("distinct meshes without _LODn must not group, got %d", len(e.lodGroups))
	}
}

func TestDetectLODGroupsDisabledByEnv(t *testing.T) {
	t.Setenv("FILEDIVER_FBX_NO_LODGROUP", "1")
	e, meshObjects := newLODTestExporter([]string{"g_body", "g_body_LOD1", "g_body_LOD2"})
	e.detectLODGroups(meshObjects)
	if len(e.lodGroups) != 0 {
		t.Fatalf("FILEDIVER_FBX_NO_LODGROUP must disable grouping, got %d groups", len(e.lodGroups))
	}
}

func TestFlattenMat4KeepsFBXColumnMajorTranslation(t *testing.T) {
	m := mgl32.Translate3D(1, 2, 3)
	got := flattenMat4(m)

	if got[12] != 1 || got[13] != 2 || got[14] != 3 {
		t.Fatalf("translation was not written in FBX column-major slots: got [%v %v %v]", got[12], got[13], got[14])
	}
	if got[3] != 0 || got[7] != 0 || got[11] != 0 {
		t.Fatalf("translation leaked into row-major slots: got [%v %v %v]", got[3], got[7], got[11])
	}
}

func TestMatrixToEulerXYZDegreesRoundTrip(t *testing.T) {
	cases := []mgl32.Vec3{
		{0, 0, 0},
		{35, -20, 15},
		{-90, 0, 0},
		{12.5, 47.25, -130.75},
		{-42, -35, 88},
	}

	for _, want := range cases {
		m := eulerXYZMatrixDegrees(want)
		got := matrixToEulerXYZDegrees(m)
		roundTrip := eulerXYZMatrixDegrees(got)
		if !mat4ApproxEqual(m, roundTrip, 0.0001) {
			t.Fatalf("XYZ Euler round trip mismatch for %v: got %v", want, got)
		}
	}
}

func TestUEForwardCorrectionMatrixPreservesFilediverRefPose(t *testing.T) {
	m := ueForwardCorrectionMatrix()

	if !mat4ApproxEqual(m, mgl32.Ident4(), 0.0001) {
		t.Fatalf("forward correction must not disturb HD2 mesh/skeleton ref pose: got %v", m)
	}
}

func TestAlignFBXTimeToFrameRoundsHalfFrameUp(t *testing.T) {
	got := alignFBXTimeToFrame(fbxTime(23.3166667), 30)
	want := fbxTimeFromSeconds(23.333333333333332)

	if got != want {
		t.Fatalf("expected half-frame take stop to align upward to 700 frames, got %d want %d", got, want)
	}
}

func TestAlignFBXTimeToFrameKeepsFrameAlignedTime(t *testing.T) {
	got := alignFBXTimeToFrame(fbxTime(1.0), 30)
	want := fbxTimeFromSeconds(1.0)

	if got != want {
		t.Fatalf("expected frame-aligned take stop to remain unchanged, got %d want %d", got, want)
	}
}

func eulerXYZMatrixDegrees(v mgl32.Vec3) mgl32.Mat4 {
	x := mgl32.DegToRad(v[0])
	y := mgl32.DegToRad(v[1])
	z := mgl32.DegToRad(v[2])
	return mgl32.HomogRotate3DZ(z).Mul4(mgl32.HomogRotate3DY(y)).Mul4(mgl32.HomogRotate3DX(x))
}

func vec3ApproxEqual(a, b mgl32.Vec3, eps float64) bool {
	for i := range a {
		if math.Abs(float64(a[i]-b[i])) > eps {
			return false
		}
	}
	return true
}

func mat4ApproxEqual(a, b mgl32.Mat4, eps float64) bool {
	for i := range a {
		if math.Abs(float64(a[i]-b[i])) > eps {
			return false
		}
	}
	return true
}
