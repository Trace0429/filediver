package fbx_helper

import (
	"encoding/binary"
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/qmuntal/gltf"
)

type exporter struct {
	doc               *gltf.Document
	w                 io.Writer
	nextID            int64
	nodeIDs           map[uint32]int64
	nodeAttrIDs       map[uint32]int64
	meshIDs           map[uint32]int64
	matIDs            map[uint32]int64
	skinIDs           map[uint32]int64
	poseIDs           map[uint32]int64
	exportJoints      map[uint32]bool
	exportModels      map[uint32]bool
	meshClusterCounts map[uint32]int
	clusters          []cluster
	conns             []connection
	takes             []fbxTake
}

type connection struct {
	child  int64
	parent int64
	prop   string
}

type cluster struct {
	id        int64
	skinID    int64
	jointNode uint32
	meshNode  uint32
	indices   []int
	weights   []float64
}

type fbxTake struct {
	name  string
	start int64
	stop  int64
}

const fbxTimeUnitsPerSecond = 46186158000.0

type meshObject struct {
	nodeIndex uint32
	meshIndex uint32
	nodeID    int64
	meshID    int64
	name      string
}

type primitiveData struct {
	positions []mgl32.Vec3
	normals   []mgl32.Vec3
	uvs       []mgl32.Vec2
	joints    [][4]uint32
	weights   [][4]float32
	indices   []int
	materials []uint32
	material  *uint32
}

// Export writes a Filediver glTF document as an FBX 7.4 ASCII file.
//
// The exporter is intentionally focused on Filediver's model documents:
// meshes, materials, skeleton nodes, skin deformers and bind poses.
func Export(doc *gltf.Document, outPath string) error {
	if err := os.MkdirAll(filepath.Dir(outPath), os.ModePerm); err != nil {
		return err
	}
	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer out.Close()

	e := &exporter{
		doc:               doc,
		w:                 out,
		nextID:            100000,
		nodeIDs:           make(map[uint32]int64),
		nodeAttrIDs:       make(map[uint32]int64),
		meshIDs:           make(map[uint32]int64),
		matIDs:            make(map[uint32]int64),
		skinIDs:           make(map[uint32]int64),
		poseIDs:           make(map[uint32]int64),
		exportJoints:      make(map[uint32]bool),
		exportModels:      make(map[uint32]bool),
		meshClusterCounts: make(map[uint32]int),
	}
	return e.export()
}

// ExportSplitAnimations writes one FBX file per glTF animation.
//
// The outPath argument is the normal unit FBX path. Split files are written to
// a sibling named_fbx directory so the default export layout remains stable:
// <unit folder>/named_fbx/0001_<take>.fbx.
func ExportSplitAnimations(doc *gltf.Document, outPath string) error {
	outDir := filepath.Join(filepath.Dir(outPath), "named_fbx")
	if err := os.MkdirAll(outDir, os.ModePerm); err != nil {
		return err
	}

	manifestPath := filepath.Join(outDir, "manifest.csv")
	manifest, err := os.Create(manifestPath)
	if err != nil {
		return err
	}
	defer manifest.Close()

	writer := csv.NewWriter(manifest)
	defer writer.Flush()
	if err := writer.Write([]string{"index", "fbx_file", "take_name", "source_hash", "bytes"}); err != nil {
		return err
	}

	allAnimations := doc.Animations
	defer func() {
		doc.Animations = allAnimations
	}()

	for animIdx, anim := range allAnimations {
		if anim == nil || len(anim.Channels) == 0 {
			continue
		}
		name := cleanName(anim.Name)
		if name == "" {
			name = fmt.Sprintf("Animation_%d", animIdx)
		}
		sourceHash := animationSourceHash(anim)
		stem := safeFileStem(name)
		if sourceHash != "" && !strings.EqualFold(stem, safeFileStem(sourceHash)) {
			stem = safeFileStem(stem + "_" + sourceHash)
		}
		fileName := fmt.Sprintf("%04d_%s.fbx", animIdx+1, stem)
		filePath := filepath.Join(outDir, fileName)

		doc.Animations = []*gltf.Animation{anim}
		if err := Export(doc, filePath); err != nil {
			return fmt.Errorf("export split animation %s: %w", name, err)
		}

		var size string
		if info, err := os.Stat(filePath); err == nil {
			size = fmt.Sprintf("%d", info.Size())
		}
		if err := writer.Write([]string{fmt.Sprintf("%d", animIdx+1), fileName, name, sourceHash, size}); err != nil {
			return err
		}
	}

	return writer.Error()
}

func (e *exporter) export() error {
	meshObjects := e.collectMeshObjects()
	if len(meshObjects) == 0 {
		return fmt.Errorf("fbx export: document has no mesh nodes")
	}
	if err := e.prepareExportSets(meshObjects); err != nil {
		return err
	}

	e.writeHeader()
	e.writeDefinitions(meshObjects)
	if err := e.writeObjects(meshObjects); err != nil {
		return err
	}
	e.writeConnections()
	e.writeTakes()
	return nil
}

func (e *exporter) newID() int64 {
	e.nextID++
	return e.nextID
}

func (e *exporter) idForNode(idx uint32) int64 {
	if id, ok := e.nodeIDs[idx]; ok {
		return id
	}
	id := e.newID()
	e.nodeIDs[idx] = id
	return id
}

func (e *exporter) idForNodeAttribute(idx uint32) int64 {
	if id, ok := e.nodeAttrIDs[idx]; ok {
		return id
	}
	id := e.newID()
	e.nodeAttrIDs[idx] = id
	return id
}

func (e *exporter) idForMesh(idx uint32) int64 {
	if id, ok := e.meshIDs[idx]; ok {
		return id
	}
	id := e.newID()
	e.meshIDs[idx] = id
	return id
}

func (e *exporter) idForMaterial(idx uint32) int64 {
	if id, ok := e.matIDs[idx]; ok {
		return id
	}
	id := e.newID()
	e.matIDs[idx] = id
	return id
}

func (e *exporter) writeHeader() {
	fmt.Fprintln(e.w, "; FBX 7.4.0 project file")
	fmt.Fprintln(e.w, "; Generated by Filediver FBX exporter")
	fmt.Fprintln(e.w, "FBXHeaderExtension:  {")
	fmt.Fprintln(e.w, "\tFBXHeaderVersion: 1003")
	fmt.Fprintln(e.w, "\tFBXVersion: 7400")
	fmt.Fprintln(e.w, "\tCreator: \"Filediver\"")
	fmt.Fprintln(e.w, "}")
	fmt.Fprintln(e.w, "GlobalSettings:  {")
	fmt.Fprintln(e.w, "\tVersion: 1000")
	fmt.Fprintln(e.w, "\tProperties70:  {")
	fmt.Fprintln(e.w, "\t\tP: \"UpAxis\", \"int\", \"Integer\", \"\",1")
	fmt.Fprintln(e.w, "\t\tP: \"UpAxisSign\", \"int\", \"Integer\", \"\",1")
	fmt.Fprintln(e.w, "\t\tP: \"FrontAxis\", \"int\", \"Integer\", \"\",2")
	fmt.Fprintln(e.w, "\t\tP: \"FrontAxisSign\", \"int\", \"Integer\", \"\",1")
	fmt.Fprintln(e.w, "\t\tP: \"CoordAxis\", \"int\", \"Integer\", \"\",0")
	fmt.Fprintln(e.w, "\t\tP: \"CoordAxisSign\", \"int\", \"Integer\", \"\",1")
	fmt.Fprintln(e.w, "\t\tP: \"UnitScaleFactor\", \"double\", \"Number\", \"\",1")
	fmt.Fprintln(e.w, "\t}")
	fmt.Fprintln(e.w, "}")
}

