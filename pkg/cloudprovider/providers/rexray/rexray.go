package rexray

import (
	"fmt"
	"io"
	"strings"

	"github.com/akutz/gofig"
	"github.com/akutz/goof"
	"github.com/emccode/rexray/core"
	_ "github.com/emccode/rexray/drivers"
	"k8s.io/kubernetes/pkg/cloudprovider"
)

const ProviderName = "rexray"
const DefaultModule = "default-docker"

type RexRay struct {
	Modules map[string]*core.RexRay
}

func init() {
	cloudprovider.RegisterCloudProvider(ProviderName, func(config io.Reader) (cloudprovider.Interface, error) {
		return newRexRay()
	})
}

func newRexRay() (*RexRay, error) {
	rr := &RexRay{}

	rr.Modules = make(map[string]*core.RexRay)
	cfg := gofig.New()

	modsc := cfg.Get("rexray.modules")
	modMap, ok := modsc.(map[string]interface{})
	if ok && len(modMap) > 0 {
		for name := range modMap {
			name = strings.ToLower(name)
			scope := fmt.Sprintf("rexray.modules.%s", name)
			sc := cfg.Scope(scope)
			rr.Modules[name] = core.New(sc)
			rr.Modules[name].Context = name
		}
	} else {
		rr.Modules[DefaultModule] = core.New(cfg)
		rr.Modules[DefaultModule].Context = DefaultModule
	}

	for _, mod := range rr.Modules {
		if err := mod.InitDrivers(); err != nil {
			return nil, goof.WithFieldsE(goof.Fields{
				"m": mod,
			}, "error initializing drivers", err)
		}
	}
	return rr, nil
}

func (rr *RexRay) LoadBalancer() (cloudprovider.LoadBalancer, bool) {
	return nil, false
}

func (rr *RexRay) Instances() (cloudprovider.Instances, bool) {
	return nil, false
}

func (rr *RexRay) Zones() (cloudprovider.Zones, bool) {
	return nil, false
}

func (rr *RexRay) Clusters() (cloudprovider.Clusters, bool) {
	return rr, false
}

func (rr *RexRay) Routes() (cloudprovider.Routes, bool) {
	return nil, false
}

func (rr *RexRay) ProviderName() string {
	return ProviderName
}

func (rr *RexRay) ScrubDNS(nameservers, searches []string) (nsOut, srchOut []string) {
	return nil, nil
}

func (rr *RexRay) ListClusters() ([]string, error) {
	return nil, nil
}

func (rr *RexRay) Master(clusterName string) (string, error) {
	return "", nil
}
