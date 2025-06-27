package azure

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/gmtsciencedev/scitq2/server/config"
)

// AzureProvider holds global configuration for Azure.
type AzureProvider struct {
	az                  config.AzureConfig
	sshPublicKeyData    string
	cfg                 config.Config
	LocalWorkspaceRoots map[string]string // Local workspace roots for different regions
}

// expandPath expands a leading ~ in a file path to the user's home directory.
func expandPath(path string) (string, error) {
	if len(path) > 0 && path[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, path[1:]), nil
	}
	return path, nil
}

func readFileContent(filePath string) (string, error) {
	fullFilePath, err := expandPath(filePath)
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(fullFilePath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// NewAzureProvider creates an AzureProvider with the given configuration.
func New(cfg config.AzureConfig, scitqCfg config.Config) *AzureProvider {

	sshKeyContent, err := readFileContent(cfg.SSHPublicKey)
	if err != nil {
		log.Fatalf("Error reading SSH key file: %v\n", err)
	}

	return &AzureProvider{
		az:               cfg,
		sshPublicKeyData: sshKeyContent,
		cfg:              scitqCfg,
	}
}

func (ap *AzureProvider) resourceGroupSuffix() string {
	return "_" + ap.cfg.Scitq.ServerName + "_group"
}

// resourceGroupName derives the resource group name from the worker name.
func (ap *AzureProvider) resourceGroupName(workerName string) string {
	return workerName + ap.resourceGroupSuffix()
}

// retry executes fn up to 'attempts' times with exponential backoff.
func retry(fn func() error, attempts int, delay time.Duration) error {
	var err error
	for i := 0; i < attempts; i++ {
		if err = fn(); err == nil {
			return nil
		}
		log.Printf("Error during processing: %v, retrying", err)
		time.Sleep(delay * time.Duration(i+1))
	}
	return fmt.Errorf("after %d attempts, last error: %w", attempts, err)
}

// createClient initializes an Azure client with retry logic.
func createClient[T any](newClientFunc func() (*T, error)) (*T, error) {
	var client *T
	err := retry(func() error {
		var err error
		client, err = newClientFunc()
		return err
	}, 3, 5*time.Second)
	return client, err
}

// createVNetAndSubnet creates a VNet and subnet with retry logic.
func (ap *AzureProvider) createVNetAndSubnet(ctx context.Context, cred *azidentity.ClientSecretCredential, rgName, vnetName, subnetName, location string) (string, error) {
	vnetClient, err := createClient(func() (*armnetwork.VirtualNetworksClient, error) {
		return armnetwork.NewVirtualNetworksClient(ap.az.SubscriptionID, cred, nil)
	})
	if err != nil {
		return "", err
	}
	subnetClient, err := createClient(func() (*armnetwork.SubnetsClient, error) {
		return armnetwork.NewSubnetsClient(ap.az.SubscriptionID, cred, nil)
	})
	if err != nil {
		return "", err
	}

	var subnetID string
	err = retry(func() error {
		vnetParams := armnetwork.VirtualNetwork{
			Location: to.Ptr(location),
			Properties: &armnetwork.VirtualNetworkPropertiesFormat{
				AddressSpace: &armnetwork.AddressSpace{
					AddressPrefixes: []*string{to.Ptr("10.0.0.0/16")},
				},
			},
		}
		// Check if the VNet already exists.
		_, err = vnetClient.Get(ctx, rgName, vnetName, nil)
		if err != nil {
			// Assume error means "not found" and try to create.
			vnetPoller, err := vnetClient.BeginCreateOrUpdate(ctx, rgName, vnetName, vnetParams, nil)
			if err != nil {
				return fmt.Errorf("failed to begin VNet creation: %w", err)
			}
			_, err = vnetPoller.PollUntilDone(ctx, nil)
			if err != nil {
				return fmt.Errorf("failed to create VNet: %w", err)
			}
		} else {
			log.Printf("VNet %s already exists in resource group %s", vnetName, rgName)
		}
		//vnetPoller, err := vnetClient.BeginCreateOrUpdate(ctx, rgName, vnetName, vnetParams, nil)
		//if err != nil {
		//	return err
		//}
		//_, err = vnetPoller.PollUntilDone(ctx, nil)
		//if err != nil {
		//	return err
		//}

		subnetParams := armnetwork.Subnet{
			Properties: &armnetwork.SubnetPropertiesFormat{
				AddressPrefix: to.Ptr("10.0.0.0/24"),
			},
		}
		subnetPoller, err := subnetClient.BeginCreateOrUpdate(ctx, rgName, vnetName, subnetName, subnetParams, nil)
		if err != nil {
			return err
		}
		subnetResp, err := subnetPoller.PollUntilDone(ctx, nil)
		if err != nil {
			return err
		}
		subnetID = *subnetResp.ID
		return nil
	}, 3, 5*time.Second)
	return subnetID, err
}

// Create provisions a new VM for a worker with retry logic and returns the IP address.
func (ap *AzureProvider) Create(workerName, flavor, location string) (string, error) {
	var ipAddress string
	var pubIPID string

	log.Printf("Creating VM for worker %s", workerName)

	err := retry(func() error {
		vmName := workerName
		rgName := ap.resourceGroupName(workerName)

		// Prepare the cloud-init script.
		cloudInit := fmt.Sprintf(`#cloud-config
runcmd:
  - curl -ksSL https://%s/scitq-client?token=%s -o /usr/local/bin/scitq-client
  - chmod a+x /usr/local/bin/scitq-client
  - /usr/local/bin/scitq-client -server %s:%d -install -docker "%s:%s" -swap "%f" -token "%s"`,
			ap.cfg.Scitq.ServerFQDN, ap.cfg.Scitq.ClientDownloadToken,
			ap.cfg.Scitq.ServerFQDN, ap.cfg.Scitq.Port,
			ap.cfg.Scitq.DockerRegistry, ap.cfg.Scitq.DockerAuthentication,
			ap.cfg.Scitq.SwapProportion, ap.cfg.Scitq.WorkerToken)
		customData := base64.StdEncoding.EncodeToString([]byte(cloudInit))

		// Create credential.
		cred, err := azidentity.NewClientSecretCredential(ap.az.TenantID, ap.az.ClientID, ap.az.ClientSecret, nil)
		if err != nil {
			return fmt.Errorf("failed to create credential: %w", err)
		}
		ctx := context.Background()

		// Create or update the resource group.
		rgClient, err := createClient(func() (*armresources.ResourceGroupsClient, error) {
			return armresources.NewResourceGroupsClient(ap.az.SubscriptionID, cred, nil)
		})
		if err != nil {
			return err
		}
		_, err = rgClient.CreateOrUpdate(ctx, rgName, armresources.ResourceGroup{
			Location: to.Ptr(location),
		}, nil)
		if err != nil {
			return err
		}

		// Create a new NIC and public IP.
		var nicID string
		nicID, pubIPID, err = ap.createDefaultNIC(ctx, cred, rgName, workerName, location)
		if err != nil {
			return err
		}

		// Create VM client.
		vmClient, err := createClient(func() (*armcompute.VirtualMachinesClient, error) {
			return armcompute.NewVirtualMachinesClient(ap.az.SubscriptionID, cred, nil)
		})
		if err != nil {
			return err
		}

		// Define VM parameters.
		vmParameters := armcompute.VirtualMachine{
			Location: to.Ptr(location),
			Tags: map[string]*string{
				"scitq":      to.Ptr(ap.cfg.Scitq.ServerName),
				"workerName": to.Ptr(workerName),
			},
			Properties: &armcompute.VirtualMachineProperties{
				HardwareProfile: &armcompute.HardwareProfile{
					VMSize: to.Ptr(armcompute.VirtualMachineSizeTypes(flavor)),
				},
				StorageProfile: &armcompute.StorageProfile{
					ImageReference: &armcompute.ImageReference{
						Publisher: to.Ptr(ap.az.Image.Publisher),
						Offer:     to.Ptr(ap.az.Image.Offer),
						SKU:       to.Ptr(ap.az.Image.Sku),
						Version:   to.Ptr(ap.az.Image.Version),
					},
				},
				OSProfile: &armcompute.OSProfile{
					ComputerName:  to.Ptr(vmName),
					CustomData:    to.Ptr(customData),
					AdminUsername: to.Ptr(ap.az.Username),
					LinuxConfiguration: &armcompute.LinuxConfiguration{
						DisablePasswordAuthentication: to.Ptr(true),
						SSH: &armcompute.SSHConfiguration{
							PublicKeys: []*armcompute.SSHPublicKey{
								{
									Path:    to.Ptr(fmt.Sprintf("/home/%s/.ssh/authorized_keys", ap.az.Username)),
									KeyData: to.Ptr(ap.sshPublicKeyData),
								},
							},
						},
					},
				},
				NetworkProfile: &armcompute.NetworkProfile{
					NetworkInterfaces: []*armcompute.NetworkInterfaceReference{
						{
							ID: to.Ptr(nicID),
							Properties: &armcompute.NetworkInterfaceReferenceProperties{
								Primary: to.Ptr(true),
							},
						},
					},
				},
			},
		}
		if ap.az.UseSpot {
			vmParameters.Properties.Priority = to.Ptr(armcompute.VirtualMachinePriorityTypesSpot)
			vmParameters.Properties.EvictionPolicy = to.Ptr(armcompute.VirtualMachineEvictionPolicyTypesDeallocate)
		}

		// Create the VM.
		poller, err := vmClient.BeginCreateOrUpdate(ctx, rgName, vmName, vmParameters, nil)
		if err != nil {
			return err
		}
		_, err = poller.PollUntilDone(ctx, nil)
		if err != nil {
			return err
		}

		// Retrieve the IP address using the stored public IP ID.
		ipAddress, err = ap.getIPAddressFromPubIPID(ctx, cred, rgName, pubIPID)
		if err != nil {
			return err
		}

		return nil
	}, 3, 5*time.Second)
	if err != nil {
		return "", err
	}

	log.Printf("VM %s created successfully with IP address %s", workerName, ipAddress)
	return ipAddress, nil
}

// createDefaultNICWithPubIP creates a new NIC with a public IP and returns both IDs.
func (ap *AzureProvider) createDefaultNIC(ctx context.Context, cred *azidentity.ClientSecretCredential, rgName, workerName, location string) (string, string, error) {
	vnetName := workerName + "-vnet"
	subnetName := workerName + "-subnet"
	nicName := workerName + "-nic"

	subnetID, err := ap.createVNetAndSubnet(ctx, cred, rgName, vnetName, subnetName, location)
	if err != nil {
		return "", "", err
	}

	pubIPClient, err := createClient(func() (*armnetwork.PublicIPAddressesClient, error) {
		return armnetwork.NewPublicIPAddressesClient(ap.az.SubscriptionID, cred, nil)
	})
	if err != nil {
		return "", "", err
	}
	nicClient, err := createClient(func() (*armnetwork.InterfacesClient, error) {
		return armnetwork.NewInterfacesClient(ap.az.SubscriptionID, cred, nil)
	})
	if err != nil {
		return "", "", err
	}

	var nicID, pubIPID string
	err = retry(func() error {
		pubIPParams := armnetwork.PublicIPAddress{
			Location: to.Ptr(location),
			Properties: &armnetwork.PublicIPAddressPropertiesFormat{
				PublicIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodDynamic),
			},
		}
		pubIPPoller, err := pubIPClient.BeginCreateOrUpdate(ctx, rgName, nicName+"-pip", pubIPParams, nil)
		if err != nil {
			return err
		}
		pubIPResp, err := pubIPPoller.PollUntilDone(ctx, nil)
		if err != nil {
			return err
		}
		pubIPID = *pubIPResp.ID

		nicParams := armnetwork.Interface{
			Location: to.Ptr(location),
			Properties: &armnetwork.InterfacePropertiesFormat{
				IPConfigurations: []*armnetwork.InterfaceIPConfiguration{
					{
						Name: to.Ptr("ipconfig1"),
						Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
							Subnet: &armnetwork.Subnet{
								ID: to.Ptr(subnetID),
							},
							PublicIPAddress: &armnetwork.PublicIPAddress{
								ID: to.Ptr(pubIPID),
							},
						},
					},
				},
			},
		}
		nicPoller, err := nicClient.BeginCreateOrUpdate(ctx, rgName, nicName, nicParams, nil)
		if err != nil {
			return err
		}
		nicResp, err := nicPoller.PollUntilDone(ctx, nil)
		if err != nil {
			return err
		}
		nicID = *nicResp.ID
		return nil
	}, 3, 5*time.Second)
	return nicID, pubIPID, err
}