func (e *exporter) writeDefinitions(meshObjects []meshObject) {
	modelCount := len(e.exportModels)
	geometryCount := len(meshObjects)
	materialCount := len(e.doc.Materials)
	animStacks, animLayers, animCurveNodes, animCurves := e.animationDefinitionCounts()
	nodeAttributeCount := 0
	for i := range e.doc.Nodes {
		if e.shouldExportJoint(uint32(i)) {
			nodeAttributeCount++
		}
	}
	deformerCount := 0
	poseCount := 0
	for _, obj := range meshObjects {
		node := e.doc.Nodes[obj.nodeIndex]
		if node.Skin == nil || int(*node.Skin) >= len(e.doc.Skins) {
			continue
		}
		deformerCount += 1 + e.meshClusterCounts[obj.nodeIndex]
		poseCount++
	}

	fmt.Fprintln(e.w, "Definitions:  {")
	fmt.Fprintf(e.w, "\tCount: %d\n", modelCount+nodeAttributeCount+geometryCount+materialCount+deformerCount+poseCount+animStacks+animLayers+animCurveNodes+animCurves+1)
	fmt.Fprintln(e.w, "\tObjectType: \"GlobalSettings\" {")
	fmt.Fprintln(e.w, "\t\tCount: 1")
	fmt.Fprintln(e.w, "\t}")
	fmt.Fprintln(e.w, "\tObjectType: \"Model\" {")
	fmt.Fprintf(e.w, "\t\tCount: %d\n", modelCount)
	fmt.Fprintln(e.w, "\t}")
	if nodeAttributeCount > 0 {
		fmt.Fprintln(e.w, "\tObjectType: \"NodeAttribute\" {")
		fmt.Fprintf(e.w, "\t\tCount: %d\n", nodeAttributeCount)
		fmt.Fprintln(e.w, "\t}")
	}
	fmt.Fprintln(e.w, "\tObjectType: \"Geometry\" {")
	fmt.Fprintf(e.w, "\t\tCount: %d\n", geometryCount)
	fmt.Fprintln(e.w, "\t}")
	fmt.Fprintln(e.w, "\tObjectType: \"Material\" {")
	fmt.Fprintf(e.w, "\t\tCount: %d\n", materialCount)
	fmt.Fprintln(e.w, "\t}")
	fmt.Fprintln(e.w, "\tObjectType: \"Deformer\" {")
	fmt.Fprintf(e.w, "\t\tCount: %d\n", deformerCount)
	fmt.Fprintln(e.w, "\t}")
	if poseCount > 0 {
		fmt.Fprintln(e.w, "\tObjectType: \"Pose\" {")
		fmt.Fprintf(e.w, "\t\tCount: %d\n", poseCount)
		fmt.Fprintln(e.w, "\t}")
	}
	if animStacks > 0 {
		fmt.Fprintln(e.w, "\tObjectType: \"AnimationStack\" {")
		fmt.Fprintf(e.w, "\t\tCount: %d\n", animStacks)
		fmt.Fprintln(e.w, "\t}")
		fmt.Fprintln(e.w, "\tObjectType: \"AnimationLayer\" {")
		fmt.Fprintf(e.w, "\t\tCount: %d\n", animLayers)
		fmt.Fprintln(e.w, "\t}")
		fmt.Fprintln(e.w, "\tObjectType: \"AnimationCurveNode\" {")
		fmt.Fprintf(e.w, "\t\tCount: %d\n", animCurveNodes)
		fmt.Fprintln(e.w, "\t}")
		fmt.Fprintln(e.w, "\tObjectType: \"AnimationCurve\" {")
		fmt.Fprintf(e.w, "\t\tCount: %d\n", animCurves)
		fmt.Fprintln(e.w, "\t}")
	}
	fmt.Fprintln(e.w, "}")
}

func (e *exporter) writeObjects(meshObjects []meshObject) error {
	fmt.Fprintln(e.w, "Objects:  {")
	for i := range e.doc.Materials {
		e.writeMaterial(uint32(i))
	}
	for i := range e.doc.Nodes {
		if e.shouldExportJoint(uint32(i)) {
			e.writeNodeAttribute(uint32(i))
		}
	}
	for i := range e.doc.Nodes {
		if !e.shouldExportModel(uint32(i)) {
			continue
		}
		e.writeModel(uint32(i))
	}
	for _, obj := range meshObjects {
		if err := e.writeMeshGeometry(obj); err != nil {
			return err
		}
	}
	if err := e.writeSkins(meshObjects); err != nil {
		return err
	}
	if err := e.writeAnimations(); err != nil {
		return err
	}
	fmt.Fprintln(e.w, "}")
	return nil
}

func (e *exporter) collectMeshObjects() []meshObject {
	meshObjects := make([]meshObject, 0)
	for nodeIndex, node := range e.doc.Nodes {
		if node.Mesh == nil {
			continue
		}
		meshIndex := *node.Mesh
		if int(meshIndex) >= len(e.doc.Meshes) {
			continue
		}
		name := cleanName(node.Name)
		if name == "" {
			name = fmt.Sprintf("Mesh_%d", meshIndex)
		}
		meshObjects = append(meshObjects, meshObject{
			nodeIndex: uint32(nodeIndex),
			meshIndex: meshIndex,
			nodeID:    e.idForNode(uint32(nodeIndex)),
			meshID:    e.idForMesh(uint32(nodeIndex)),
			name:      name,
		})
	}
	return meshObjects
}

func (e *exporter) prepareExportSets(meshObjects []meshObject) error {
	e.exportJoints = make(map[uint32]bool)
	e.exportModels = make(map[uint32]bool)
	e.meshClusterCounts = make(map[uint32]int)

	for idx := range e.doc.Nodes {
		nodeIdx := uint32(idx)
		if e.shouldSkipExportNode(nodeIdx) {
			continue
		}
		e.exportModels[nodeIdx] = true
	}

	for _, skin := range e.doc.Skins {
		for _, jointNode := range skin.Joints {
			if int(jointNode) >= len(e.doc.Nodes) || e.shouldSkipExportNode(jointNode) {
				continue
			}
			e.exportJoints[jointNode] = true
			e.exportModels[jointNode] = true
		}
	}

	for _, obj := range meshObjects {
		e.exportModels[obj.nodeIndex] = true

		node := e.doc.Nodes[obj.nodeIndex]
		if node.Skin == nil || int(*node.Skin) >= len(e.doc.Skins) {
			continue
		}
		skin := e.doc.Skins[*node.Skin]
		merged, _, err := e.mergeMesh(e.doc.Meshes[obj.meshIndex])
		if err != nil {
			return err
		}
		clusterWeights := collectClusterWeights(merged.joints, merged.weights, len(skin.Joints))
		exportedForMesh := 0
		for jointSlot, jointNode := range skin.Joints {
			if int(jointNode) >= len(e.doc.Nodes) || e.shouldSkipExportNode(jointNode) {
				continue
			}
			if len(clusterWeights[jointSlot].indices) == 0 {
				continue
			}
			exportedForMesh++
		}
		e.meshClusterCounts[obj.nodeIndex] = exportedForMesh
	}

	if len(e.exportModels) == 0 {
		return fmt.Errorf("fbx export: no exportable mesh or weighted joints")
	}
	return nil
}

func (e *exporter) shouldSkipExportNode(idx uint32) bool {
	if int(idx) >= len(e.doc.Nodes) {
		return true
	}
	switch e.doc.Nodes[idx].Name {
	case "StingrayEntityRoot", "FbxAxisSystem_ConvertNode":
		return true
	default:
		return false
	}
}

func (e *exporter) shouldExportModel(idx uint32) bool {
	return e.exportModels[idx]
}

func (e *exporter) shouldExportJoint(idx uint32) bool {
	return e.exportJoints[idx]
}

