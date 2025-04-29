package azure

import (
	"bytes"
	"context"
	"embed"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/gmtsciencedev/scitq2/server/config" // Adjust the import path to where generic.go is located.
	"github.com/gmtsciencedev/scitq2/server/updater"
)

// loggingPolicy is a custom pipeline policy for logging HTTP requests and responses.
//type loggingPolicy struct{}
//
//func (lp loggingPolicy) Do(req *policy.Request) (*http.Response, error) {
//	r := req.Raw()
//	log.Printf("Request: %s %s", r.Method, r.URL.String())
//	for name, values := range r.Header {
//		log.Printf("Request Header: %s: %v", name, values)
//	}
//	if r.Body != nil {
//		bodyBytes, err := io.ReadAll(r.Body)
//		if err == nil {
//			log.Printf("Request Body: %s", string(bodyBytes))
//			// Reset the body so it can be read later.
//			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
//		} else {
//			log.Printf("Error reading request body: %v", err)
//		}
//	}
//
//	resp, err := req.Next()
//	if err != nil {
//		log.Printf("Response error: %v", err)
//	} else {
//		log.Printf("Response status: %d", resp.StatusCode)
//		for name, values := range resp.Header {
//			log.Printf("Response Header: %s: %v", name, values)
//		}
//		if resp.Body != nil {
//			respBodyBytes, err := io.ReadAll(resp.Body)
//			if err == nil {
//				log.Printf("Response Body: %s", string(respBodyBytes))
//				resp.Body = io.NopCloser(bytes.NewReader(respBodyBytes))
//			} else {
//				log.Printf("Error reading response body: %v", err)
//			}
//		}
//	}
//	return resp, err
//}

// Constants and regex definitions.
const DEFAULT_OS_DISK = 30.0

//go:embed resource/*
var embeddedResource embed.FS

var flavorNameRe = regexp.MustCompile(`Standard_(?P<main>[A-Z0-9-]+)(?P<option>[a-z]+)?(_.*?)?$`)

// VMSize represents an Azure virtual machine size.
type VMSize struct {
	Name                 string
	NumberOfCores        int
	MemoryInMB           int
	ResourceDiskSizeInMB int
}

// RetailPricesResponse models the JSON response for retail prices.
type RetailPricesResponse struct {
	Items []struct {
		ArmSkuName    string  `json:"armSkuName"`
		ArmRegionName string  `json:"armRegionName"`
		UnitPrice     float64 `json:"unitPrice"`
	} `json:"Items"`
	NextPageLink string `json:"NextPageLink"`
}

// Azure represents the provider-specific updater.
type Azure struct {
	*updater.GenericProvider
	SubscriptionID string
	Client         *azidentity.ClientSecretCredential
	// These client fields would be set using actual Azure SDK clients.
	// ComputeClient        *armcompute.VirtualMachineSizesClient
	// ResourceGraphClient  *armresourcegraph.Client
	Regions          []string
	Quotas           map[string]config.Quota
	FlavorMetricsMap map[string]*updater.FlavorMetrics // key: "flavor|region"
}

// NewAzure creates a new Azure updater.
func NewAzure(cfg config.Config, providerCfg config.AzureConfig) (*Azure, error) {
	cred, err := azidentity.NewClientSecretCredential(providerCfg.TenantID, providerCfg.ClientID, providerCfg.ClientSecret, nil)
	if err != nil {
		return nil, err
	}

	gp, err := updater.NewGenericProvider(cfg, providerCfg.GetName())
	if err != nil {
		return nil, err
	}

	return &Azure{
		GenericProvider:  gp,
		SubscriptionID:   providerCfg.SubscriptionID,
		Client:           cred,
		Regions:          providerCfg.Regions,
		Quotas:           providerCfg.Quotas,
		FlavorMetricsMap: make(map[string]*updater.FlavorMetrics),
	}, nil
}

