//
// Copyright (c) 2019 Intel Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package appsdk

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	nethttp "net/http"
	"os"
	"os/signal"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	toml "github.com/pelletier/go-toml"

	"github.com/edgexfoundry/go-mod-core-contracts/clients"
	"github.com/edgexfoundry/go-mod-core-contracts/clients/command"
	"github.com/edgexfoundry/go-mod-core-contracts/clients/coredata"
	"github.com/edgexfoundry/go-mod-core-contracts/clients/logger"
	"github.com/edgexfoundry/go-mod-core-contracts/clients/metadata"
	"github.com/edgexfoundry/go-mod-core-contracts/clients/notifications"
	"github.com/edgexfoundry/go-mod-core-contracts/clients/scheduler"
	"github.com/edgexfoundry/go-mod-core-contracts/models"
	"github.com/edgexfoundry/go-mod-registry/pkg/types"
	"github.com/edgexfoundry/go-mod-registry/registry"

	"github.com/tuanldchainos/app-functions-sdk-go/appcontext"
	"github.com/tuanldchainos/app-functions-sdk-go/internal"
	"github.com/tuanldchainos/app-functions-sdk-go/internal/common"
	"github.com/tuanldchainos/app-functions-sdk-go/internal/config"
	"github.com/tuanldchainos/app-functions-sdk-go/internal/runtime"
	"github.com/tuanldchainos/app-functions-sdk-go/internal/security"
	"github.com/tuanldchainos/app-functions-sdk-go/internal/store"
	"github.com/tuanldchainos/app-functions-sdk-go/internal/store/db/interfaces"
	"github.com/tuanldchainos/app-functions-sdk-go/internal/telemetry"
	"github.com/tuanldchainos/app-functions-sdk-go/internal/trigger"
	"github.com/tuanldchainos/app-functions-sdk-go/internal/trigger/http"
	"github.com/tuanldchainos/app-functions-sdk-go/internal/trigger/messagebus"
	"github.com/tuanldchainos/app-functions-sdk-go/internal/webserver"
	"github.com/tuanldchainos/app-functions-sdk-go/pkg/urlclient"
	"github.com/tuanldchainos/app-functions-sdk-go/pkg/util"
)

const (
	// ProfileSuffixPlaceholder is used to create unique names for profiles
	ProfileSuffixPlaceholder   = "<profile>"
	ProfileEnvironmentVariable = "edgex_profile"
	CoreServiceVersionKey      = "version"
	MajorIndex                 = 0
)

// The key type is unexported to prevent collisions with context keys defined in
// other packages.
type key int

// SDKKey is the context key for getting the sdk context.  Its value of zero is
// arbitrary.  If this package defined other context keys, they would have
// different integer values.
const SDKKey key = 0

// AppFunctionsSDK provides the necessary struct to create an instance of the Application Functions SDK. Be sure and provide a ServiceKey
// when creating an instance of the SDK. After creating an instance, you'll first want to call .Initialize(), to start up the SDK. Secondly,
// provide the desired transforms for your pipeline by calling .SetFunctionsPipeline(). Lastly, call .MakeItRun() to start listening for events based on
// your configured trigger.
type AppFunctionsSDK struct {
	// ServiceKey is the application services's key used for Configuration and Registration when the Registry is enabled
	ServiceKey string
	// LoggingClient is the EdgeX logger client used to log messages
	LoggingClient logger.LoggingClient
	// TargetType is the expected type of the incoming data. Must be set to a pointer to an instance of the type.
	// Defaults to &models.Event{} if nil. The income data is unmarshaled (JSON or CBOR) in to the type,
	// except when &[]byte{} is specified. In this case the []byte data is pass to the first function in the Pipeline.
	TargetType                interface{}
	transforms                []appcontext.AppFunction
	configProfile             string
	configDir                 string
	useRegistry               bool
	skipVersionCheck          bool
	overwriteConfig           bool
	usingConfigurablePipeline bool
	httpErrors                chan error
	runtime                   *runtime.GolangRuntime
	webserver                 *webserver.WebServer
	edgexClients              common.EdgeXClients
	registryClient            registry.Client
	config                    common.ConfigurationStruct
	storeClient               interfaces.StoreClient
	secretProvider            *security.SecretProvider
	storeForwardWg            *sync.WaitGroup
	storeForwardCancelCtx     context.CancelFunc
	appWg                     *sync.WaitGroup
	appCtx                    context.Context
	appCancelCtx              context.CancelFunc
}