func (e *exporter) writeMaterial(idx uint32) {
	mat := e.doc.Materials[idx]
	name := cleanName(mat.Name)
	if name == "" {
		name = fmt.Sprintf("Material_%d", idx)
	}
	id := e.idForMaterial(idx)
	fmt.Fprintf(e.w, "\tMaterial: %d, \"Material::%s\", \"\" {\n", id, escape(name))
	fmt.Fprintln(e.w, "\t\tVersion: 102")
	fmt.Fprintln(e.w, "\t\tShadingModel: \"phong\"")
	fmt.Fprintln(e.w, "\t\tProperties70:  {")
	fmt.Fprintln(e.w, "\t\t\tP: \"DiffuseColor\", \"Color\", \"\", \"A\",0.8,0.8,0.8")
	fmt.Fprintln(e.w, "\t\t\tP: \"SpecularColor\", \"Color\", \"\", \"A\",0.2,0.2,0.2")
	fmt.Fprintln(e.w, "\t\t}")
	fmt.Fprintln(e.w, "\t}")
}

func (e *exporter) writeNodeAttribute(idx uint32) {
	node := e.doc.Nodes[idx]
	name := cleanName(node.Name)
	if name == "" {
		name = fmt.Sprintf("Joint_%d", idx)
	}
	id := e.idForNodeAttribute(idx)
	fmt.Fprintf(e.w, "\tNodeAttribute: %d, \"NodeAttribute::%s\", \"LimbNode\" {\n", id, escape(name))
	fmt.Fprintln(e.w, "\t\tTypeFlags: \"Skeleton\"")
	fmt.Fprintln(e.w, "\t\tProperties70:  {")
	fmt.Fprintln(e.w, "\t\t\tP: \"Size\", \"double\", \"Number\", \"\",1")
	fmt.Fprintln(e.w, "\t\t}")
	fmt.Fprintln(e.w, "\t}")
}

func (e *exporter) writeModel(idx uint32) {
	node := e.doc.Nodes[idx]
	id := e.idForNode(idx)
	name := cleanName(node.Name)
	if name == "" {
		name = fmt.Sprintf("Node_%d", idx)
	}
	modelType := "Null"
	if node.Mesh != nil {
		modelType = "Mesh"
	} else if e.shouldExportJoint(idx) {
		modelType = "LimbNode"
	}
	t, r, s := e.nodeTRSForExport(idx)
	fmt.Fprintf(e.w, "\tModel: %d, \"Model::%s\", \"%s\" {\n", id, escape(name), modelType)
	fmt.Fprintln(e.w, "\t\tVersion: 232")
	fmt.Fprintln(e.w, "\t\tProperties70:  {")
	fmt.Fprintln(e.w, "\t\t\tP: \"RotationActive\", \"bool\", \"\", \"\",1")
	fmt.Fprintln(e.w, "\t\t\tP: \"RotationOrder\", \"enum\", \"\", \"\",0")
	fmt.Fprintln(e.w, "\t\t\tP: \"InheritType\", \"enum\", \"\", \"\",1")
	fmt.Fprintln(e.w, "\t\t\tP: \"DefaultAttributeIndex\", \"int\", \"Integer\", \"\",0")
	fmt.Fprintf(e.w, "\t\t\tP: \"Lcl Translation\", \"Lcl Translation\", \"\", \"A\",%s,%s,%s\n", f64(float64(t[0])), f64(float64(t[1])), f64(float64(t[2])))
	fmt.Fprintf(e.w, "\t\t\tP: \"Lcl Rotation\", \"Lcl Rotation\", \"\", \"A\",%s,%s,%s\n", f64(float64(r[0])), f64(float64(r[1])), f64(float64(r[2])))
	fmt.Fprintf(e.w, "\t\t\tP: \"Lcl Scaling\", \"Lcl Scaling\", \"\", \"A\",%s,%s,%s\n", f64(float64(s[0])), f64(float64(s[1])), f64(float64(s[2])))
	if modelType == "LimbNode" {
		fmt.Fprintln(e.w, "\t\t\tP: \"Size\", \"double\", \"Number\", \"\",1")
	}
	fmt.Fprintln(e.w, "\t\t}")
	fmt.Fprintln(e.w, "\t\tShading: T")
	fmt.Fprintln(e.w, "\t}")
}

func (e *exporter) writeMeshGeometry(obj meshObject) error {
	mesh := e.doc.Meshes[obj.meshIndex]
	merged, materialSlots, err := e.mergeMesh(mesh)
	if err != nil {
		return fmt.Errorf("fbx geometry %s: %w", obj.name, err)
	}
	fmt.Fprintf(e.w, "\tGeometry: %d, \"Geometry::%s\", \"Mesh\" {\n", obj.meshID, escape(obj.name))
	fmt.Fprintln(e.w, "\t\tGeometryVersion: 124")
	writeFloatArray(e.w, "\t\tVertices", flattenVec3(merged.positions))
	writeIntArray(e.w, "\t\tPolygonVertexIndex", merged.indices)
	if len(merged.normals) == len(merged.positions) {
		fmt.Fprintln(e.w, "\t\tLayerElementNormal: 0 {")
		fmt.Fprintln(e.w, "\t\t\tVersion: 101")
		fmt.Fprintln(e.w, "\t\t\tName: \"\"")
		fmt.Fprintln(e.w, "\t\t\tMappingInformationType: \"ByVertice\"")
		fmt.Fprintln(e.w, "\t\t\tReferenceInformationType: \"Direct\"")
		writeFloatArray(e.w, "\t\t\tNormals", flattenVec3(merged.normals))
		fmt.Fprintln(e.w, "\t\t}")
	}
	if len(merged.uvs) == len(merged.positions) {
		fmt.Fprintln(e.w, "\t\tLayerElementUV: 0 {")
		fmt.Fprintln(e.w, "\t\t\tVersion: 101")
		fmt.Fprintln(e.w, "\t\t\tName: \"UVSet\"")
		fmt.Fprintln(e.w, "\t\t\tMappingInformationType: \"ByVertice\"")
		fmt.Fprintln(e.w, "\t\t\tReferenceInformationType: \"Direct\"")
		writeFloatArray(e.w, "\t\t\tUV", flattenVec2(merged.uvs))
		fmt.Fprintln(e.w, "\t\t}")
	}
	if len(materialSlots) > 0 {
		fmt.Fprintln(e.w, "\t\tLayerElementMaterial: 0 {")
		fmt.Fprintln(e.w, "\t\t\tVersion: 101")
		fmt.Fprintln(e.w, "\t\t\tName: \"\"")
		fmt.Fprintln(e.w, "\t\t\tMappingInformationType: \"ByPolygon\"")
		fmt.Fprintln(e.w, "\t\t\tReferenceInformationType: \"IndexToDirect\"")
		writeIntArray(e.w, "\t\t\tMaterials", materialSlots)
		fmt.Fprintln(e.w, "\t\t}")
	}
	polygonCount := len(merged.indices) / 3
	if polygonCount > 0 {
		smoothing := make([]int, polygonCount)
		for i := range smoothing {
			smoothing[i] = 1
		}
		fmt.Fprintln(e.w, "\t\tLayerElementSmoothing: 0 {")
		fmt.Fprintln(e.w, "\t\t\tVersion: 102")
		fmt.Fprintln(e.w, "\t\t\tName: \"\"")
		fmt.Fprintln(e.w, "\t\t\tMappingInformationType: \"ByPolygon\"")
		fmt.Fprintln(e.w, "\t\t\tReferenceInformationType: \"Direct\"")
		writeIntArray(e.w, "\t\t\tSmoothing", smoothing)
		fmt.Fprintln(e.w, "\t\t}")
	}
	fmt.Fprintln(e.w, "\t\tLayer: 0 {")
	fmt.Fprintln(e.w, "\t\t\tVersion: 100")
	if len(merged.normals) == len(merged.positions) {
		fmt.Fprintln(e.w, "\t\t\tLayerElement:  {")
		fmt.Fprintln(e.w, "\t\t\t\tType: \"LayerElementNormal\"")
		fmt.Fprintln(e.w, "\t\t\t\tTypedIndex: 0")
		fmt.Fprintln(e.w, "\t\t\t}")
	}
	if len(merged.uvs) == len(merged.positions) {
		fmt.Fprintln(e.w, "\t\t\tLayerElement:  {")
		fmt.Fprintln(e.w, "\t\t\t\tType: \"LayerElementUV\"")
		fmt.Fprintln(e.w, "\t\t\t\tTypedIndex: 0")
		fmt.Fprintln(e.w, "\t\t\t}")
	}
	if len(materialSlots) > 0 {
		fmt.Fprintln(e.w, "\t\t\tLayerElement:  {")
		fmt.Fprintln(e.w, "\t\t\t\tType: \"LayerElementMaterial\"")
		fmt.Fprintln(e.w, "\t\t\t\tTypedIndex: 0")
		fmt.Fprintln(e.w, "\t\t\t}")
	}
	if polygonCount > 0 {
		fmt.Fprintln(e.w, "\t\t\tLayerElement:  {")
		fmt.Fprintln(e.w, "\t\t\t\tType: \"LayerElementSmoothing\"")
		fmt.Fprintln(e.w, "\t\t\t\tTypedIndex: 0")
		fmt.Fprintln(e.w, "\t\t\t}")
	}
	fmt.Fprintln(e.w, "\t\t}")
	fmt.Fprintln(e.w, "\t}")

	e.conns = append(e.conns, connection{child: obj.meshID, parent: obj.nodeID})
	for _, matIndex := range merged.materials {
		e.conns = append(e.conns, connection{child: e.idForMaterial(matIndex), parent: obj.nodeID})
	}
	return nil
}