// listVMSizes calls the Azure SDK's VirtualMachineSizesClient.List method to retrieve the available VM sizes.
func (az *Azure) listVMSizes(region string) ([]*VMSize, error) {
	// Create a new VirtualMachineSizesClient using the subscription ID and credential.
	computeClient, err := armcompute.NewVirtualMachineSizesClient(az.SubscriptionID, az.Client, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create VirtualMachineSizesClient: %w", err)
	}

	// Call the List method for the given region.
	pager := computeClient.NewListPager(region, nil)
	var sizes []*VMSize

	for pager.More() {
		resp, err := pager.NextPage(context.Background())
		if err != nil {
			return nil, fmt.Errorf("failed to list VM sizes for region %s: %w", region, err)
		}

		// Convert the returned sizes into our local VMSize struct.
		for _, vmSize := range resp.Value {
			// Note: The SDK returns pointer values for fields.
			size := &VMSize{
				Name:                 *vmSize.Name,
				NumberOfCores:        int(*vmSize.NumberOfCores),
				MemoryInMB:           int(*vmSize.MemoryInMB),
				ResourceDiskSizeInMB: int(*vmSize.ResourceDiskSizeInMB),
			}
			sizes = append(sizes, size)
		}
	}

	return sizes, nil
}

// maxFloat returns the larger of two float64 numbers.
func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// GetFlavors retrieves flavor information from Azure.
func (az *Azure) GetFlavors() error {
	//fmt.Println("Adding GPU info")
	flavorGPU := make(map[string]map[string]string)
	f, err := embeddedResource.ReadFile("resource/gpu.tsv")
	if err != nil {
		return err
	}

	reader := csv.NewReader(bytes.NewReader(f))
	reader.Comma = '\t'
	headers, err := reader.Read()
	if err != nil {
		return err
	}
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Println("CSV error:", err)
			continue
		}
		entry := make(map[string]string)
		for i, h := range headers {
			entry[h] = record[i]
		}
		flavorGPU[entry["flavor"]] = entry
	}

	log.Printf("Updating Azure flavors")
	seenFlavors := make(map[string]bool)
	var flavors []*updater.Flavor
	flavorMetricsMap := make(map[string]*updater.FlavorMetrics)

	for _, region := range az.Regions {
		vmSizes, err := az.listVMSizes(region)
		if err != nil {
			continue
		}
		for _, vm := range vmSizes {
			// Use regex to match the flavor name.
			matches := flavorNameRe.FindStringSubmatch(vm.Name)
			if matches == nil {
				if !strings.Contains(vm.Name, "Basic") {
					log.Printf("<Not a standard flavor %s>", vm.Name)
				}
				continue
			}
			option := ""
			if len(matches) > 2 {
				option = matches[2]
			}
			if option == "" || !strings.Contains(option, "s") {
				continue
			}
			if strings.Contains(option, "p") {
				continue
			}
			if !seenFlavors[vm.Name] {
				f := &updater.Flavor{
					Name:         vm.Name,
					ProviderID:   int(az.GenericProvider.ProviderID),
					ProviderName: az.ProviderName,
					CPU:          vm.NumberOfCores,
					Mem:          float64(vm.MemoryInMB) / 1024,
					Disk:         maxFloat(DEFAULT_OS_DISK, float64(vm.ResourceDiskSizeInMB)/1024),
					Bandwidth:    1,
					HasGPU:       false,
					HasQuickDisk: false,
				}
				if strings.HasPrefix(vm.Name, "Standard_N") {
					f.HasGPU = true
				}
				if gpuEntry, ok := flavorGPU[vm.Name]; ok {
					f.GPU = gpuEntry["gpu"]
					if gpumem, err := strconv.Atoi(gpuEntry["gpumem"]); err == nil {
						f.GPUMem = gpumem
					}
				}
				flavors = append(flavors, f)
				seenFlavors[vm.Name] = true
			}
			// Record flavor metrics for this region.
			key := fmt.Sprintf("%s|%s", vm.Name, region)
			flavorMetricsMap[key] = &updater.FlavorMetrics{
				FlavorName: vm.Name,
				ProviderID: int(az.GenericProvider.ProviderID),
				RegionName: region,
			}
		}
	}

	if err := az.GenericProvider.UpdateFlavors(flavors); err != nil {
		return err
	}
	az.FlavorMetricsMap = flavorMetricsMap
	return nil
}