// getIPAddressFromPubIPID retrieves the IP address from the public IP ID.
func (ap *AzureProvider) getIPAddressFromPubIPID(ctx context.Context, cred *azidentity.ClientSecretCredential, rgName, pubIPID string) (string, error) {
	pubIPClient, err := createClient(func() (*armnetwork.PublicIPAddressesClient, error) {
		return armnetwork.NewPublicIPAddressesClient(ap.az.SubscriptionID, cred, nil)
	})
	if err != nil {
		return "", err
	}

	pubIPName := pubIPID[strings.LastIndex(pubIPID, "/")+1:]
	pubIPResp, err := pubIPClient.Get(ctx, rgName, pubIPName, nil)
	if err != nil {
		return "", err
	}

	if pubIPResp.Properties == nil || pubIPResp.Properties.IPAddress == nil {
		return "", fmt.Errorf("no IP address found for public IP ID %s", pubIPID)
	}

	return *pubIPResp.Properties.IPAddress, nil
}

// List returns the worker names and IP addresses for VMs created by scitq.
func (ap *AzureProvider) List() (map[string]string, error) {
	cred, err := azidentity.NewClientSecretCredential(ap.az.TenantID, ap.az.ClientID, ap.az.ClientSecret, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create credential: %w", err)
	}
	ctx := context.Background()

	rgClient, err := createClient(func() (*armresources.ResourceGroupsClient, error) {
		return armresources.NewResourceGroupsClient(ap.az.SubscriptionID, cred, nil)
	})
	if err != nil {
		return nil, err
	}
	pager := rgClient.NewListPager(nil)
	var workers map[string]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get next page of resource groups: %w", err)
		}
		for _, rg := range page.ResourceGroupListResult.Value {
			if rg.Name != nil && strings.HasSuffix(*rg.Name, ap.resourceGroupSuffix()) {
				workerName := strings.TrimSuffix(*rg.Name, ap.resourceGroupSuffix())
				vmClient, err := createClient(func() (*armcompute.VirtualMachinesClient, error) {
					return armcompute.NewVirtualMachinesClient(ap.az.SubscriptionID, cred, nil)
				})
				if err != nil {
					return nil, err
				}
				vmPager := vmClient.NewListPager(*rg.Name, nil)
				for vmPager.More() {
					page, err := vmPager.NextPage(ctx)
					if err != nil {
						return nil, fmt.Errorf("failed to get next page of VMs: %w", err)
					}
					for _, vm := range page.VirtualMachineListResult.Value {
						if vm.Tags != nil && vm.Tags["scitq"] != nil && *vm.Tags["scitq"] == ap.cfg.Scitq.ServerName {
							// Get the network interface ID
							if vm.Properties != nil && vm.Properties.NetworkProfile != nil && len(vm.Properties.NetworkProfile.NetworkInterfaces) > 0 {
								networkInterfaceID := *vm.Properties.NetworkProfile.NetworkInterfaces[0].ID

								// Extract the network interface name from the ID
								parts := strings.Split(networkInterfaceID, "/")
								networkInterfaceName := parts[len(parts)-1]

								// Get the network interface client
								nicClient, err := armnetwork.NewInterfacesClient(ap.az.SubscriptionID, cred, nil)
								if err != nil {
									return nil, fmt.Errorf("failed to create network interfaces client: %w", err)
								}
								nic, err := nicClient.Get(ctx, *rg.Name, networkInterfaceName, nil)
								if err != nil {
									return nil, fmt.Errorf("failed to get network interface: %w", err)
								}

								// Get the IP address from the network interface
								if nic.Properties != nil && len(nic.Properties.IPConfigurations) > 0 {
									ipAddress := *nic.Properties.IPConfigurations[0].Properties.PrivateIPAddress
									if workers == nil {
										workers = make(map[string]string)
									}
									workers[workerName] = ipAddress
								}
							}
						}
					}
				}
			}
		}
	}
	return workers, nil
}