func (e *exporter) writeSkins(meshObjects []meshObject) error {
	for _, obj := range meshObjects {
		node := e.doc.Nodes[obj.nodeIndex]
		if node.Skin == nil || int(*node.Skin) >= len(e.doc.Skins) {
			continue
		}
		if e.meshClusterCounts[obj.nodeIndex] == 0 {
			continue
		}
		skin := e.doc.Skins[*node.Skin]
		skinID := e.newID()
		e.skinIDs[*node.Skin] = skinID
		fmt.Fprintf(e.w, "\tDeformer: %d, \"Deformer::%s_Skin\", \"Skin\" {\n", skinID, escape(obj.name))
		fmt.Fprintln(e.w, "\t\tVersion: 101")
		fmt.Fprintln(e.w, "\t\tLink_DeformAcuracy: 50")
		fmt.Fprintln(e.w, "\t}")
		e.conns = append(e.conns, connection{child: skinID, parent: obj.meshID})

		merged, _, err := e.mergeMesh(e.doc.Meshes[obj.meshIndex])
		if err != nil {
			return err
		}
		globalMesh := e.exportGlobalMatrix(obj.nodeIndex)
		jointBindMatrices := make([]mgl32.Mat4, len(skin.Joints))
		for jointSlot, jointNode := range skin.Joints {
			jointBindMatrices[jointSlot] = e.exportGlobalMatrix(jointNode)
		}
		e.writeBindPose(obj, skin, globalMesh, jointBindMatrices)

		clusterWeights := collectClusterWeights(merged.joints, merged.weights, len(skin.Joints))
		for jointSlot, jointNode := range skin.Joints {
			if int(jointNode) >= len(e.doc.Nodes) {
				continue
			}
			cw := clusterWeights[jointSlot]
			if len(cw.indices) == 0 || !e.shouldExportJoint(jointNode) {
				continue
			}
			clusterID := e.newID()
			clusterName := e.doc.Nodes[jointNode].Name
			if clusterName == "" {
				clusterName = fmt.Sprintf("Joint_%d", jointSlot)
			}
			fmt.Fprintf(e.w, "\tDeformer: %d, \"SubDeformer::%s\", \"Cluster\" {\n", clusterID, escape(clusterName))
			fmt.Fprintln(e.w, "\t\tVersion: 100")
			fmt.Fprintln(e.w, "\t\tUserData: \"\", \"\"")
			writeIntArray(e.w, "\t\tIndexes", cw.indices)
			writeFloatArray(e.w, "\t\tWeights", cw.weights)
			link := jointBindMatrices[jointSlot]
			writeFloatArray(e.w, "\t\tTransform", flattenMat4(link.Inv().Mul4(globalMesh)))
			writeFloatArray(e.w, "\t\tTransformLink", flattenMat4(link))
			writeFloatArray(e.w, "\t\tTransformAssociateModel", flattenMat4(e.transformAssociateMatrix(skin, globalMesh)))
			fmt.Fprintln(e.w, "\t}")
			e.clusters = append(e.clusters, cluster{
				id:        clusterID,
				skinID:    skinID,
				jointNode: jointNode,
				meshNode:  obj.nodeIndex,
				indices:   cw.indices,
				weights:   cw.weights,
			})
			e.conns = append(e.conns, connection{child: clusterID, parent: skinID})
			e.conns = append(e.conns, connection{child: e.idForNode(jointNode), parent: clusterID})
		}
	}
	return nil
}

func (e *exporter) writeBindPose(obj meshObject, skin *gltf.Skin, globalMesh mgl32.Mat4, jointBindMatrices []mgl32.Mat4) {
	poseID := e.newID()
	e.poseIDs[obj.nodeIndex] = poseID
	skeletonNode, hasSkeletonNode := e.bindPoseSkeletonNode(skin)

	type poseNode struct {
		id     int64
		matrix mgl32.Mat4
	}
	poseNodes := []poseNode{{id: obj.nodeID, matrix: globalMesh}}
	seen := map[int64]bool{obj.nodeID: true}
	addPoseNode := func(node uint32, matrix mgl32.Mat4) {
		id := e.idForNode(node)
		if seen[id] {
			return
		}
		seen[id] = true
		poseNodes = append(poseNodes, poseNode{id: id, matrix: matrix})
	}
	if hasSkeletonNode {
		addPoseNode(skeletonNode, e.exportGlobalMatrix(skeletonNode))
	}
	parentMap := e.parentMap()
	for jointSlot, jointNode := range skin.Joints {
		if int(jointNode) >= len(e.doc.Nodes) || !e.shouldExportJoint(jointNode) {
			continue
		}
		ancestorChain := make([]uint32, 0)
		for parent, ok := parentMap[jointNode]; ok; parent, ok = parentMap[parent] {
			if e.shouldExportJoint(parent) {
				ancestorChain = append(ancestorChain, parent)
			}
		}
		for i := len(ancestorChain) - 1; i >= 0; i-- {
			addPoseNode(ancestorChain[i], e.exportGlobalMatrix(ancestorChain[i]))
		}
		addPoseNode(jointNode, jointBindMatrices[jointSlot])
	}

	fmt.Fprintf(e.w, "\tPose: %d, \"Pose::%s_BindPose\", \"BindPose\" {\n", poseID, escape(obj.name))
	fmt.Fprintln(e.w, "\t\tType: \"BindPose\"")
	fmt.Fprintln(e.w, "\t\tVersion: 100")
	fmt.Fprintf(e.w, "\t\tNbPoseNodes: %d\n", len(poseNodes))
	for _, node := range poseNodes {
		e.writePoseNode(node.id, node.matrix)
	}
	fmt.Fprintln(e.w, "\t}")
}

func (e *exporter) bindPoseSkeletonNode(skin *gltf.Skin) (uint32, bool) {
	if skin.Skeleton == nil || int(*skin.Skeleton) >= len(e.doc.Nodes) {
		return 0, false
	}
	if !e.shouldExportModel(*skin.Skeleton) || e.shouldExportJoint(*skin.Skeleton) {
		return 0, false
	}
	for _, jointNode := range skin.Joints {
		if jointNode == *skin.Skeleton {
			return 0, false
		}
	}
	return *skin.Skeleton, true
}

func (e *exporter) writePoseNode(nodeID int64, matrix mgl32.Mat4) {
	fmt.Fprintln(e.w, "\t\tPoseNode:  {")
	fmt.Fprintf(e.w, "\t\t\tNode: %d\n", nodeID)
	writeFloatArray(e.w, "\t\t\tMatrix", flattenMat4(matrix))
	fmt.Fprintln(e.w, "\t\t}")
}