// httpGet is a helper to perform HTTP GET requests.
func httpGet(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func wrapRegionsWithQuotes(regions []string) []string {
	quoted := make([]string, len(regions))
	for i, region := range regions {
		quoted[i] = fmt.Sprintf("'%s'", region) // Ensure each region is wrapped in single quotes
	}
	return quoted
}

// GetMetrics retrieves pricing and eviction data.
func (az *Azure) GetMetrics() error {
	log.Printf("Finding prices")
	var regionFilters []string
	for _, region := range az.Regions {
		regionFilters = append(regionFilters, fmt.Sprintf("armRegionName eq '%s'", region))
	}
	query := fmt.Sprintf("https://prices.azure.com/api/retail/prices?currencyCode='EUR'&$filter= serviceName eq 'Virtual Machines' and priceType eq 'Consumption' and contains(meterName, 'Spot') and (%s)",
		strings.Join(regionFilters, " or "))

	//var retainMetricsKeys []string
	// Loop over pages.
	for query != "" {
		respBytes, err := httpGet(query)
		if err != nil {
			break
		}
		var data RetailPricesResponse
		if err := json.Unmarshal(respBytes, &data); err != nil {
			break
		}
		for _, item := range data.Items {
			flavor := item.ArmSkuName
			region := item.ArmRegionName
			price := item.UnitPrice
			key := fmt.Sprintf("%s|%s", flavor, region)
			if fm, ok := az.FlavorMetricsMap[key]; ok {
				//retainMetricsKeys = append(retainMetricsKeys, key)
				fm.Cost = price
			}
		}
		query = data.NextPageLink
	}

	// Build the final metrics slice.
	//newMetrics := []*updater.FlavorMetrics{}
	//for _, key := range retainMetricsKeys {
	//	if fm, ok := az.FlavorMetricsMap[key]; ok {
	//		newMetrics = append(newMetrics, fm)
	//	}
	//}

	// Prepare a lowercase-key map for eviction lookup.
	flavorsLower := make(map[string]*updater.FlavorMetrics)
	for key, fm := range az.FlavorMetricsMap {
		flavorsLower[strings.ToLower(key)] = fm
	}

	log.Printf("Finding eviction rates")
	skipToken := ""
	var fullMetricKeys []string
	for {
		var options *QueryRequestOptions
		if skipToken != "" {
			options = &QueryRequestOptions{SkipToken: skipToken}
		}
		qr := QueryRequest{
			Query: fmt.Sprintf(`spotresources
			| where type == 'microsoft.compute/skuspotevictionrate/location'
			| where location in (%s)
			| project skuName=tostring(sku.name), location, evictionRate=properties.evictionRate, price=properties.unitPrice
			| order by skuName`, strings.Join(wrapRegionsWithQuotes(az.Regions), ",")),
			//Query: "spotresources | where type == 'microsoft.compute/skuspotevictionrate/location' | project skuName=tostring(sku.name), location, evictionRate=properties.evictionRate, price=properties.unitPrice | order by skuName",

			//Query: `Resources
			//| project id, name, type
			//| limit 5`,
			Options: options,
		}
		queryResponse, err := az.runResourceGraphQuery(qr)
		if err != nil {
			log.Printf("Azure graph query error: %v", err)
			break
		}
		for _, item := range queryResponse.Data {
			//log.Printf("Eviction item %v", item)
			key := fmt.Sprintf("%s|%s", item["skuName"], item["location"])
			lowerKey := strings.ToLower(key)
			if fm, ok := flavorsLower[lowerKey]; ok {
				//log.Printf("Eviction %s %s %s", key, item["evictionRate"], item["price"])
				// Parse eviction rate (assumes format like "rate-10+").
				parts := strings.Split(item["evictionRate"], "-")
				evictionRatePart := strings.Trim(parts[len(parts)-1], " +")
				if eviction, err := strconv.Atoi(evictionRatePart); err == nil {
					fm.Eviction = eviction
				} else {
					log.Printf("!")
				}
				if !contains(fullMetricKeys, key) {
					//log.Printf("Eviction %s %d", key, fm.Eviction)
					fullMetricKeys = append(fullMetricKeys, key)
				}
			}
		}
		if queryResponse.SkipToken == "" {
			break
		}
		skipToken = queryResponse.SkipToken
	}

	//log.Printf("Updating Azure metrics with keys %v", fullMetricKeys)
	finalMetrics := []*updater.FlavorMetrics{}
	for _, key := range fullMetricKeys {
		if fm, ok := flavorsLower[strings.ToLower(key)]; ok {
			finalMetrics = append(finalMetrics, fm)
		}
	}
	//log.Printf("Azure metrics %v", az.FlavorMetricsMap)
	//log.Printf("Updating Azure metrics for %v", finalMetrics)

	if err := az.GenericProvider.UpdateFlavorMetrics(finalMetrics); err != nil {
		return err
	}
	return nil
}

// QueryRequest and QueryRequestOptions are your custom types.
type QueryRequestOptions struct {
	SkipToken string
}

type QueryRequest struct {
	Query   string
	Options *QueryRequestOptions
}

// QueryResponse is your custom type to hold the converted response.
type QueryResponse struct {
	Data      []map[string]string
	SkipToken string
}

// apiVersionPolicy is a custom policy that forces the "api-version" query parameter.
type apiVersionPolicy struct {
	version string
}

func (p apiVersionPolicy) Do(req *policy.Request) (*http.Response, error) {
	r := req.Raw()
	// Get the current query parameters, set the api-version, then encode back.
	q := r.URL.Query()
	q.Set("api-version", p.version)
	r.URL.RawQuery = q.Encode()
	return req.Next()
}

// runResourceGraphQuery executes a resource graph query using the Azure SDK.
func (az *Azure) runResourceGraphQuery(qr QueryRequest) (*QueryResponse, error) {
	// Create a new Resource Graph client using your credential.
	clientOptions := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			PerCallPolicies: []policy.Policy{
				//loggingPolicy{},
				apiVersionPolicy{version: "2021-03-01"},
			},
		},
	}
	client, err := armresourcegraph.NewClient(az.Client, clientOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource graph client: %w", err)
	}

	// Build the SDK query request.
	sdkReq := armresourcegraph.QueryRequest{
		//Subscriptions: []*string{to.Ptr(az.SubscriptionID)},
		Query: to.Ptr(qr.Query),
	}

	// Always set the result format to objectArray so that data is returned as an array of objects.
	//sdkOptions := &armresourcegraph.QueryRequestOptions{
	//	ResultFormat: to.Ptr(armresourcegraph.ResultFormatObjectArray),
	//	//ResultFormat: to.Ptr(armresourcegraph.ResultFormatTable),
	//}
	//if qr.Options != nil && qr.Options.SkipToken != "" {
	//	sdkOptions.SkipToken = to.Ptr(qr.Options.SkipToken)
	//}
	//sdkReq.Options = sdkOptions

	// Execute the query.
	ctx := context.Background()
	resp, err := client.Resources(ctx, sdkReq, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to run resource graph query: %w", err)
	}

	// Ensure we have data.
	if resp.Data == nil {
		return nil, fmt.Errorf("no data returned from resource graph query")
	}

	// Marshal resp.Data to JSON.
	dataBytes, err := json.Marshal(resp.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal resp.Data: %w", err)
	}
	//log.Printf("Raw resp.Data: %s", string(dataBytes))

	// With resultFormat set to objectArray, resp.Data should now be a JSON array.
	var dataArray []map[string]interface{}
	if err := json.Unmarshal(dataBytes, &dataArray); err != nil {
		return nil, fmt.Errorf("failed to unmarshal resp.Data as array: %w", err)
	}

	var data []map[string]string
	for _, item := range dataArray {
		row := make(map[string]string)
		for k, v := range item {
			row[k] = fmt.Sprintf("%v", v)
		}
		data = append(data, row)
	}

	// Return the QueryResponse.
	return &QueryResponse{
		Data:      data,
		SkipToken: "", // Adjust if needed.
	}, nil
}

// contains checks if a slice contains a string.
func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// Run is the entry point for the updater.
func Run(cfg config.Config, providerCfg config.AzureConfig) error {
	if len(providerCfg.Regions) == 0 {
		return fmt.Errorf("configure at least one region for Azure provider")
	}

	updater, err := NewAzure(cfg, providerCfg)
	if err != nil {
		return err
	}
	if err := updater.GetFlavors(); err != nil {
		return err
	}
	if err := updater.GetMetrics(); err != nil {
		return err
	}
	return updater.Close()
}