// Delete removes the VM and its resource group for the given worker with retry logic.
func (ap *AzureProvider) Delete(workerName string) error {
	vmName := workerName
	rgName := ap.resourceGroupName(workerName)

	cred, err := azidentity.NewClientSecretCredential(ap.az.TenantID, ap.az.ClientID, ap.az.ClientSecret, nil)
	if err != nil {
		return fmt.Errorf("failed to create credential: %w", err)
	}
	ctx := context.Background()

	// Create the VM client.
	vmClient, err := createClient(func() (*armcompute.VirtualMachinesClient, error) {
		return armcompute.NewVirtualMachinesClient(ap.az.SubscriptionID, cred, nil)
	})
	if err != nil {
		return err
	}

	// Delete the VM with retries.
	err = retry(func() error {
		poller, err := vmClient.BeginDelete(ctx, rgName, vmName, nil)
		if err != nil {
			var respErr *azcore.ResponseError
			if errors.As(err, &respErr) && respErr.StatusCode == 404 {
				log.Printf("VM %s already deleted or not found, continuing: %v", vmName, respErr.Error())
				return nil
			}
			return fmt.Errorf("failed to begin VM deletion: %w", err)
		}
		_, err = poller.PollUntilDone(ctx, nil)
		if err != nil {
			var respErr *azcore.ResponseError
			if errors.As(err, &respErr) && respErr.StatusCode == 404 {
				log.Printf("VM %s deletion polling got 404, assuming already gone: %v", vmName, respErr.Error())
				return nil
			}
		}
		return err
	}, 3, 5*time.Second)
	if err != nil {
		return fmt.Errorf("failed to delete VM after retries: %w", err)
	}
	log.Printf("VM %s deleted successfully", vmName)

	// Create the Resource Group client.
	rgClient, err := createClient(func() (*armresources.ResourceGroupsClient, error) {
		return armresources.NewResourceGroupsClient(ap.az.SubscriptionID, cred, nil)
	})
	if err != nil {
		return err
	}

	// Delete the resource group with retries.
	err = retry(func() error {
		rgPoller, err := rgClient.BeginDelete(ctx, rgName, nil)
		if err != nil {
			var respErr *azcore.ResponseError
			if errors.As(err, &respErr) && respErr.StatusCode == 404 {
				log.Printf("Resource group %s already deleted or not found, continuing: %v", rgName, respErr.Error())
				return nil
			}
			return fmt.Errorf("failed to begin deletion of resource group: %w", err)
		}
		_, err = rgPoller.PollUntilDone(ctx, nil)
		if err != nil {
			var respErr *azcore.ResponseError
			if errors.As(err, &respErr) && respErr.StatusCode == 404 {
				log.Printf("Resource group %s deletion polling got 404, assuming already gone: %v", rgName, respErr.Error())
				return nil
			}
		}
		return err
	}, 3, 5*time.Second)
	if err != nil {
		return fmt.Errorf("failed to delete resource group after retries: %w", err)
	}
	log.Printf("Resource group %s deleted successfully", rgName)
	return nil
}