// AddRoute allows you to leverage the existing webserver to add routes.
func (sdk *AppFunctionsSDK) AddRoute(route string, handler func(nethttp.ResponseWriter, *nethttp.Request), methods ...string) error {
	if route == clients.ApiPingRoute ||
		route == clients.ApiConfigRoute ||
		route == clients.ApiMetricsRoute ||
		route == clients.ApiVersionRoute ||
		route == internal.ApiTriggerRoute {
		return errors.New("route is reserved")
	}
	return sdk.webserver.AddRoute(route, sdk.addContext(handler), methods...)
}

// MakeItRun will initialize and start the trigger as specifed in the
// configuration. It will also configure the webserver and start listening on
// the specified port.
func (sdk *AppFunctionsSDK) MakeItRun() error {
	httpErrors := make(chan error)
	defer close(httpErrors)

	sdk.runtime = &runtime.GolangRuntime{
		TargetType: sdk.TargetType,
		ServiceKey: sdk.ServiceKey,
	}

	sdk.runtime.Initialize(sdk.storeClient, sdk.secretProvider)
	sdk.runtime.SetTransforms(sdk.transforms)
	// determine input type and create trigger for it
	t := sdk.setupTrigger(sdk.config, sdk.runtime)

	// Initialize the trigger (i.e. start a web server, or connect to message bus)
	err := t.Initialize(sdk.appWg, sdk.appCtx)
	if err != nil {
		sdk.LoggingClient.Error(err.Error())
	}

	if sdk.config.Writable.StoreAndForward.Enabled {
		sdk.startStoreForward()
	} else {
		sdk.LoggingClient.Info("StoreAndForward disabled. Not running retry loop.")
	}

	sdk.LoggingClient.Info(sdk.config.Service.StartupMsg)

	signals := make(chan os.Signal)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	sdk.webserver.StartWebServer(sdk.httpErrors)

	select {
	case httpError := <-sdk.httpErrors:
		sdk.LoggingClient.Info("Terminating: ", httpError.Error())
		err = httpError

	case signalReceived := <-signals:
		sdk.LoggingClient.Info("Terminating: " + signalReceived.String())
	}

	if sdk.config.Writable.StoreAndForward.Enabled {
		sdk.storeForwardCancelCtx()
		sdk.storeForwardWg.Wait()
	}

	sdk.appCancelCtx() // Cancel all long running go funcs
	sdk.appWg.Wait()
	return err
}

// LoadConfigurablePipeline ...
func (sdk *AppFunctionsSDK) LoadConfigurablePipeline() ([]appcontext.AppFunction, error) {
	var pipeline []appcontext.AppFunction

	sdk.usingConfigurablePipeline = true

	sdk.TargetType = nil
	if sdk.config.Writable.Pipeline.UseTargetTypeOfByteArray {
		sdk.TargetType = &[]byte{}
	}

	configurable := AppFunctionsSDKConfigurable{
		Sdk: sdk,
	}
	valueOfType := reflect.ValueOf(configurable)
	pipelineConfig := sdk.config.Writable.Pipeline
	executionOrder := util.DeleteEmptyAndTrim(strings.FieldsFunc(pipelineConfig.ExecutionOrder, util.SplitComma))

	if len(executionOrder) <= 0 {
		return nil, errors.New(
			"execution Order has 0 functions specified. You must have a least one function in the pipeline")
	}
	sdk.LoggingClient.Debug("Execution Order", "Functions", strings.Join(executionOrder, ","))

	for _, functionName := range executionOrder {
		functionName = strings.TrimSpace(functionName)
		configuration, ok := pipelineConfig.Functions[functionName]
		if !ok {
			return nil, fmt.Errorf("function %s configuration not found in Pipeline.Functions section", functionName)
		}

		result := valueOfType.MethodByName(functionName)
		if result.Kind() == reflect.Invalid {
			return nil, fmt.Errorf("function %s is not a built in SDK function", functionName)
		} else if result.IsNil() {
			return nil, fmt.Errorf("invalid/missing configuration for %s", functionName)
		}

		// determine number of parameters required for function call
		inputParameters := make([]reflect.Value, result.Type().NumIn())
		// set keys to be all lowercase to avoid casing issues from configuration
		for key := range configuration.Parameters {
			configuration.Parameters[strings.ToLower(key)] = configuration.Parameters[key]
		}
		for index := range inputParameters {
			parameter := result.Type().In(index)

			switch parameter {
			case reflect.TypeOf(map[string]string{}):
				inputParameters[index] = reflect.ValueOf(configuration.Parameters)

			case reflect.TypeOf(models.Addressable{}):
				inputParameters[index] = reflect.ValueOf(configuration.Addressable)

			default:
				return nil, fmt.Errorf(
					"function %s has an unsupported parameter type: %s",
					functionName,
					parameter.String(),
				)
			}
		}

		function, ok := result.Call(inputParameters)[0].Interface().(appcontext.AppFunction)
		if !ok {
			return nil, fmt.Errorf("failed to cast function %s as AppFunction type", functionName)
		}
		pipeline = append(pipeline, function)
		configurable.Sdk.LoggingClient.Debug(fmt.Sprintf("%s function added to configurable pipeline", functionName))
	}

	return pipeline, nil
}

