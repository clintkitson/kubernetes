// +build linux

/*
Copyright 2014 The Kubernetes Authors All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package rexray

import (
	"os"
	"path"
	"testing"

	log "github.com/Sirupsen/logrus"

	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/resource"
	"k8s.io/kubernetes/pkg/types"
	utiltesting "k8s.io/kubernetes/pkg/util/testing"
	"k8s.io/kubernetes/pkg/volume"
)

func TestCanSupport(t *testing.T) {
	plugMgr := volume.VolumePluginMgr{}
	plugMgr.InitPlugins(ProbeVolumePlugins(), volume.NewFakeVolumeHost("fake", nil, nil))

	plug, err := plugMgr.FindPluginByName("kubernetes.io/rexray")
	if err != nil {
		t.Errorf("Can't find the plugin by name")
	}
	if plug.Name() != "kubernetes.io/rexray" {
		t.Errorf("Wrong name: %s", plug.Name())
	}
	if !plug.CanSupport(&volume.Spec{Volume: &api.Volume{VolumeSource: api.VolumeSource{RexRay: &api.RexRayVolumeSource{}}}}) {
		t.Errorf("Expected true")
	}
	if !plug.CanSupport(&volume.Spec{PersistentVolume: &api.PersistentVolume{Spec: api.PersistentVolumeSpec{PersistentVolumeSource: api.PersistentVolumeSource{RexRay: &api.RexRayVolumeSource{}}}}}) {
		t.Errorf("Expected true")
	}
	if plug.CanSupport(&volume.Spec{Volume: &api.Volume{VolumeSource: api.VolumeSource{}}}) {
		t.Errorf("Expected false")
	}
}

func TestGetAccessModes(t *testing.T) {
	plugMgr := volume.VolumePluginMgr{}
	plugMgr.InitPlugins(ProbeVolumePlugins(), volume.NewFakeVolumeHost("/tmp/fake", nil, nil))

	plug, err := plugMgr.FindPersistentPluginByName("kubernetes.io/rexray")
	if err != nil {
		t.Errorf("Can't find the plugin by name")
	}
	if len(plug.GetAccessModes()) != 1 || plug.GetAccessModes()[0] != api.ReadWriteOnce {
		t.Errorf("Expected %s PersistentVolumeAccessMode", api.ReadWriteOnce)
	}
}

func TestPlugin(t *testing.T) {
	tmpDir, err := utiltesting.MkTmpdir("rexrayTest")
	if err != nil {
		t.Fatalf("can't make a temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	plugMgr := volume.VolumePluginMgr{}
	plugMgr.InitPlugins(ProbeVolumePlugins(), volume.NewFakeVolumeHost(tmpDir, nil, nil))

	plug, err := plugMgr.FindPluginByName("kubernetes.io/rexray")
	if err != nil {
		t.Errorf("Can't find the plugin by name")
	}
	spec := &api.Volume{
		Name:         "test",
		VolumeSource: api.VolumeSource{RexRay: &api.RexRayVolumeSource{VolumeName: "test", Module: "rexray"}},
	}
	pod := &api.Pod{ObjectMeta: api.ObjectMeta{UID: types.UID("poduid")}}
	builder, err := plug.NewBuilder(volume.NewSpecFromVolume(spec), pod, volume.VolumeOptions{})
	if err != nil {
		t.Errorf("Failed to make a new Builder: %v", err)
	}
	if builder == nil {
		t.Errorf("Got a nil Builder")
	}

	volPath := path.Join(tmpDir, "pods/poduid/volumes/kubernetes.io~rexray/test")
	path := builder.GetPath()
	log.Info("path: ", path)
	if path != volPath {
		t.Errorf("Got unexpected path: %s", path)
	}

	if err := builder.SetUp(nil); err != nil {
		t.Errorf("Expected success, got: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			t.Errorf("SetUp() failed, volume path not created: %s", path)
		} else {
			t.Errorf("SetUp() failed: %v", err)
		}
	}

	cleaner, err := plug.NewCleaner("test", types.UID("poduid"))
	if err != nil {
		t.Errorf("Failed to make a new Cleaner: %v", err)
	}
	if cleaner == nil {
		t.Errorf("Got a nil Cleaner")
	}

	if err := cleaner.TearDown(); err != nil {
		t.Errorf("Expected success, got: %v", err)
	}

	if _, err := os.Stat(path); err == nil {
		t.Errorf("TearDown() failed, volume path still exists: %s", path)
	} else if !os.IsNotExist(err) {
		t.Errorf("SetUp() failed: %v", err)
	}

}

func TestProvisioner(t *testing.T) {
	tmpDir, err := utiltesting.MkTmpdir("rexrayTest")
	if err != nil {
		t.Fatalf("can't make a temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	plugMgr := volume.VolumePluginMgr{}
	plugMgr.InitPlugins(ProbeVolumePlugins(), volume.NewFakeVolumeHost(tmpDir, nil, nil))

	plug, err := plugMgr.FindPluginByName("kubernetes.io/rexray")
	if err != nil {
		t.Errorf("Can't find the plugin by name")
	}

	cap := resource.NewQuantity(1*1024*1024*1024, resource.BinarySI)
	options := volume.VolumeOptions{
		Capacity: *cap,
		AccessModes: []api.PersistentVolumeAccessMode{
			api.ReadWriteOnce,
		},
		PersistentVolumeReclaimPolicy: api.PersistentVolumeReclaimDelete,
	}

	provisioner, err := plug.(*rexrayPlugin).NewProvisioner(options)
	if err != nil {
		t.Errorf("Failed to make a new Provisioner: %v", err)
	}

	persistentSpec, err := provisioner.NewPersistentVolumeTemplate()
	if err != nil {
		t.Errorf("NewPersistentVolumeTemplate() failed: %v", err)
	}

	// get 2nd Provisioner - persistent volume controller will do the same
	// provisioner, err = plug.(*rexrayPlugin).newProvisionerInternal(options, &fakePDManager{0})
	err = provisioner.Provision(persistentSpec)
	if err != nil {
		t.Errorf("Provision() failed: %v", err)
	}

	volSpec := &volume.Spec{
		PersistentVolume: persistentSpec,
	}

	deleter, err := plug.(*rexrayPlugin).NewDeleter(volSpec)
	if err != nil {
		t.Errorf("NewDeleter() failed: %v", err)
	}

	err = deleter.Delete()
	if err != nil {
		t.Errorf("Deleter() failed: %v", err)
	}
}

//
// func TestRecycler(t *testing.T) {
// 	plugMgr := volume.VolumePluginMgr{}
// 	pluginHost := volume.NewFakeVolumeHost("/tmp/fake", nil, nil)
// 	plugMgr.InitPlugins([]volume.VolumePlugin{&hostPathPlugin{nil, volume.NewFakeRecycler, nil, nil, volume.VolumeConfig{}}}, pluginHost)
//
// 	spec := &volume.Spec{PersistentVolume: &api.PersistentVolume{Spec: api.PersistentVolumeSpec{PersistentVolumeSource: api.PersistentVolumeSource{HostPath: &api.HostPathVolumeSource{Path: "/foo"}}}}}
// 	plug, err := plugMgr.FindRecyclablePluginBySpec(spec)
// 	if err != nil {
// 		t.Errorf("Can't find the plugin by name")
// 	}
// 	recycler, err := plug.NewRecycler(spec)
// 	if err != nil {
// 		t.Errorf("Failed to make a new Recyler: %v", err)
// 	}
// 	if recycler.GetPath() != spec.PersistentVolume.Spec.HostPath.Path {
// 		t.Errorf("Expected %s but got %s", spec.PersistentVolume.Spec.HostPath.Path, recycler.GetPath())
// 	}
// 	if err := recycler.Recycle(); err != nil {
// 		t.Errorf("Mock Recycler expected to return nil but got %s", err)
// 	}
// }
//
// func TestDeleter(t *testing.T) {
// 	// Deleter has a hard-coded regex for "/tmp".
// 	tempPath := fmt.Sprintf("/tmp/hostpath/%s", util.NewUUID())
// 	defer os.RemoveAll(tempPath)
// 	err := os.MkdirAll(tempPath, 0750)
// 	if err != nil {
// 		t.Fatalf("Failed to create tmp directory for deleter: %v", err)
// 	}
//
// 	plugMgr := volume.VolumePluginMgr{}
// 	plugMgr.InitPlugins(ProbeVolumePlugins(), volume.NewFakeVolumeHost("/tmp/fake", nil, nil))
//
// 	spec := &volume.Spec{PersistentVolume: &api.PersistentVolume{Spec: api.PersistentVolumeSpec{PersistentVolumeSource: api.PersistentVolumeSource{HostPath: &api.HostPathVolumeSource{Path: tempPath}}}}}
// 	plug, err := plugMgr.FindDeletablePluginBySpec(spec)
// 	if err != nil {
// 		t.Errorf("Can't find the plugin by name")
// 	}
// 	deleter, err := plug.NewDeleter(spec)
// 	if err != nil {
// 		t.Errorf("Failed to make a new Deleter: %v", err)
// 	}
// 	if deleter.GetPath() != tempPath {
// 		t.Errorf("Expected %s but got %s", tempPath, deleter.GetPath())
// 	}
// 	if err := deleter.Delete(); err != nil {
// 		t.Errorf("Mock Recycler expected to return nil but got %s", err)
// 	}
// 	if exists, _ := util.FileExists("foo"); exists {
// 		t.Errorf("Temp path expected to be deleted, but was found at %s", tempPath)
// 	}
// }
//
// func TestDeleterTempDir(t *testing.T) {
// 	tests := map[string]struct {
// 		expectedFailure bool
// 		path            string
// 	}{
// 		"just-tmp": {true, "/tmp"},
// 		"not-tmp":  {true, "/nottmp"},
// 		"good-tmp": {false, "/tmp/scratch"},
// 	}
//
// 	for name, test := range tests {
// 		plugMgr := volume.VolumePluginMgr{}
// 		plugMgr.InitPlugins(ProbeVolumePlugins(), volume.NewFakeVolumeHost("/tmp/fake", nil, nil))
// 		spec := &volume.Spec{PersistentVolume: &api.PersistentVolume{Spec: api.PersistentVolumeSpec{PersistentVolumeSource: api.PersistentVolumeSource{HostPath: &api.HostPathVolumeSource{Path: test.path}}}}}
// 		plug, _ := plugMgr.FindDeletablePluginBySpec(spec)
// 		deleter, _ := plug.NewDeleter(spec)
// 		err := deleter.Delete()
// 		if err == nil && test.expectedFailure {
// 			t.Errorf("Expected failure for test '%s' but got nil err", name)
// 		}
// 		if err != nil && !test.expectedFailure {
// 			t.Errorf("Unexpected failure for test '%s': %v", name, err)
// 		}
// 	}
// }
//

//
// func TestPersistentClaimReadOnlyFlag(t *testing.T) {
// 	pv := &api.PersistentVolume{
// 		ObjectMeta: api.ObjectMeta{
// 			Name: "pvA",
// 		},
// 		Spec: api.PersistentVolumeSpec{
// 			PersistentVolumeSource: api.PersistentVolumeSource{
// 				HostPath: &api.HostPathVolumeSource{Path: "foo"},
// 			},
// 			ClaimRef: &api.ObjectReference{
// 				Name: "claimA",
// 			},
// 		},
// 	}
//
// 	claim := &api.PersistentVolumeClaim{
// 		ObjectMeta: api.ObjectMeta{
// 			Name:      "claimA",
// 			Namespace: "nsA",
// 		},
// 		Spec: api.PersistentVolumeClaimSpec{
// 			VolumeName: "pvA",
// 		},
// 		Status: api.PersistentVolumeClaimStatus{
// 			Phase: api.ClaimBound,
// 		},
// 	}
//
// 	client := fake.NewSimpleClientset(pv, claim)
//
// 	plugMgr := volume.VolumePluginMgr{}
// 	plugMgr.InitPlugins(ProbeVolumePlugins(), volume.NewFakeVolumeHost("/tmp/fake", client, nil))
// 	plug, _ := plugMgr.FindPluginByName(hostPathPluginName)
//
// 	// readOnly bool is supplied by persistent-claim volume source when its builder creates other volumes
// 	spec := volume.NewSpecFromPersistentVolume(pv, true)
// 	pod := &api.Pod{ObjectMeta: api.ObjectMeta{UID: types.UID("poduid")}}
// 	builder, _ := plug.NewBuilder(spec, pod, volume.VolumeOptions{})
//
// 	if !builder.GetAttributes().ReadOnly {
// 		t.Errorf("Expected true for builder.IsReadOnly")
// 	}
// }
//
// // TestMetrics tests that MetricProvider methods return sane values.
// func TestMetrics(t *testing.T) {
// 	// Create an empty temp directory for the volume
// 	tmpDir, err := ioutil.TempDir(os.TempDir(), "host_path_test")
// 	if err != nil {
// 		t.Fatalf("Can't make a tmp dir: %v", err)
// 	}
// 	defer os.RemoveAll(tmpDir)
//
// 	plugMgr := volume.VolumePluginMgr{}
// 	plugMgr.InitPlugins(ProbeVolumePlugins(), volume.NewFakeVolumeHost(tmpDir, nil, nil))
//
// 	plug, err := plugMgr.FindPluginByName("kubernetes.io/host-path")
// 	if err != nil {
// 		t.Errorf("Can't find the plugin by name")
// 	}
// 	spec := &api.Volume{
// 		Name:         "vol1",
// 		VolumeSource: api.VolumeSource{HostPath: &api.HostPathVolumeSource{Path: tmpDir}},
// 	}
// 	pod := &api.Pod{ObjectMeta: api.ObjectMeta{UID: types.UID("poduid")}}
// 	builder, err := plug.NewBuilder(volume.NewSpecFromVolume(spec), pod, volume.VolumeOptions{})
// 	if err != nil {
// 		t.Errorf("Failed to make a new Builder: %v", err)
// 	}
//
// 	expectedEmptyDirUsage, err := volume.FindEmptyDirectoryUsageOnTmpfs()
// 	if err != nil {
// 		t.Errorf("Unexpected error finding expected empty directory usage on tmpfs: %v", err)
// 	}
//
// 	metrics, err := builder.GetMetrics()
// 	if err != nil {
// 		t.Errorf("Unexpected error when calling GetMetrics %v", err)
// 	}
// 	if e, a := expectedEmptyDirUsage.Value(), metrics.Used.Value(); e != a {
// 		t.Errorf("Unexpected value for empty directory; expected %v, got %v", e, a)
// 	}
// 	if metrics.Capacity.Value() <= 0 {
// 		t.Errorf("Expected Capacity to be greater than 0")
// 	}
// 	if metrics.Available.Value() <= 0 {
// 		t.Errorf("Expected Available to be greater than 0")
// 	}
// }