// Restart restarts the VM for the given worker with retry logic.
func (ap *AzureProvider) Restart(workerName string) error {
	vmName := workerName
	rgName := ap.resourceGroupName(workerName)

	cred, err := azidentity.NewClientSecretCredential(ap.az.TenantID, ap.az.ClientID, ap.az.ClientSecret, nil)
	if err != nil {
		return fmt.Errorf("failed to create credential: %w", err)
	}
	ctx := context.Background()

	vmClient, err := createClient(func() (*armcompute.VirtualMachinesClient, error) {
		return armcompute.NewVirtualMachinesClient(ap.az.SubscriptionID, cred, nil)
	})
	if err != nil {
		return err
	}

	// Wrap the restart operation in a retry loop.
	err = retry(func() error {
		poller, err := vmClient.BeginRestart(ctx, rgName, vmName, nil)
		if err != nil {
			return fmt.Errorf("failed to begin VM restart: %w", err)
		}
		_, err = poller.PollUntilDone(ctx, nil)
		return err
	}, 3, 5*time.Second)
	if err != nil {
		return fmt.Errorf("failed to restart VM after retries: %w", err)
	}
	log.Printf("VM %s restarted successfully", vmName)
	return nil
}

func (a *AzureProvider) GetWorkspaceRoot(region string) (string, bool) {
	if a.LocalWorkspaceRoots == nil {
		return "", false
	}
	if root, ok := a.LocalWorkspaceRoots[region]; ok {
		return root, true
	}
	if root, ok := a.LocalWorkspaceRoots["*"]; ok {
		return root, true
	}
	return "", false
}