// SetFunctionsPipeline allows you to define each fgitunction to execute and the order in which each function
// will be called as each event comes in.
func (sdk *AppFunctionsSDK) SetFunctionsPipeline(transforms ...appcontext.AppFunction) error {
	if len(transforms) == 0 {
		return errors.New("no transforms provided to pipeline")
	}

	sdk.transforms = transforms

	if sdk.runtime != nil {
		sdk.runtime.SetTransforms(transforms)
		sdk.runtime.TargetType = sdk.TargetType
	}

	return nil
}

// ApplicationSettings returns the values specifed in the custom configuration section.
func (sdk *AppFunctionsSDK) ApplicationSettings() map[string]string {
	return sdk.config.ApplicationSettings
}

func (sdk *AppFunctionsSDK) GetServiceUrl(serviceName string) string {
	return sdk.config.Clients[serviceName].Url()
}

// GetAppSettingStrings returns the strings slice for the specified App Setting.
func (sdk *AppFunctionsSDK) GetAppSettingStrings(setting string) ([]string, error) {
	if sdk.config.ApplicationSettings == nil {
		return nil, fmt.Errorf("%s setting not found: ApplicationSettings section is missing", setting)
	}

	settingValue, ok := sdk.config.ApplicationSettings[setting]
	if !ok {
		return nil, fmt.Errorf("%s setting not found in ApplicationSettings", setting)
	}

	valueStrings := util.DeleteEmptyAndTrim(strings.FieldsFunc(settingValue, util.SplitComma))

	return valueStrings, nil
}

