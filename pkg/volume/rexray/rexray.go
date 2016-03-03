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
	"fmt"
	"os"

	gostrings "strings"

	"github.com/golang/glog"

	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/resource"
	"k8s.io/kubernetes/pkg/types"
	"k8s.io/kubernetes/pkg/util/mount"
	"k8s.io/kubernetes/pkg/util/strings"
	"k8s.io/kubernetes/pkg/volume"
)

func ProbeVolumePlugins() []volume.VolumePlugin {
	return []volume.VolumePlugin{&rexrayPlugin{nil}}
}

type rexrayPlugin struct {
	host volume.VolumeHost
}

var _ volume.VolumePlugin = &rexrayPlugin{}
var _ volume.ProvisionableVolumePlugin = &rexrayPlugin{}
var _ volume.PersistentVolumePlugin = &rexrayPlugin{}
var _ volume.DeletableVolumePlugin = &rexrayPlugin{}

const (
	rexrayPluginName = "kubernetes.io/rexray"
)

func (plugin *rexrayPlugin) Init(host volume.VolumeHost) error {
	plugin.host = host
	return nil
}

func (plugin *rexrayPlugin) Name() string {
	return rexrayPluginName
}

func (plugin *rexrayPlugin) CanSupport(spec *volume.Spec) bool {
	return (spec.PersistentVolume != nil && spec.PersistentVolume.Spec.RexRay != nil) ||
		(spec.Volume != nil && spec.Volume.RexRay != nil)
}

func (plugin *rexrayPlugin) GetAccessModes() []api.PersistentVolumeAccessMode {
	return []api.PersistentVolumeAccessMode{
		api.ReadWriteOnce,
	}
}

func (plugin *rexrayPlugin) NewBuilder(spec *volume.Spec, pod *api.Pod, _ volume.VolumeOptions) (volume.Builder, error) {
	mounter := mount.New()
	if spec.Volume != nil && spec.Volume.RexRay != nil {
		return &rexrayDiskBuilder{
			rexray: &rexray{
				podUID:        pod.UID,
				volumeName:    spec.Volume.RexRay.VolumeName,
				volumeID:      spec.Volume.RexRay.VolumeID,
				module:        spec.Volume.RexRay.Module,
				storageDriver: spec.Volume.RexRay.StorageDriver,
				mounter:       mounter,
				manager:       &RexRayDiskUtil{},
				plugin:        plugin,
			},
			readOnly: false,
		}, nil
	} else {
		return &rexrayDiskBuilder{
			rexray: &rexray{
				podUID:        pod.UID,
				volumeName:    spec.PersistentVolume.Spec.RexRay.VolumeName,
				volumeID:      spec.PersistentVolume.Spec.RexRay.VolumeID,
				module:        spec.PersistentVolume.Spec.RexRay.Module,
				storageDriver: spec.PersistentVolume.Spec.RexRay.StorageDriver,
				mounter:       mounter,
				manager:       &RexRayDiskUtil{},
				plugin:        plugin,
			},
			readOnly: spec.ReadOnly,
		}, nil
	}
}

type rexray struct {
	podUID        types.UID
	volumeName    string
	volumeID      string
	module        string
	storageDriver string
	mntPath       string
	mounter       mount.Interface
	plugin        *rexrayPlugin
	manager       rrManager
	volume.MetricsNil
}

type rrManager interface {
	// Attaches the disk to the kubelet's host machine.
	AttachAndMountDisk(b *rexrayDiskBuilder, globalPDPath string) error
	// Detaches the disk from the kubelet's host machine.
	DetachDisk(c *rexrayDiskCleaner) error
	// Creates a volume
	CreateVolume(provisioner *rexrayDiskProvisioner) (volumeID string, volumeSizeGB int, err error)
	// Deletes a volume
	DeleteVolume(deleter *rexrayDiskDeleter) error
}

func (r *rexray) GetPath() string {
	name := rexrayPluginName
	return r.plugin.host.GetPodVolumeDir(r.podUID, strings.EscapeQualifiedNameForDisk(name), r.volumeName)
}

func (r *rexray) GetName() string {
	return r.volumeName
}

func (r *rexray) GetModule() string {
	return r.module
}

type rexrayDiskBuilder struct {
	*rexray
	readOnly bool
}

var _ volume.Builder = &rexrayDiskBuilder{}

func (b *rexrayDiskBuilder) GetAttributes() volume.Attributes {
	return volume.Attributes{
		ReadOnly:        b.readOnly,
		Managed:         false,
		SupportsSELinux: true,
	}
}

func (b *rexrayDiskBuilder) SetUp(fsGroup *int64) error {
	return b.SetUpAt(b.volumeName, fsGroup)
}

func (b *rexrayDiskBuilder) detachDiskLogError() {
	mod, err := getRexRayModule(b.module)
	if err != nil {
		glog.Error(err)
	}

	err = mod.Volume.Unmount(b.volumeName, "")
	if err != nil {
		glog.Warningf("Failed to detach disk: %v (%v)", b.volumeName, err)
	}
}

func (b *rexrayDiskBuilder) SetUpAt(dir string, fsGroup *int64) error {

	if err := b.manager.AttachAndMountDisk(b, ""); err != nil {
		return err
	}

	path := b.GetPath()

	if err := os.MkdirAll(path, 0750); err != nil {
		b.detachDiskLogError()
		return err
	}

	options := []string{"bind"}
	if b.readOnly {
		options = append(options, "ro")
	}

	err := b.mounter.Mount(b.mntPath, path, "", options)
	if err != nil {
		return err
	}

	return nil
}