func (e *exporter) writeConnections() {
	rootID := int64(0)
	for i := range e.doc.Nodes {
		idx := uint32(i)
		if !e.shouldExportModel(idx) {
			continue
		}
		if parent, ok := e.exportParent(idx); ok {
			e.conns = append(e.conns, connection{child: e.idForNode(idx), parent: e.idForNode(parent)})
		} else {
			e.conns = append(e.conns, connection{child: e.idForNode(idx), parent: rootID})
		}
		if e.shouldExportJoint(idx) {
			e.conns = append(e.conns, connection{child: e.idForNodeAttribute(idx), parent: e.idForNode(idx)})
		}
	}

	sort.SliceStable(e.conns, func(i, j int) bool {
		if e.conns[i].parent == e.conns[j].parent {
			return e.conns[i].child < e.conns[j].child
		}
		return e.conns[i].parent < e.conns[j].parent
	})

	fmt.Fprintln(e.w, "Connections:  {")
	seen := make(map[connection]bool)
	for _, c := range e.conns {
		if seen[c] {
			continue
		}
		seen[c] = true
		if c.prop != "" {
			fmt.Fprintf(e.w, "\tC: \"OP\",%d,%d, \"%s\"\n", c.child, c.parent, escape(c.prop))
		} else {
			fmt.Fprintf(e.w, "\tC: \"OO\",%d,%d\n", c.child, c.parent)
		}
	}
	fmt.Fprintln(e.w, "}")
}

func (e *exporter) exportParent(idx uint32) (uint32, bool) {
	parents := e.parentMap()
	for parent, ok := parents[idx]; ok; parent, ok = parents[parent] {
		if e.shouldExportModel(parent) {
			return parent, true
		}
	}
	return 0, false
}

func (e *exporter) nodeTRSForExport(idx uint32) (mgl32.Vec3, mgl32.Vec3, mgl32.Vec3) {
	local := e.exportGlobalMatrix(idx)
	if parent, ok := e.exportParent(idx); ok {
		local = e.exportGlobalMatrix(parent).Inv().Mul4(local)
	}
	return matrixToTRS(local)
}

func (e *exporter) transformAssociateMatrix(skin *gltf.Skin, globalMesh mgl32.Mat4) mgl32.Mat4 {
	if skin != nil && skin.Skeleton != nil {
		skeleton := *skin.Skeleton
		if int(skeleton) < len(e.doc.Nodes) && e.shouldExportModel(skeleton) && !e.shouldExportJoint(skeleton) {
			return e.exportGlobalMatrix(skeleton)
		}
	}
	return globalMesh
}

func (e *exporter) writeTakes() {
	fmt.Fprintln(e.w, "Takes:  {")
	current := ""
	if len(e.takes) > 0 {
		current = e.takes[0].name
	}
	fmt.Fprintf(e.w, "\tCurrent: \"%s\"\n", escape(current))
	for _, take := range e.takes {
		fmt.Fprintf(e.w, "\tTake: \"%s\" {\n", escape(take.name))
		fmt.Fprintf(e.w, "\t\tFileName: \"%s.tak\"\n", escape(take.name))
		fmt.Fprintf(e.w, "\t\tLocalTime: %d,%d\n", take.start, take.stop)
		fmt.Fprintf(e.w, "\t\tReferenceTime: %d,%d\n", take.start, take.stop)
		fmt.Fprintln(e.w, "\t}")
	}
	fmt.Fprintln(e.w, "}")
}

func (e *exporter) animationDefinitionCounts() (int, int, int, int) {
	stackCount := 0
	layerCount := 0
	curveNodeCount := 0
	curveCount := 0
	for _, anim := range e.doc.Animations {
		if anim == nil || len(anim.Channels) == 0 {
			continue
		}
		stackCount++
		layerCount++
		for _, ch := range anim.Channels {
			if ch == nil || ch.Target.Node == nil || int(*ch.Target.Node) >= len(e.doc.Nodes) || !e.shouldExportModel(*ch.Target.Node) {
				continue
			}
			switch ch.Target.Path {
			case gltf.TRSTranslation, gltf.TRSRotation, gltf.TRSScale:
				curveNodeCount++
				curveCount += 3
			}
		}
	}
	return stackCount, layerCount, curveNodeCount, curveCount
}

func (e *exporter) writeAnimations() error {
	frameRate := e.animationFrameRate()
	for animIdx, anim := range e.doc.Animations {
		if anim == nil || len(anim.Channels) == 0 {
			continue
		}
		name := cleanName(anim.Name)
		if name == "" {
			name = fmt.Sprintf("Animation_%d", animIdx)
		}
		stackID := e.newID()
		layerID := e.newID()
		start, stop := int64(0), int64(0)
		fmt.Fprintf(e.w, "\tAnimationStack: %d, \"AnimStack::%s\", \"\" {\n", stackID, escape(name))
		fmt.Fprintln(e.w, "\t}")
		fmt.Fprintf(e.w, "\tAnimationLayer: %d, \"AnimLayer::BaseLayer\", \"\" {\n", layerID)
		fmt.Fprintln(e.w, "\t}")
		e.conns = append(e.conns, connection{child: layerID, parent: stackID})

		for _, ch := range anim.Channels {
			if ch == nil || ch.Sampler == nil || int(*ch.Sampler) >= len(anim.Samplers) || ch.Target.Node == nil {
				continue
			}
			nodeIdx := *ch.Target.Node
			if int(nodeIdx) >= len(e.doc.Nodes) || !e.shouldExportModel(nodeIdx) {
				continue
			}
			sampler := anim.Samplers[*ch.Sampler]
			times, err := e.readScalarFloatAccessor(sampler.Input)
			if err != nil {
				return fmt.Errorf("fbx animation %s: read times: %w", name, err)
			}
			props, values, err := e.animationChannelValues(ch.Target.Path, sampler.Output)
			if err != nil {
				return fmt.Errorf("fbx animation %s: read values: %w", name, err)
			}
			if len(times) == 0 || len(values) == 0 {
				continue
			}
			if len(values) < len(times) {
				times = times[:len(values)]
			} else if len(times) < len(values) {
				values = values[:len(times)]
			}
			if v := fbxTime(times[0]); start == 0 || v < start {
				start = v
			}
			if v := fbxTime(times[len(times)-1]); v > stop {
				stop = v
			}
			curveNodeID := e.newID()
			curveNodeName := animationCurveNodeName(ch.Target.Path)
			fmt.Fprintf(e.w, "\tAnimationCurveNode: %d, \"AnimCurveNode::%s\", \"\" {\n", curveNodeID, curveNodeName)
			fmt.Fprintln(e.w, "\t\tProperties70:  {")
			for _, prop := range props {
				fmt.Fprintf(e.w, "\t\t\tP: \"%s\", \"Number\", \"\", \"A\",0\n", prop)
			}
			fmt.Fprintln(e.w, "\t\t}")
			fmt.Fprintln(e.w, "\t}")
			e.conns = append(e.conns, connection{child: curveNodeID, parent: e.idForNode(nodeIdx), prop: animationNodeProperty(ch.Target.Path)})
			e.conns = append(e.conns, connection{child: curveNodeID, parent: layerID})

			for axis := 0; axis < 3; axis++ {
				curveID := e.newID()
				axisValues := make([]float64, len(values))
				for i, v := range values {
					axisValues[i] = float64(v[axis])
				}
				e.writeAnimationCurve(curveID, props[axis], times, axisValues)
				e.conns = append(e.conns, connection{child: curveID, parent: curveNodeID, prop: props[axis]})
			}
		}

		if stop == 0 {
			stop = fbxTime(float32(1.0 / frameRate))
		}
		start = alignFBXTimeToFrame(start, frameRate)
		stop = alignFBXTimeToFrame(stop, frameRate)
		e.takes = append(e.takes, fbxTake{name: name, start: start, stop: stop})
	}
	return nil
}

