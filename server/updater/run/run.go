package run

import (
	"fmt"

	"github.com/scitq/scitq/server/config"
	"github.com/scitq/scitq/server/updater/azure"
	//"github.com/scitq/scitq/server/updater/openstack"
)

func Run(cfg config.Config, providerCfg config.ProviderConfig) error {
	switch c := providerCfg.(type) {
	case *config.AzureConfig:
		return azure.Run(cfg, *c)
	//case *config.OpenstackConfig:
	//	return openstack.Run(cfg, *c)
	default:
		return fmt.Errorf("unsupported provider configuration type: %T", providerCfg)
	}
}