// Initialize will parse command line flags, register for interrupts,
// initialize the logging system, and ingest configuration.
func (sdk *AppFunctionsSDK) Initialize() error {
	applyCommandlineEnvironmentOverrides()

	flag.BoolVar(&sdk.useRegistry, "registry", false, "Indicates the service should use the registry.")
	flag.BoolVar(&sdk.useRegistry, "r", false, "Indicates the service should use registry.")

	flag.StringVar(&sdk.configProfile, "profile", "", "Specify a profile other than default.")
	flag.StringVar(&sdk.configProfile, "p", "", "Specify a profile other than default.")

	flag.StringVar(&sdk.configDir, "confdir", "", "Specify an alternate configuration directory.")
	flag.StringVar(&sdk.configDir, "c", "", "Specify an alternate configuration directory.")

	flag.BoolVar(&sdk.skipVersionCheck, "skipVersionCheck", false, "Indicates the service should skip the Core Service's version compatibility check.")
	flag.BoolVar(&sdk.skipVersionCheck, "s", false, "Indicates the service should skip the Core Service's version compatibility check.")

	flag.BoolVar(&sdk.overwriteConfig, "overwrite", false, "Overwrite configuration in the Registry with local values")
	flag.BoolVar(&sdk.overwriteConfig, "o", false, "Overwrite configuration in the Registry with local values")

	flag.Parse()

	// Service keys must be unique. If an executable is run multiple times, it must have a different
	// profile for each instance, thus adding the profile to the base key will make it unique.
	// This requires services that are expected to have multiple instances running, such as the Configurable App Service,
	// add the ProfileSuffixPlaceholder placeholder in the service key.
	//
	// The Dockerfile must also take this into account and set the profile appropriately, i.e. not just "docker"
	//

	if strings.Contains(sdk.ServiceKey, ProfileSuffixPlaceholder) {
		if sdk.configProfile == "" {
			sdk.ServiceKey = strings.Replace(sdk.ServiceKey, ProfileSuffixPlaceholder, "", 1)
		} else {
			sdk.ServiceKey = strings.Replace(sdk.ServiceKey, ProfileSuffixPlaceholder, "-"+sdk.configProfile, 1)
		}
	}

	// to first initialize the app context and cancel function as the context
	// is being used inside sdk.initializeSecretProvider() call below
	sdk.appCtx, sdk.appCancelCtx = context.WithCancel(context.Background())

	loggerInitialized := false
	databaseInitialized := false
	configurationInitialized := false
	bootstrapComplete := false
	secretProviderInitialized := false

	timeStart := time.Now()
	// Currently have to load configuration from filesystem first in order to obtain
	// Registry Host/Port and BootTimeout
	configuration, err := readConfigurationFromFile(sdk.configProfile, sdk.configDir)
	if err != nil {
		return err
	}

	bootTimeout, err := time.ParseDuration(configuration.Service.BootTimeout)
	if err != nil {
		fmt.Printf("warning- failed to parse Service.BootTimeout, use the default %s: %v\n",
			internal.BootTimeoutDefault.String(), err)
		bootTimeout = internal.BootTimeoutDefault
	}

	timeElapsed := time.Since(timeStart)

	// Bootstrap retry loop to ensure all dependencies are ready before continuing.
	until := time.Now().Add(bootTimeout - timeElapsed)
	for time.Now().Before(until) {
		if !configurationInitialized {
			err := sdk.initializeConfiguration(configuration)
			if err != nil {
				fmt.Printf("failed to initialize Registry: %v\n", err)
				goto ContinueWithSleep
			}
			configurationInitialized = true
			fmt.Printf("Configuration & Registry initialized")
		}

		if !loggerInitialized {
			loggingTarget, err := sdk.setLoggingTarget()
			if err != nil {
				fmt.Printf("logger initialization failed: %v", err)
				goto ContinueWithSleep
			}

			sdk.LoggingClient = logger.NewClient(
				sdk.ServiceKey,
				sdk.config.Logging.EnableRemote,
				loggingTarget,
				sdk.config.Writable.LogLevel,
			)
			sdk.LoggingClient.Info("Logger successfully initialized")
			sdk.edgexClients.LoggingClient = sdk.LoggingClient
			loggerInitialized = true
		}

		// Verify that Core Services major version matches this SDK's major version
		if !sdk.validateVersionMatch() {
			return fmt.Errorf("core service's version is not compatible with SDK's version")
		}

		if !secretProviderInitialized {
			if err := sdk.initializeSecretProvider(); err != nil {
				return err
			}
			secretProviderInitialized = true
		}

		// Currently only need the database if store and forward is enabled
		if sdk.config.Writable.StoreAndForward.Enabled {
			if !databaseInitialized {
				if sdk.initializeStoreClient() != nil {

					// Error already logged
					goto ContinueWithSleep
				}

				databaseInitialized = true
			}
		}

		sdk.initializeClients()
		sdk.LoggingClient.Info("Clients initialized")

		// This is the last dependency so can break out of the retry loop.
		bootstrapComplete = true
		break

	ContinueWithSleep:
		time.Sleep(time.Second * time.Duration(1))
	}

	if !bootstrapComplete {
		return fmt.Errorf("bootstrap retry timed out")
	}

	sdk.appWg = &sync.WaitGroup{}

	if sdk.useRegistry {
		sdk.appWg.Add(1)
		go sdk.listenForConfigChanges()
	}

	sdk.appWg.Add(1)
	go telemetry.StartCpuUsageAverage(sdk.appWg, sdk.appCtx, sdk.LoggingClient)

	sdk.webserver = webserver.NewWebServer(&sdk.config, sdk.secretProvider, sdk.LoggingClient, mux.NewRouter())
	sdk.webserver.ConfigureStandardRoutes()

	return nil
}

func (sdk *AppFunctionsSDK) initializeSecretProvider() error {

	sdk.secretProvider = security.NewSecretProvider(sdk.LoggingClient, &sdk.config)
	ok := sdk.secretProvider.Initialize(sdk.appCtx)
	if !ok {
		err := errors.New("unable to initialize secret provider")
		sdk.LoggingClient.Error(err.Error())
		return err
	}

	return nil
}

func (sdk *AppFunctionsSDK) initializeStoreClient() error {
	var err error

	credentials, err := sdk.secretProvider.GetDatabaseCredentials(sdk.config.Database)
	if err != nil {
		sdk.LoggingClient.Error("Unable to get Database Credentials", "error", err)
	}

	sdk.config.Database.Username = credentials.Username
	sdk.config.Database.Password = credentials.Password

	sdk.storeClient, err = store.NewStoreClient(sdk.config.Database)
	if err != nil {
		sdk.LoggingClient.Error(fmt.Sprintf("unable to initialize Database for Store and Forward: %s", err.Error()))
	}

	return err
}

