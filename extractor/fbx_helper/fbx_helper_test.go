package fbx_helper

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

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