func (e *exporter) animationFrameRate() float64 {
	const fallback = 30.0
	extras, ok := e.doc.Extras.(map[string]any)
	if !ok {
		return fallback
	}
	value, ok := extras["frameRate"]
	if !ok {
		return fallback
	}
	switch v := value.(type) {
	case int:
		if v > 0 {
			return float64(v)
		}
	case int32:
		if v > 0 {
			return float64(v)
		}
	case int64:
		if v > 0 {
			return float64(v)
		}
	case uint32:
		if v > 0 {
			return float64(v)
		}
	case uint64:
		if v > 0 {
			return float64(v)
		}
	case float32:
		if v > 0 {
			return float64(v)
		}
	case float64:
		if v > 0 {
			return v
		}
	}
	return fallback
}

func (e *exporter) animationChannelValues(path gltf.TRSProperty, accessor uint32) ([]string, []mgl32.Vec3, error) {
	switch path {
	case gltf.TRSTranslation:
		values, err := e.readVec3Accessor(accessor)
		return []string{"d|X", "d|Y", "d|Z"}, values, err
	case gltf.TRSRotation:
		quats, err := e.readVec4FloatAccessor(accessor)
		if err != nil {
			return nil, nil, err
		}
		values := make([]mgl32.Vec3, len(quats))
		for i, qv := range quats {
			q := mgl32.Quat{V: mgl32.Vec3{qv[0], qv[1], qv[2]}, W: qv[3]}
			values[i] = quatToEulerDegrees(q)
		}
		return []string{"d|X", "d|Y", "d|Z"}, values, nil
	case gltf.TRSScale:
		values, err := e.readVec3Accessor(accessor)
		return []string{"d|X", "d|Y", "d|Z"}, values, err
	default:
		return nil, nil, fmt.Errorf("unsupported animation path %v", path)
	}
}

func (e *exporter) writeAnimationCurve(id int64, name string, times []float32, values []float64) {
	fmt.Fprintf(e.w, "\tAnimationCurve: %d, \"AnimCurve::%s\", \"\" {\n", id, name)
	fmt.Fprintln(e.w, "\t\tDefault: 0")
	fmt.Fprintf(e.w, "\t\tKeyVer: 4008\n")
	writeInt64Array(e.w, "\t\tKeyTime", timesToFBX(times))
	writeFloatArray(e.w, "\t\tKeyValueFloat", values)
	flags := make([]int, len(times))
	for i := range flags {
		flags[i] = 260
	}
	writeIntArray(e.w, "\t\tKeyAttrFlags", flags)
	writeFloatArray(e.w, "\t\tKeyAttrDataFloat", []float64{0, 0, 9.41996334692463e-30, 0})
	writeIntArray(e.w, "\t\tKeyAttrRefCount", []int{len(times)})
	fmt.Fprintln(e.w, "\t}")
}

func animationCurveNodeName(path gltf.TRSProperty) string {
	switch path {
	case gltf.TRSTranslation:
		return "T"
	case gltf.TRSRotation:
		return "R"
	case gltf.TRSScale:
		return "S"
	default:
		return "Unknown"
	}
}

func animationNodeProperty(path gltf.TRSProperty) string {
	switch path {
	case gltf.TRSTranslation:
		return "Lcl Translation"
	case gltf.TRSRotation:
		return "Lcl Rotation"
	case gltf.TRSScale:
		return "Lcl Scaling"
	default:
		return ""
	}
}

func fbxTime(seconds float32) int64 {
	return int64(math.Round(float64(seconds) * fbxTimeUnitsPerSecond))
}

func fbxTimeFromSeconds(seconds float64) int64 {
	return int64(math.Round(seconds * fbxTimeUnitsPerSecond))
}

func alignFBXTimeToFrame(t int64, frameRate float64) int64 {
	if t <= 0 || frameRate <= 0 {
		return t
	}
	seconds := float64(t) / fbxTimeUnitsPerSecond
	frames := seconds * frameRate
	rounded := math.Round(frames)
	if math.Abs(frames-rounded) < 0.0001 {
		return fbxTimeFromSeconds(rounded / frameRate)
	}
	return fbxTimeFromSeconds(math.Ceil(frames) / frameRate)
}

func timesToFBX(times []float32) []int64 {
	out := make([]int64, len(times))
	for i, t := range times {
		out[i] = fbxTime(t)
	}
	return out
}

func (e *exporter) parentMap() map[uint32]uint32 {
	parents := make(map[uint32]uint32)
	for parentIdx, node := range e.doc.Nodes {
		for _, child := range node.Children {
			parents[child] = uint32(parentIdx)
		}
	}
	return parents
}

func (e *exporter) isJointNode(idx uint32) bool {
	for _, skin := range e.doc.Skins {
		for _, joint := range skin.Joints {
			if joint == idx {
				return true
			}
		}
	}
	return false
}

func (e *exporter) mergeMesh(mesh *gltf.Mesh) (primitiveData, []int, error) {
	var out primitiveData
	materialSlots := make([]int, 0)
	materialSlotByID := make(map[uint32]int)

	for _, prim := range mesh.Primitives {
		pd, err := e.readPrimitive(prim)
		if err != nil {
			return out, nil, err
		}
		base := uint32(len(out.positions))
		out.positions = append(out.positions, pd.positions...)
		out.normals = append(out.normals, pd.normals...)
		out.uvs = append(out.uvs, pd.uvs...)
		out.joints = append(out.joints, pd.joints...)
		out.weights = append(out.weights, pd.weights...)
		slot := 0
		if pd.material != nil {
			var ok bool
			slot, ok = materialSlotByID[*pd.material]
			if !ok {
				slot = len(materialSlotByID)
				materialSlotByID[*pd.material] = slot
				out.materials = append(out.materials, *pd.material)
			}
		}
		baseInt := int(base)
		for tri := 0; tri+2 < len(pd.indices); tri += 3 {
			out.indices = append(out.indices,
				baseInt+pd.indices[tri],
				baseInt+pd.indices[tri+1],
				-(baseInt + pd.indices[tri+2] + 1),
			)
			materialSlots = append(materialSlots, slot)
		}
	}
	return out, materialSlots, nil
}

func (e *exporter) readPrimitive(prim *gltf.Primitive) (primitiveData, error) {
	var out primitiveData
	positionIdx, ok := prim.Attributes[gltf.POSITION]
	if !ok {
		return out, fmt.Errorf("primitive missing POSITION")
	}
	positions, err := e.readVec3Accessor(positionIdx)
	if err != nil {
		return out, err
	}
	out.positions = positions
	if normalIdx, ok := prim.Attributes[gltf.NORMAL]; ok {
		out.normals, _ = e.readVec3Accessor(normalIdx)
	}
	if uvIdx, ok := prim.Attributes[gltf.TEXCOORD_0]; ok {
		out.uvs, _ = e.readVec2Accessor(uvIdx)
	}
	if jointIdx, ok := prim.Attributes[gltf.JOINTS_0]; ok {
		out.joints, _ = e.readVec4UintAccessor(jointIdx)
	}
	if weightIdx, ok := prim.Attributes[gltf.WEIGHTS_0]; ok {
		out.weights, _ = e.readVec4FloatAccessor(weightIdx)
	}
	if prim.Indices != nil {
		rawIndices, err := e.readScalarUintAccessor(*prim.Indices)
		if err != nil {
			return out, err
		}
		out.indices = make([]int, len(rawIndices))
		for i, raw := range rawIndices {
			out.indices[i] = int(raw)
		}
	} else {
		out.indices = make([]int, len(out.positions))
		for i := range out.indices {
			out.indices[i] = i
		}
	}
	out.material = prim.Material
	return out, nil
}