func (sdk *AppFunctionsSDK) validateVersionMatch() bool {
	if sdk.skipVersionCheck {
		sdk.LoggingClient.Info("Skipping core service version compatibility check")
		return true
	}

	// SDK version is set via the SemVer TAG at build time
	// and has the format "v{major}.{minor}.{patch}[-dev.{build}]"
	sdkVersionParts := strings.Split(internal.SDKVersion, ".")
	if len(sdkVersionParts) < 3 {
		sdk.LoggingClient.Error("SDK version is malformed", "version", internal.SDKVersion)
		return false
	}

	sdkVersionParts[MajorIndex] = strings.Replace(sdkVersionParts[MajorIndex], "v", "", 1)
	if sdkVersionParts[MajorIndex] == "0" {
		sdk.LoggingClient.Info("Skipping core service version compatibility check for SDK Beta version or running in debugger", "version", internal.SDKVersion)
		return true
	}

	url := sdk.config.Clients[common.CoreDataClientName].Url() + clients.ApiVersionRoute
	data, err := clients.GetRequestWithURL(context.Background(), url)
	if err != nil {
		sdk.LoggingClient.Error("Unable to get version of Core Services", "error", err)
		return false
	}

	versionJson := map[string]string{}
	err = json.Unmarshal(data, &versionJson)
	if err != nil {
		sdk.LoggingClient.Error("Unable to un-marshal Core Services version data", "error", err)
		return false
	}

	version, ok := versionJson[CoreServiceVersionKey]
	if !ok {
		sdk.LoggingClient.Error(fmt.Sprintf("Core Services version data missing '%s' information", CoreServiceVersionKey))
		return false
	}

	// Core Service version is reported as "{major}.{minor}.{patch}"
	coreVersionParts := strings.Split(version, ".")
	if len(coreVersionParts) != 3 {
		sdk.LoggingClient.Error("Core Services version is malformed", "version", version)
		return false
	}

	// Do Major versions match?
	if coreVersionParts[0] == sdkVersionParts[0] {
		sdk.LoggingClient.Debug(
			fmt.Sprintf("Confirmed Core Services version (%s) is compatible with SDK's version (%s)",
				version, internal.SDKVersion))
		return true
	}

	sdk.LoggingClient.Error(fmt.Sprintf("Core services version (%s) is not compatible with SDK's version(%s)",
		version, internal.SDKVersion))
	return false
}

// setupTrigger configures the appropriate trigger as specified by configuration.
func (sdk *AppFunctionsSDK) setupTrigger(configuration common.ConfigurationStruct, runtime *runtime.GolangRuntime) trigger.Trigger {
	var t trigger.Trigger
	// Need to make dynamic, search for the binding that is input

	switch strings.ToUpper(configuration.Binding.Type) {
	case "HTTP":
		sdk.LoggingClient.Info("HTTP trigger selected")
		t = &http.Trigger{Configuration: configuration, Runtime: runtime, Webserver: sdk.webserver, EdgeXClients: sdk.edgexClients}
	case "MESSAGEBUS":
		sdk.LoggingClient.Info("MessageBus trigger selected")
		t = &messagebus.Trigger{Configuration: configuration, Runtime: runtime, EdgeXClients: sdk.edgexClients}
	}

	return t
}

func (sdk *AppFunctionsSDK) addContext(next func(nethttp.ResponseWriter, *nethttp.Request)) func(nethttp.ResponseWriter, *nethttp.Request) {
	return func(w nethttp.ResponseWriter, r *nethttp.Request) {
		ctx := context.WithValue(r.Context(), SDKKey, sdk)
		next(w, r.WithContext(ctx))
	}
}