type rexrayDiskCleaner struct {
	*rexray
}

var _ volume.Cleaner = &rexrayDiskCleaner{}

func (plugin *rexrayPlugin) NewCleaner(volName string, podUID types.UID) (volume.Cleaner, error) {
	return &rexrayDiskCleaner{&rexray{
		podUID:     podUID,
		volumeName: volName,
		mounter:    mount.New(),
		manager:    &RexRayDiskUtil{},
		plugin:     plugin,
	}}, nil
}

func (c *rexrayDiskCleaner) TearDown() error {
	return c.TearDownAt(c.GetPath())
}

func (c *rexrayDiskCleaner) TearDownAt(dir string) error {

	notMnt, err := c.mounter.IsLikelyNotMountPoint(dir)
	if err != nil {
		return err
	}
	if notMnt {
		return os.Remove(dir)
	}

	refs, err := mount.GetMountRefs(c.mounter, dir)
	if err != nil {
		return err
	}
	// Unmount the bind-mount inside this pod
	if err := c.mounter.Unmount(dir); err != nil {
		return err
	}

	if len(refs) == 1 {
		if err := c.manager.DetachDisk(c); err != nil {
			return err
		}
	}
	notMnt, mntErr := c.mounter.IsLikelyNotMountPoint(dir)
	if mntErr != nil {
		glog.Errorf("IsLikelyNotMountPoint check failed: %v", mntErr)
		return err
	}
	if notMnt {
		if err := os.Remove(dir); err != nil {
			return err
		}
	}
	return nil
}

type rexrayDiskProvisioner struct {
	*rexray
	options volume.VolumeOptions
}

var _ volume.Provisioner = &rexrayDiskProvisioner{}

func (plugin *rexrayPlugin) NewProvisioner(options volume.VolumeOptions) (volume.Provisioner, error) {
	if len(options.AccessModes) == 0 {
		options.AccessModes = plugin.GetAccessModes()
	}
	return &rexrayDiskProvisioner{
		rexray: &rexray{
			plugin:  plugin,
			manager: &RexRayDiskUtil{},
		},
		options: options,
	}, nil
}

func setVal(key string, haystack map[string]string, outVal *string) {
	for key, val := range haystack {
		kl := gostrings.ToLower(key)
		haystack[kl] = val
	}
	key = gostrings.ToLower(key)
	if val, ok := haystack[key]; ok {
		*outVal = val
	}
}

func (c *rexrayDiskProvisioner) Provision(pv *api.PersistentVolume) error {
	glog.V(4).Infof("Provisioning disk: %+v", pv)

	if val, ok := pv.Annotations["volume.alpha.kubernetes.io/storage-class"]; ok {
		c.module = val
	} else {
		c.module = "default-docker"
	}

	volName, volSize, err := c.manager.CreateVolume(c)
	if err != nil {
		return err
	}

	pv.Spec.PersistentVolumeSource.RexRay.VolumeID = c.volumeID
	pv.Spec.PersistentVolumeSource.RexRay.StorageDriver = c.storageDriver
	pv.Spec.PersistentVolumeSource.RexRay.VolumeName = volName
	pv.Spec.PersistentVolumeSource.RexRay.Module = c.module

	pv.Spec.Capacity = api.ResourceList{
		api.ResourceName(api.ResourceStorage): resource.MustParse(fmt.Sprintf("%dGi", volSize)),
	}
	return nil
}

func (c *rexrayDiskProvisioner) NewPersistentVolumeTemplate() (*api.PersistentVolume, error) {
	return &api.PersistentVolume{
		ObjectMeta: api.ObjectMeta{
			GenerateName: "rexray-",
			Labels:       map[string]string{},
			Annotations: map[string]string{
				"kubernetes.io/createdby": "rexray-dynamic-provisioner",
			},
		},
		Spec: api.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: c.options.PersistentVolumeReclaimPolicy,
			AccessModes:                   c.options.AccessModes,
			Capacity: api.ResourceList{
				api.ResourceName(api.ResourceStorage): c.options.Capacity,
			},
			PersistentVolumeSource: api.PersistentVolumeSource{
				RexRay: &api.RexRayVolumeSource{
					VolumeName:    "dummy",
					VolumeID:      "",
					Module:        "",
					StorageDriver: "",
				},
			},
		},
	}, nil
}

type rexrayDiskDeleter struct {
	*rexray
}

var _ volume.Deleter = &rexrayDiskDeleter{}

func (plugin *rexrayPlugin) NewDeleter(spec *volume.Spec) (volume.Deleter, error) {
	return &rexrayDiskDeleter{
		rexray: &rexray{
			volumeName: spec.PersistentVolume.Spec.PersistentVolumeSource.RexRay.VolumeName,
			volumeID:   spec.PersistentVolume.Spec.PersistentVolumeSource.RexRay.VolumeID,
			module:     spec.PersistentVolume.Spec.PersistentVolumeSource.RexRay.Module,
			plugin:     plugin,
			manager:    &RexRayDiskUtil{},
		}}, nil
}

func (d *rexrayDiskDeleter) Delete() error {
	glog.V(4).Infof("Deleting disk: %+v", d.rexray)

	if err := d.manager.DeleteVolume(d); err != nil {
		return err
	}

	return nil
}