func (e *exporter) accessorBytes(idx uint32) (*gltf.Accessor, []byte, uint32, error) {
	if int(idx) >= len(e.doc.Accessors) {
		return nil, nil, 0, fmt.Errorf("accessor %d out of bounds", idx)
	}
	acc := e.doc.Accessors[idx]
	if acc.BufferView == nil || int(*acc.BufferView) >= len(e.doc.BufferViews) {
		return nil, nil, 0, fmt.Errorf("accessor %d has no buffer view", idx)
	}
	bv := e.doc.BufferViews[*acc.BufferView]
	if int(bv.Buffer) >= len(e.doc.Buffers) {
		return nil, nil, 0, fmt.Errorf("buffer %d out of bounds", bv.Buffer)
	}
	buf := e.doc.Buffers[bv.Buffer]
	data := buf.Data
	offset := bv.ByteOffset + acc.ByteOffset
	stride := bv.ByteStride
	if stride == 0 {
		stride = acc.ComponentType.ByteSize() * accessorElements(acc.Type)
	}
	if int(offset) > len(data) {
		return nil, nil, 0, fmt.Errorf("accessor %d offset out of bounds", idx)
	}
	return acc, data[offset:], stride, nil
}

func (e *exporter) readVec3Accessor(idx uint32) ([]mgl32.Vec3, error) {
	acc, data, stride, err := e.accessorBytes(idx)
	if err != nil {
		return nil, err
	}
	out := make([]mgl32.Vec3, acc.Count)
	for i := uint32(0); i < acc.Count; i++ {
		base := i * stride
		out[i] = mgl32.Vec3{
			readComponent(data, base, acc.ComponentType),
			readComponent(data, base+acc.ComponentType.ByteSize(), acc.ComponentType),
			readComponent(data, base+2*acc.ComponentType.ByteSize(), acc.ComponentType),
		}
	}
	return out, nil
}

func (e *exporter) readVec2Accessor(idx uint32) ([]mgl32.Vec2, error) {
	acc, data, stride, err := e.accessorBytes(idx)
	if err != nil {
		return nil, err
	}
	out := make([]mgl32.Vec2, acc.Count)
	for i := uint32(0); i < acc.Count; i++ {
		base := i * stride
		out[i] = mgl32.Vec2{
			readComponent(data, base, acc.ComponentType),
			readComponent(data, base+acc.ComponentType.ByteSize(), acc.ComponentType),
		}
	}
	return out, nil
}

func (e *exporter) readVec4FloatAccessor(idx uint32) ([][4]float32, error) {
	acc, data, stride, err := e.accessorBytes(idx)
	if err != nil {
		return nil, err
	}
	out := make([][4]float32, acc.Count)
	for i := uint32(0); i < acc.Count; i++ {
		base := i * stride
		for j := uint32(0); j < 4; j++ {
			out[i][j] = readComponent(data, base+j*acc.ComponentType.ByteSize(), acc.ComponentType)
		}
	}
	return out, nil
}

func (e *exporter) readVec4UintAccessor(idx uint32) ([][4]uint32, error) {
	acc, data, stride, err := e.accessorBytes(idx)
	if err != nil {
		return nil, err
	}
	out := make([][4]uint32, acc.Count)
	for i := uint32(0); i < acc.Count; i++ {
		base := i * stride
		for j := uint32(0); j < 4; j++ {
			out[i][j] = readUintComponent(data, base+j*acc.ComponentType.ByteSize(), acc.ComponentType)
		}
	}
	return out, nil
}

func (e *exporter) readScalarUintAccessor(idx uint32) ([]uint32, error) {
	acc, data, stride, err := e.accessorBytes(idx)
	if err != nil {
		return nil, err
	}
	out := make([]uint32, acc.Count)
	for i := uint32(0); i < acc.Count; i++ {
		out[i] = readUintComponent(data, i*stride, acc.ComponentType)
	}
	return out, nil
}

func (e *exporter) readScalarFloatAccessor(idx uint32) ([]float32, error) {
	acc, data, stride, err := e.accessorBytes(idx)
	if err != nil {
		return nil, err
	}
	out := make([]float32, acc.Count)
	for i := uint32(0); i < acc.Count; i++ {
		out[i] = readComponent(data, i*stride, acc.ComponentType)
	}
	return out, nil
}

func (e *exporter) readMat4Accessor(idx *uint32) ([]mgl32.Mat4, error) {
	if idx == nil {
		return nil, fmt.Errorf("no matrix accessor")
	}
	acc, data, stride, err := e.accessorBytes(*idx)
	if err != nil {
		return nil, err
	}
	out := make([]mgl32.Mat4, acc.Count)
	for i := uint32(0); i < acc.Count; i++ {
		base := i * stride
		var vals [16]float32
		for j := uint32(0); j < 16; j++ {
			vals[j] = readComponent(data, base+j*acc.ComponentType.ByteSize(), acc.ComponentType)
		}
		out[i] = mgl32.Mat4(vals)
	}
	return out, nil
}

func readComponent(data []byte, offset uint32, ct gltf.ComponentType) float32 {
	if int(offset+ct.ByteSize()) > len(data) {
		return 0
	}
	switch ct {
	case gltf.ComponentFloat:
		return math.Float32frombits(binary.LittleEndian.Uint32(data[offset:]))
	case gltf.ComponentUbyte:
		return float32(data[offset])
	case gltf.ComponentByte:
		return float32(int8(data[offset]))
	case gltf.ComponentUshort:
		return float32(binary.LittleEndian.Uint16(data[offset:]))
	case gltf.ComponentShort:
		return float32(int16(binary.LittleEndian.Uint16(data[offset:])))
	case gltf.ComponentUint:
		return float32(binary.LittleEndian.Uint32(data[offset:]))
	default:
		return 0
	}
}

func readUintComponent(data []byte, offset uint32, ct gltf.ComponentType) uint32 {
	if int(offset+ct.ByteSize()) > len(data) {
		return 0
	}
	switch ct {
	case gltf.ComponentUbyte:
		return uint32(data[offset])
	case gltf.ComponentByte:
		return uint32(int8(data[offset]))
	case gltf.ComponentUshort:
		return uint32(binary.LittleEndian.Uint16(data[offset:]))
	case gltf.ComponentShort:
		return uint32(int16(binary.LittleEndian.Uint16(data[offset:])))
	case gltf.ComponentUint:
		return binary.LittleEndian.Uint32(data[offset:])
	case gltf.ComponentFloat:
		return uint32(math.Float32frombits(binary.LittleEndian.Uint32(data[offset:])))
	default:
		return 0
	}
}

type clusterWeights struct {
	indices []int
	weights []float64
}

func collectClusterWeights(joints [][4]uint32, weights [][4]float32, jointCount int) []clusterWeights {
	out := make([]clusterWeights, jointCount)
	for vertex := range joints {
		for slot := 0; slot < 4; slot++ {
			joint := int(joints[vertex][slot])
			if joint < 0 || joint >= jointCount {
				continue
			}
			weight := float64(0)
			if vertex < len(weights) {
				weight = float64(weights[vertex][slot])
			}
			if weight <= 0 {
				continue
			}
			out[joint].indices = append(out[joint].indices, vertex)
			out[joint].weights = append(out[joint].weights, weight)
		}
	}
	return out
}