func (sdk *AppFunctionsSDK) initializeClients() {
	// Need when passing all Clients to other components
	sdk.edgexClients.LoggingClient = sdk.LoggingClient
	wg := &sync.WaitGroup{}
	clientMonitor, err := time.ParseDuration(sdk.config.Service.ClientMonitor)
	if err != nil {
		sdk.LoggingClient.Warn(
			fmt.Sprintf(
				"Service.ClientMonitor failed to parse: %s, use the default value: %v",
				err,
				internal.ClientMonitorDefault,
			),
		)
		// fall back to default value
		clientMonitor = internal.ClientMonitorDefault
	}

	interval := int(clientMonitor / time.Millisecond)

	// Use of these client interfaces is optional, so they are not required to be configured. For instance if not
	// sending commands, then don't need to have the Command client in the configuration.
	if _, ok := sdk.config.Clients[common.CoreDataClientName]; ok {
		sdk.edgexClients.EventClient = coredata.NewEventClient(
			urlclient.New(
				context.Background(),
				wg,
				sdk.registryClient,
				clients.CoreDataServiceKey,
				clients.ApiEventRoute,
				interval,
				sdk.config.Clients[common.CoreDataClientName].Url()+clients.ApiEventRoute,
			),
		)

		sdk.edgexClients.ReadingClient = coredata.NewReadingClient(
			urlclient.New(
				context.Background(),
				wg,
				sdk.registryClient,
				clients.CoreDataServiceKey,
				clients.ApiReadingRoute,
				interval,
				sdk.config.Clients[common.CoreDataClientName].Url()+clients.ApiReadingRoute,
			),
		)

		sdk.edgexClients.ValueDescriptorClient = coredata.NewValueDescriptorClient(
			urlclient.New(
				context.Background(),
				wg,
				sdk.registryClient,
				clients.CoreDataServiceKey,
				clients.ApiValueDescriptorRoute,
				interval,
				sdk.config.Clients[common.CoreDataClientName].Url()+clients.ApiValueDescriptorRoute,
			),
		)
	}

	if _, ok := sdk.config.Clients[common.CoreCommandClientName]; ok {
		sdk.edgexClients.CommandClient = command.NewCommandClient(
			urlclient.New(
				context.Background(),
				wg,
				sdk.registryClient,
				clients.CoreCommandServiceKey,
				clients.ApiDeviceRoute,
				interval,
				sdk.config.Clients[common.CoreCommandClientName].Url()+clients.ApiDeviceRoute,
			),
		)
	}

	if _, ok := sdk.config.Clients[common.NotificationsClientName]; ok {
		sdk.edgexClients.NotificationsClient = notifications.NewNotificationsClient(
			urlclient.New(
				context.Background(),
				wg,
				sdk.registryClient,
				clients.SupportNotificationsServiceKey,
				clients.ApiNotificationRoute,
				interval,
				sdk.config.Clients[common.NotificationsClientName].Url()+clients.ApiNotificationRoute,
			),
		)
	}

	if _, ok := sdk.config.Clients[common.MetadataClientName]; ok {
		sdk.edgexClients.AddressableClient = metadata.NewAddressableClient(
			urlclient.New(
				context.Background(),
				wg,
				sdk.registryClient,
				clients.CoreMetaDataServiceKey,
				clients.ApiAddressableRoute,
				interval,
				sdk.config.Clients[common.MetadataClientName].Url()+clients.ApiAddressableRoute,
			),
		)

		sdk.edgexClients.DeviceClient = metadata.NewDeviceClient(
			urlclient.New(
				context.Background(),
				wg,
				sdk.registryClient,
				clients.CoreMetaDataServiceKey,
				clients.ApiDeviceRoute,
				interval,
				sdk.config.Clients[common.MetadataClientName].Url()+clients.ApiDeviceRoute,
			),
		)

		sdk.edgexClients.ProvisionWatcherClient = metadata.NewProvisionWatcherClient(
			urlclient.New(
				context.Background(),
				wg,
				sdk.registryClient,
				clients.CoreMetaDataServiceKey,
				clients.ApiProvisionWatcherRoute,
				interval,
				sdk.config.Clients[common.MetadataClientName].Url()+clients.ApiProvisionWatcherRoute,
			),
		)
	}

	if _, ok := sdk.config.Clients[common.SchedulerClientName]; ok {
		sdk.edgexClients.IntervalClient = scheduler.NewIntervalClient(
			urlclient.New(
				context.Background(),
				wg,
				sdk.registryClient,
				clients.SupportSchedulerServiceKey,
				clients.ApiIntervalRoute,
				interval,
				sdk.config.Clients[common.MetadataClientName].Url()+clients.ApiIntervalRoute,
			),
		)

		sdk.edgexClients.IntervalActionClient = scheduler.NewIntervalActionClient(
			urlclient.New(
				context.Background(),
				wg,
				sdk.registryClient,
				clients.SupportSchedulerServiceKey,
				clients.ApiIntervalActionRoute,
				interval,
				sdk.config.Clients[common.MetadataClientName].Url()+clients.ApiIntervalActionRoute,
			),
		)
	}

}

