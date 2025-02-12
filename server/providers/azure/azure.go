package azure

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

// AzureProvider holds global configuration for Azure.
type AzureProvider struct {
	SubscriptionID     string
	ClientID           string
	ClientSecret       string
	TenantID           string
	DefaultLocation    string
	ScitqServerTag     string
	ScitqServerAddress string
	ScitqServerPort    int
}

// NewAzureProviderFromEnv creates an AzureProvider from environment variables.
// Probably a very bad idea
func NewAzureProviderFromEnv() *AzureProvider {
	serverPort := strings.Split(os.Getenv("SCITQ_SERVER"), ":")
	server := serverPort[0]
	port, err := strconv.Atoi(serverPort[1])
	if err != nil {
		port = 50051
	}

	return &AzureProvider{
		SubscriptionID:     os.Getenv("AZURE_SUBSCRIPTION_ID"),
		ClientID:           os.Getenv("AZURE_CLIENT_ID"),
		ClientSecret:       os.Getenv("AZURE_SECRET"),
		TenantID:           os.Getenv("AZURE_TENANT"),
		DefaultLocation:    os.Getenv("AZURE_LOCATION"),
		ScitqServerTag:     "scitq",
		ScitqServerAddress: server,
		ScitqServerPort:    port,
	}
}

func (ap *AzureProvider) resourceGroupSuffix() string {
	return "_" + ap.ScitqServerTag + "_group"
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
		return armnetwork.NewVirtualNetworksClient(ap.SubscriptionID, cred, nil)
	})
	if err != nil {
		return "", err
	}
	subnetClient, err := createClient(func() (*armnetwork.SubnetsClient, error) {
		return armnetwork.NewSubnetsClient(ap.SubscriptionID, cred, nil)
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
		vnetPoller, err := vnetClient.BeginCreateOrUpdate(ctx, rgName, vnetName, vnetParams, nil)
		if err != nil {
			return err
		}
		_, err = vnetPoller.PollUntilDone(ctx, nil)
		if err != nil {
			return err
		}

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
func (ap *AzureProvider) Create(workerName, flavor string, useSpot bool, sshPublicKey, publisher, offer, sku, version, location string) (string, error) {
	var ipAddress string
	var pubIPID string

	err := retry(func() error {
		vmName := workerName
		rgName := ap.resourceGroupName(workerName)

		// Prepare the cloud-init script.
		cloudInit := fmt.Sprintf(`#cloud-config
runcmd:
  - curl -sSL https://%s/scitq-client -o /usr/local/bin/scitq-client
  - /usr/local/bin/scitq-client --server %s:%d --install`,
			ap.ScitqServerAddress, ap.ScitqServerAddress, ap.ScitqServerPort)
		customData := base64.StdEncoding.EncodeToString([]byte(cloudInit))

		// Create credential.
		cred, err := azidentity.NewClientSecretCredential(ap.TenantID, ap.ClientID, ap.ClientSecret, nil)
		if err != nil {
			return fmt.Errorf("failed to create credential: %w", err)
		}
		ctx := context.Background()

		// Create or update the resource group.
		rgClient, err := createClient(func() (*armresources.ResourceGroupsClient, error) {
			return armresources.NewResourceGroupsClient(ap.SubscriptionID, cred, nil)
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
			return armcompute.NewVirtualMachinesClient(ap.SubscriptionID, cred, nil)
		})
		if err != nil {
			return err
		}

		// Define VM parameters.
		vmParameters := armcompute.VirtualMachine{
			Location: to.Ptr(location),
			Tags: map[string]*string{
				"scitq":      to.Ptr(ap.ScitqServerTag),
				"workerName": to.Ptr(workerName),
			},
			Properties: &armcompute.VirtualMachineProperties{
				HardwareProfile: &armcompute.HardwareProfile{
					VMSize: to.Ptr(armcompute.VirtualMachineSizeTypes(flavor)),
				},
				StorageProfile: &armcompute.StorageProfile{
					ImageReference: &armcompute.ImageReference{
						Publisher: to.Ptr(publisher),
						Offer:     to.Ptr(offer),
						SKU:       to.Ptr(sku),
						Version:   to.Ptr(version),
					},
				},
				OSProfile: &armcompute.OSProfile{
					ComputerName:  to.Ptr(vmName),
					CustomData:    to.Ptr(customData),
					AdminUsername: to.Ptr("azureuser"),
					LinuxConfiguration: &armcompute.LinuxConfiguration{
						DisablePasswordAuthentication: to.Ptr(true),
						SSH: &armcompute.SSHConfiguration{
							PublicKeys: []*armcompute.SSHPublicKey{
								{
									Path:    to.Ptr("/home/azureuser/.ssh/authorized_keys"),
									KeyData: to.Ptr(sshPublicKey),
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
		if useSpot {
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
	subnetName := "default-subnet"
	nicName := workerName + "-nic"

	subnetID, err := ap.createVNetAndSubnet(ctx, cred, rgName, vnetName, subnetName, location)
	if err != nil {
		return "", "", err
	}

	pubIPClient, err := createClient(func() (*armnetwork.PublicIPAddressesClient, error) {
		return armnetwork.NewPublicIPAddressesClient(ap.SubscriptionID, cred, nil)
	})
	if err != nil {
		return "", "", err
	}
	nicClient, err := createClient(func() (*armnetwork.InterfacesClient, error) {
		return armnetwork.NewInterfacesClient(ap.SubscriptionID, cred, nil)
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
		return armnetwork.NewPublicIPAddressesClient(ap.SubscriptionID, cred, nil)
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
	cred, err := azidentity.NewClientSecretCredential(ap.TenantID, ap.ClientID, ap.ClientSecret, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create credential: %w", err)
	}
	ctx := context.Background()

	rgClient, err := createClient(func() (*armresources.ResourceGroupsClient, error) {
		return armresources.NewResourceGroupsClient(ap.SubscriptionID, cred, nil)
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
					return armcompute.NewVirtualMachinesClient(ap.SubscriptionID, cred, nil)
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
						if vm.Tags != nil && vm.Tags["scitq"] != nil && *vm.Tags["scitq"] == ap.ScitqServerTag {
							// Get the network interface ID
							if vm.Properties != nil && vm.Properties.NetworkProfile != nil && len(vm.Properties.NetworkProfile.NetworkInterfaces) > 0 {
								networkInterfaceID := *vm.Properties.NetworkProfile.NetworkInterfaces[0].ID

								// Extract the network interface name from the ID
								parts := strings.Split(networkInterfaceID, "/")
								networkInterfaceName := parts[len(parts)-1]

								// Get the network interface client
								nicClient, err := armnetwork.NewInterfacesClient(ap.SubscriptionID, cred, nil)
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

	cred, err := azidentity.NewClientSecretCredential(ap.TenantID, ap.ClientID, ap.ClientSecret, nil)
	if err != nil {
		return fmt.Errorf("failed to create credential: %w", err)
	}
	ctx := context.Background()

	// Create the VM client.
	vmClient, err := createClient(func() (*armcompute.VirtualMachinesClient, error) {
		return armcompute.NewVirtualMachinesClient(ap.SubscriptionID, cred, nil)
	})
	if err != nil {
		return err
	}

	// Delete the VM with retries.
	err = retry(func() error {
		poller, err := vmClient.BeginDelete(ctx, rgName, vmName, nil)
		if err != nil {
			return fmt.Errorf("failed to begin VM deletion: %w", err)
		}
		_, err = poller.PollUntilDone(ctx, nil)
		return err
	}, 3, 5*time.Second)
	if err != nil {
		return fmt.Errorf("failed to delete VM after retries: %w", err)
	}
	log.Printf("VM %s deleted successfully", vmName)

	// Create the Resource Group client.
	rgClient, err := createClient(func() (*armresources.ResourceGroupsClient, error) {
		return armresources.NewResourceGroupsClient(ap.SubscriptionID, cred, nil)
	})
	if err != nil {
		return err
	}

	// Delete the resource group with retries.
	err = retry(func() error {
		rgPoller, err := rgClient.BeginDelete(ctx, rgName, nil)
		if err != nil {
			return fmt.Errorf("failed to begin deletion of resource group: %w", err)
		}
		_, err = rgPoller.PollUntilDone(ctx, nil)
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

	cred, err := azidentity.NewClientSecretCredential(ap.TenantID, ap.ClientID, ap.ClientSecret, nil)
	if err != nil {
		return fmt.Errorf("failed to create credential: %w", err)
	}
	ctx := context.Background()

	vmClient, err := createClient(func() (*armcompute.VirtualMachinesClient, error) {
		return armcompute.NewVirtualMachinesClient(ap.SubscriptionID, cred, nil)
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