func meshMaterialIndices(mesh *gltf.Mesh) []uint32 {
	seen := make(map[uint32]bool)
	var out []uint32
	for _, prim := range mesh.Primitives {
		if prim.Material == nil || seen[*prim.Material] {
			continue
		}
		seen[*prim.Material] = true
		out = append(out, *prim.Material)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func nodeTRS(node *gltf.Node) (mgl32.Vec3, mgl32.Vec3, mgl32.Vec3) {
	t := mgl32.Vec3{node.Translation[0], node.Translation[1], node.Translation[2]}
	s := mgl32.Vec3{1, 1, 1}
	if !zeroVec3(node.Scale) {
		s = mgl32.Vec3{node.Scale[0], node.Scale[1], node.Scale[2]}
	}
	r := mgl32.Vec3{}
	if !zeroVec4(node.Rotation) {
		q := mgl32.Quat{V: mgl32.Vec3{node.Rotation[0], node.Rotation[1], node.Rotation[2]}, W: node.Rotation[3]}.Normalize()
		r = quatToEulerDegrees(q)
	}
	if !zeroMat4(node.Matrix) {
		m := mgl32.Mat4(node.Matrix)
		t = mgl32.Vec3{m[12], m[13], m[14]}
		s = mgl32.Vec3{m.Col(0).Vec3().Len(), m.Col(1).Vec3().Len(), m.Col(2).Vec3().Len()}
		rot := m
		if s[0] != 0 {
			rot[0], rot[1], rot[2] = rot[0]/s[0], rot[1]/s[0], rot[2]/s[0]
		}
		if s[1] != 0 {
			rot[4], rot[5], rot[6] = rot[4]/s[1], rot[5]/s[1], rot[6]/s[1]
		}
		if s[2] != 0 {
			rot[8], rot[9], rot[10] = rot[8]/s[2], rot[9]/s[2], rot[10]/s[2]
		}
		r = matrixToEulerXYZDegrees(rot)
	}
	return t, r, s
}

func (e *exporter) globalMatrix(idx uint32) mgl32.Mat4 {
	parents := e.parentMap()
	local := nodeMatrix(e.doc.Nodes[idx])
	if parent, ok := parents[idx]; ok {
		return e.globalMatrix(parent).Mul4(local)
	}
	return local
}

func (e *exporter) exportGlobalMatrix(idx uint32) mgl32.Mat4 {
	return ueForwardCorrectionMatrix().Mul4(e.globalMatrix(idx))
}

func ueForwardCorrectionMatrix() mgl32.Mat4 {
	return mgl32.Ident4()
}

func matrixToTRS(m mgl32.Mat4) (mgl32.Vec3, mgl32.Vec3, mgl32.Vec3) {
	t := mgl32.Vec3{m[12], m[13], m[14]}
	s := mgl32.Vec3{m.Col(0).Vec3().Len(), m.Col(1).Vec3().Len(), m.Col(2).Vec3().Len()}
	rot := m
	if s[0] != 0 {
		rot[0], rot[1], rot[2] = rot[0]/s[0], rot[1]/s[0], rot[2]/s[0]
	}
	if s[1] != 0 {
		rot[4], rot[5], rot[6] = rot[4]/s[1], rot[5]/s[1], rot[6]/s[1]
	}
	if s[2] != 0 {
		rot[8], rot[9], rot[10] = rot[8]/s[2], rot[9]/s[2], rot[10]/s[2]
	}
	r := matrixToEulerXYZDegrees(rot)
	return t, r, s
}

func nodeMatrix(node *gltf.Node) mgl32.Mat4 {
	if !zeroMat4(node.Matrix) {
		return mgl32.Mat4(node.Matrix)
	}
	t, _, s := nodeTRS(node)
	m := mgl32.Translate3D(t[0], t[1], t[2])
	if !zeroVec4(node.Rotation) {
		q := mgl32.Quat{V: mgl32.Vec3{node.Rotation[0], node.Rotation[1], node.Rotation[2]}, W: node.Rotation[3]}.Normalize()
		m = m.Mul4(q.Mat4())
	}
	m = m.Mul4(mgl32.Scale3D(s[0], s[1], s[2]))
	return m
}

func zeroVec3(v [3]float32) bool {
	return v[0] == 0 && v[1] == 0 && v[2] == 0
}

func zeroVec4(v [4]float32) bool {
	return v[0] == 0 && v[1] == 0 && v[2] == 0 && v[3] == 0
}

func zeroMat4(m [16]float32) bool {
	for i := range m {
		if m[i] != 0 {
			return false
		}
	}
	return true
}

func quatToEulerDegrees(q mgl32.Quat) mgl32.Vec3 {
	return matrixToEulerXYZDegrees(q.Normalize().Mat4())
}

func matrixToEulerXYZDegrees(m mgl32.Mat4) mgl32.Vec3 {
	m00 := float64(m[0])
	m10 := float64(m[1])
	m20 := float64(m[2])
	m11 := float64(m[5])
	m12 := float64(m[9])
	m21 := float64(m[6])
	m22 := float64(m[10])

	m20 = math.Max(-1, math.Min(1, m20))
	y := math.Asin(-m20)
	var x, z float64
	if math.Abs(m20) < 0.999999 {
		x = math.Atan2(m21, m22)
		z = math.Atan2(m10, m00)
	} else {
		x = math.Atan2(-m12, m11)
		z = 0
	}
	return mgl32.Vec3{
		float32(x * 180 / math.Pi),
		float32(y * 180 / math.Pi),
		float32(z * 180 / math.Pi),
	}
}

func flattenVec3(v []mgl32.Vec3) []float64 {
	out := make([]float64, 0, len(v)*3)
	for _, x := range v {
		out = append(out, float64(x[0]), float64(x[1]), float64(x[2]))
	}
	return out
}

func flattenVec2(v []mgl32.Vec2) []float64 {
	out := make([]float64, 0, len(v)*2)
	for _, x := range v {
		out = append(out, float64(x[0]), float64(1-x[1]))
	}
	return out
}

func flattenMat4(m mgl32.Mat4) []float64 {
	out := make([]float64, 16)
	for i := range out {
		out[i] = float64(m[i])
	}
	return out
}

func writeFloatArray(w io.Writer, name string, vals []float64) {
	fmt.Fprintf(w, "%s: *%d {\n", name, len(vals))
	fmt.Fprintf(w, "%s\ta: ", indentOf(name))
	for i, val := range vals {
		if i > 0 {
			fmt.Fprint(w, ",")
		}
		fmt.Fprint(w, f64(val))
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%s}\n", indentOf(name))
}

func writeIntArray[T ~int | ~uint32](w io.Writer, name string, vals []T) {
	fmt.Fprintf(w, "%s: *%d {\n", name, len(vals))
	fmt.Fprintf(w, "%s\ta: ", indentOf(name))
	for i, val := range vals {
		if i > 0 {
			fmt.Fprint(w, ",")
		}
		fmt.Fprintf(w, "%d", val)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%s}\n", indentOf(name))
}

func writeInt64Array(w io.Writer, name string, vals []int64) {
	fmt.Fprintf(w, "%s: *%d {\n", name, len(vals))
	fmt.Fprintf(w, "%s\ta: ", indentOf(name))
	for i, val := range vals {
		if i > 0 {
			fmt.Fprint(w, ",")
		}
		fmt.Fprintf(w, "%d", val)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%s}\n", indentOf(name))
}

func indentOf(name string) string {
	idx := strings.LastIndex(name, "\t")
	if idx < 0 {
		return ""
	}
	return name[:idx+1]
}

func accessorElements(t gltf.AccessorType) uint32 {
	switch t {
	case gltf.AccessorScalar:
		return 1
	case gltf.AccessorVec2:
		return 2
	case gltf.AccessorVec3:
		return 3
	case gltf.AccessorVec4:
		return 4
	case gltf.AccessorMat2:
		return 4
	case gltf.AccessorMat3:
		return 9
	case gltf.AccessorMat4:
		return 16
	default:
		return 1
	}
}

func cleanName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "\x00", "")
	if len(name) > 120 {
		name = name[len(name)-120:]
	}
	return name
}

func animationSourceHash(anim *gltf.Animation) string {
	extras, ok := anim.Extras.(map[string]any)
	if !ok {
		return ""
	}
	value, ok := extras["source_hash"]
	if !ok {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	default:
		return fmt.Sprintf("%v", v)
	}
}

func safeFileStem(name string) string {
	name = cleanName(name)
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	stem := strings.Trim(b.String(), " ._")
	if stem == "" {
		stem = "animation"
	}
	if len(stem) > 96 {
		stem = stem[len(stem)-96:]
	}
	return stem
}

func escape(s string) string {
	s = strings.ReplaceAll(s, "\\", "_")
	s = strings.ReplaceAll(s, "\"", "_")
	return s
}

func f64(v float64) string {
	if math.Abs(v) < 0.0000001 {
		v = 0
	}
	return strconvFormatFloat(v)
}

func strconvFormatFloat(v float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.9f", v), "0"), ".")
}