func (sdk *AppFunctionsSDK) initializeConfiguration(configuration *common.ConfigurationStruct) error {
	if sdk.useRegistry {
		e := config.NewEnvironment()
		configuration.Registry = e.OverrideRegistryInfoFromEnvironment(configuration.Registry)
		configuration.Service = e.OverrideServiceInfoFromEnvironment(configuration.Service)

		if _, err := time.ParseDuration(configuration.Service.CheckInterval); err != nil {
			return fmt.Errorf("failed to parse Service.CheckInterval: %v", err)
		}

		registryConfig := types.Config{
			Host:            configuration.Registry.Host,
			Port:            configuration.Registry.Port,
			Type:            configuration.Registry.Type,
			Stem:            internal.ConfigRegistryStem,
			CheckInterval:   configuration.Service.CheckInterval,
			CheckRoute:      clients.ApiPingRoute,
			ServiceKey:      sdk.ServiceKey,
			ServiceHost:     configuration.Service.Host,
			ServicePort:     configuration.Service.Port,
			ServiceProtocol: configuration.Service.Protocol,
		}

		client, err := registry.NewRegistryClient(registryConfig)
		if err != nil {
			return fmt.Errorf("connection to Registry could not be made: %v", err)
		}

		// set registryClient
		sdk.registryClient = client

		if !sdk.registryClient.IsAlive() {
			return fmt.Errorf("registry (%s) is not running", registryConfig.Type)
		}

		hasConfig, err := sdk.registryClient.HasConfiguration()
		if err != nil {
			return fmt.Errorf("could not determine if registry has configuration: %v", err)
		}

		if !sdk.overwriteConfig && hasConfig {
			rawConfig, err := sdk.registryClient.GetConfiguration(configuration)
			if err != nil {
				return fmt.Errorf("could not get configuration from Registry: %v", err)
			}

			actual, ok := rawConfig.(*common.ConfigurationStruct)
			if !ok {
				return fmt.Errorf("configuration from Registry failed type check")
			}
			configuration = actual

			// Check that information was successfully read from Consul
			if configuration.Service.Port == 0 {
				sdk.LoggingClient.Error("Error reading from registry")
			}

			fmt.Println("Configuration loaded from registry with service key: " + sdk.ServiceKey)
		} else {
			// Marshal into a toml Tree for overriding with environment variables.
			contents, err := toml.Marshal(*configuration)
			if err != nil {
				return err
			}
			configTree, err := toml.LoadBytes(contents)
			if err != nil {
				return err
			}

			err = sdk.registryClient.PutConfigurationToml(e.OverrideFromEnvironment(configTree), true)
			if err != nil {
				return fmt.Errorf("could not push configuration into registry: %v", err)
			}
			err = configTree.Unmarshal(configuration)
			if err != nil {
				return fmt.Errorf("could not marshal configTree to configuration: %v", err.Error())
			}
			fmt.Println("Configuration pushed to registry with service key: " + sdk.ServiceKey)
		}

		// Register the service with Registry
		err = sdk.registryClient.Register()
		if err != nil {
			return fmt.Errorf("could not register service with Registry: %v", err)
		}
	}

	sdk.config = *configuration
	return nil
}

func (sdk *AppFunctionsSDK) listenForConfigChanges() {

	updates := make(chan interface{})
	registryErrors := make(chan error)

	defer sdk.appWg.Done()
	defer close(updates)

	sdk.LoggingClient.Info("Listening for changes from registry")
	sdk.registryClient.WatchForChanges(updates, registryErrors, &common.WritableInfo{}, internal.WritableKey)

	for {
		select {
		case <-sdk.appCtx.Done():
			sdk.LoggingClient.Info("Exiting Listen for changes from registry")
			return

		case err := <-registryErrors:
			sdk.LoggingClient.Error(err.Error())

		case raw, ok := <-updates:
			if !ok {
				sdk.LoggingClient.Error("Failed to receive changes from update channel")
				return
			}

			actual, ok := raw.(*common.WritableInfo)
			if !ok {
				sdk.LoggingClient.Error("listenForConfigChanges() type check failed")
				return
			}

			previousLogLevel := sdk.config.Writable.LogLevel
			previousStoreForward := sdk.config.Writable.StoreAndForward

			sdk.config.Writable = *actual
			sdk.LoggingClient.Info("Writable configuration has been updated from Registry")

			// Note: Changes occur one setting at a time so if setting not part of the pipeline,
			//       then skip updating the pipeline
			switch {
			case previousLogLevel != sdk.config.Writable.LogLevel:
				_ = sdk.LoggingClient.SetLogLevel(sdk.config.Writable.LogLevel)
				sdk.LoggingClient.Info(fmt.Sprintf("Logging level changed to %s", sdk.config.Writable.LogLevel))

			case previousStoreForward.MaxRetryCount != sdk.config.Writable.StoreAndForward.MaxRetryCount:
				if sdk.config.Writable.StoreAndForward.MaxRetryCount < 0 {
					sdk.LoggingClient.Warn(fmt.Sprintf("StoreAndForward MaxRetryCount can not be less than 0, defaulting to 1"))
					sdk.config.Writable.StoreAndForward.MaxRetryCount = 1
				}
				sdk.LoggingClient.Info(fmt.Sprintf("StoreAndForward MaxRetryCount changed to %d", sdk.config.Writable.StoreAndForward.MaxRetryCount))

			case previousStoreForward.RetryInterval != sdk.config.Writable.StoreAndForward.RetryInterval:
				sdk.processConfigChangedStoreForwardRetryInterval()

			case previousStoreForward.Enabled != sdk.config.Writable.StoreAndForward.Enabled:
				sdk.processConfigChangedStoreForwardEnabled()

			default:
				// Must have been a change to the pipeline configuration, so now attempt to update it.
				sdk.processConfigChangedPipeline()
			}
		}
	}
}

