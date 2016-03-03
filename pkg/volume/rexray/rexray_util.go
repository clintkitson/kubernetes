package rexray

import (
	"errors"
	"fmt"
	"strconv"
	"sync"

	"github.com/emccode/rexray/core"
	"github.com/golang/glog"
	"k8s.io/kubernetes/pkg/cloudprovider"
	crr "k8s.io/kubernetes/pkg/cloudprovider/providers/rexray"
	"k8s.io/kubernetes/pkg/util/keymutex"
	"k8s.io/kubernetes/pkg/volume"
)

type RexRayDiskUtil struct{}

var attachDetachMutex = keymutex.NewKeyMutex()
var volPodUIDModule map[string]string
var volPod sync.Mutex
var cachedRexRay *crr.RexRay

func init() {
	volPodUIDModule = make(map[string]string)
}

func getRexRay() (*crr.RexRay, error) {
	if cachedRexRay != nil {
		return cachedRexRay, nil
	}

	rrCloudProvider, err := cloudprovider.GetCloudProvider("rexray", nil)
	if err != nil || rrCloudProvider == nil {
		return nil, err
	}

	cachedRexRay = rrCloudProvider.(*crr.RexRay)

	return cachedRexRay, nil
}

func getRexRayModule(name string) (*core.RexRay, error) {
	rr, err := getRexRay()
	if err != nil {
		return nil, err
	}

	if mod, ok := rr.Modules[name]; ok {
		return mod, nil
	}

	return nil, errors.New(fmt.Sprintf("could not find module name: %s", name))
}

func (b *rexrayDiskBuilder) setVolModuleCache() {
	volPod.Lock()
	defer volPod.Unlock()
	glog.V(4).Infof("Caching module (%s) for volume (%s) at pod (%s)\r\n", b.module, b.volumeName, b.podUID)

	vp := fmt.Sprintf(b.volumeName, b.podUID)
	volPodUIDModule[vp] = b.module
}

func (c *rexrayDiskCleaner) getVolModuleCache() (string, error) {
	volPod.Lock()
	defer volPod.Unlock()
	glog.V(4).Infof("Looking up module for volume (%s) at pod (%s)\r\n", c.volumeName, c.podUID)

	vp := fmt.Sprintf(c.volumeName, c.podUID)

	if val, ok := volPodUIDModule[vp]; ok {
		glog.V(4).Infof("Found module (%s) for volume (%s) at pod (%s)\r\n", val, c.volumeName, c.podUID)
		return val, nil
	}

	return "", errors.New("volume and pod id not recognized")
}

func (diskUtil *RexRayDiskUtil) AttachAndMountDisk(b *rexrayDiskBuilder, globalPDPath string) error {
	glog.V(4).Infof("AttachAndMountDisk(...) for volumeName %q\r\n", b.volumeName)
	attachDetachMutex.LockKey(b.volumeName)
	defer attachDetachMutex.UnlockKey(b.volumeName)

	mod, err := getRexRayModule(b.module)
	if err != nil {
		return err
	}

	vol, err := mod.Volume.Get(b.volumeName)
	if err != nil {
		return err
	}

	b.setVolModuleCache()

	if mountPoint, ok := vol["Mountpoint"]; ok {
		glog.V(4).Infof("PersistentDisk mounted already: %s to %s", b.volumeName, mountPoint)
		b.mntPath = mountPoint
		return nil
	}

	mntPath, err := mod.Volume.Mount(b.volumeName, "", false, "", false)
	if err != nil {
		return err
	}

	b.mntPath = mntPath

	return nil
}

func (util *RexRayDiskUtil) DetachDisk(c *rexrayDiskCleaner) error {
	glog.V(4).Infof("DetachDisk(...) for volumeName %q\r\n", c.volumeName)
	attachDetachMutex.LockKey(c.volumeName)
	defer attachDetachMutex.UnlockKey(c.volumeName)

	module, err := c.getVolModuleCache()
	if err != nil {
		return err
	}

	mod, err := getRexRayModule(module)
	if err != nil {
		return err
	}

	if err := mod.Volume.Unmount(c.volumeName, ""); err != nil {
		return err
	}

	return nil
}

func (gceutil *RexRayDiskUtil) CreateVolume(c *rexrayDiskProvisioner) (string, int, error) {

	name := volume.GenerateVolumeName(c.options.ClusterName, c.options.PVName, 31)
	glog.V(4).Infof("CreateVolume(...) for volumeName %q\r\n", name)

	mod, err := getRexRayModule(c.module)
	if err != nil {
		return "", 0, err
	}

	volSizeBytes := c.options.Capacity.Value()
	volSizeGB := int(volume.RoundUpSize(volSizeBytes, 1024*1024*1024))

	vol, err := mod.Storage.CreateVolume(false, name,
		"", "", "", 0, int64(volSizeGB), "")
	if err != nil {
		return "", 0, err
	}

	c.volumeID = vol.VolumeID
	c.storageDriver = mod.Storage.Name()

	size, _ := strconv.Atoi(vol.Size)

	return vol.Name, size, nil
}

func (util *RexRayDiskUtil) DeleteVolume(d *rexrayDiskDeleter) error {
	glog.V(4).Infof("DeleteVolume(...) for volumeName %q\r\n", d.volumeName)

	mod, err := getRexRayModule(d.module)
	if err != nil {
		return err
	}

	return mod.Storage.RemoveVolume(d.rexray.volumeID)
}