func (sdk *AppFunctionsSDK) processConfigChangedStoreForwardRetryInterval() {
	if sdk.config.Writable.StoreAndForward.Enabled {
		sdk.stopStoreForward()
		sdk.startStoreForward()
	}
}

func (sdk *AppFunctionsSDK) processConfigChangedStoreForwardEnabled() {
	if sdk.config.Writable.StoreAndForward.Enabled {
		// StoreClient must be set up for StoreAndForward
		if sdk.storeClient == nil {
			if sdk.initializeStoreClient() != nil {
				// Error already logged
				sdk.config.Writable.StoreAndForward.Enabled = false
				return
			}

			sdk.runtime.Initialize(sdk.storeClient, sdk.secretProvider)
		}

		sdk.startStoreForward()
	} else {
		sdk.stopStoreForward()
	}
}

func (sdk *AppFunctionsSDK) processConfigChangedPipeline() {
	if sdk.usingConfigurablePipeline {
		transforms, err := sdk.LoadConfigurablePipeline()
		if err != nil {
			sdk.LoggingClient.Error("unable to reload Configurable Pipeline from Registry: " + err.Error())
			return
		}
		err = sdk.SetFunctionsPipeline(transforms...)
		if err != nil {
			sdk.LoggingClient.Error("unable to set Configurable Pipeline from Registry: " + err.Error())
			return
		}

		sdk.LoggingClient.Info("Reloaded Configurable Pipeline from Registry")
	}
}

func (sdk *AppFunctionsSDK) startStoreForward() {
	var storeForwardEnabledCtx context.Context
	sdk.storeForwardWg = &sync.WaitGroup{}
	storeForwardEnabledCtx, sdk.storeForwardCancelCtx = context.WithCancel(context.Background())
	sdk.runtime.StartStoreAndForward(sdk.appWg, sdk.appCtx,
		sdk.storeForwardWg, storeForwardEnabledCtx,
		sdk.ServiceKey, &sdk.config, sdk.edgexClients)
}

func (sdk *AppFunctionsSDK) stopStoreForward() {
	sdk.LoggingClient.Info("Canceling Store and Forward retry loop")
	sdk.storeForwardCancelCtx()
	sdk.storeForwardWg.Wait()
}

// GetSecrets retrieves secrets from a secret store.
// path specifies the type or location of the secrets to retrieve. If specified it is appended
// to the base path from the SecretConfig
// keys specifies the secrets which to retrieve. If no keys are provided then all the keys associated with the
// specified path will be returned.
func (sdk *AppFunctionsSDK) GetSecrets(path string, keys ...string) (map[string]string, error) {
	return sdk.secretProvider.GetSecrets(path, keys...)
}

// StoreSecrets stores the secrets to a secret store.
// it sets the values requested at provided keys
// path specifies the type or location of the secrets to store. If specified it is appended
// to the base path from the SecretConfig
// secrets map specifies the "key": "value" pairs of secrets to store
func (sdk *AppFunctionsSDK) StoreSecrets(path string, secrets map[string]string) error {
	return sdk.secretProvider.StoreSecrets(path, secrets)
}

func (sdk *AppFunctionsSDK) setLoggingTarget() (string, error) {
	if sdk.config.Logging.EnableRemote {
		logging, ok := sdk.config.Clients[common.LoggingClientName]
		if !ok {
			return "", errors.New("logging client configuration is missing")
		}

		return logging.Url() + clients.ApiLoggingRoute, nil
	}

	return sdk.config.Logging.File, nil
}

func applyCommandlineEnvironmentOverrides() {
	// Currently there is just one commandline option that can be overwritten with an environment variable.
	// If more are added, a more dynamic data driven approach should be used to avoid code duplication.

	profileName := os.Getenv(ProfileEnvironmentVariable)
	if profileName == "" {
		return
	}

	found := false
	for index, option := range os.Args {
		if strings.Contains(option, "-p=") || strings.Contains(option, "--profile=") {
			os.Args[index] = "--profile=" + profileName
			found = true
		}
	}

	if !found {
		os.Args = append(os.Args, "--profile="+profileName)
	}
}

func readConfigurationFromFile(profileName string, configDir string) (*common.ConfigurationStruct, error) {
	configuration, err := common.LoadFromFile(profileName, configDir)
	if err != nil {
		return nil, err
	}
	return configuration, nil
}
