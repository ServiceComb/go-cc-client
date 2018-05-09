/*
 * Copyright 2017 Huawei Technologies Co., Ltd
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *    http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Package memberdiscovery created on 2017/6/20.
package memberdiscovery

import (
	"crypto/tls"
	"errors"
	"fmt"
	"github.com/ServiceComb/go-archaius"
	"github.com/ServiceComb/go-archaius/core"
	"github.com/ServiceComb/go-cc-client"
	"github.com/ServiceComb/go-cc-client/serializers"
	"github.com/ServiceComb/go-chassis/core/common"
	"github.com/ServiceComb/go-chassis/core/config"
	"github.com/ServiceComb/go-chassis/core/endpoint-discovery"
	"github.com/ServiceComb/go-chassis/core/lager"
	chassisTLS "github.com/ServiceComb/go-chassis/core/tls"
	"github.com/ServiceComb/http-client"
	"io/ioutil"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
)

var (
	memDiscovery *MemDiscovery
	//HeaderTenantName is a variable of type string
	HeaderTenantName = "X-Tenant-Name"
	//ConfigMembersPath is a variable of type string
	ConfigMembersPath = ""
	//ConfigPath is a variable of type string
	ConfigPath = ""
	//ConfigRefreshPath is a variable of type string
	ConfigRefreshPath = ""
)

const (
	//StatusUP is a variable of type string
	StatusUP = "UP"
	//HeaderContentType is a variable of type string
	HeaderContentType = "Content-Type"
	//HeaderUserAgent is a variable of type string
	HeaderUserAgent = "User-Agent"
	//HeaderEnvironment specifies the environment of a service
	HeaderEnvironment  = "X-Environment"
	members            = "/configuration/members"
	dimensionsInfo     = "dimensionsInfo"
	dynamicConfigAPI   = `/configuration/refresh/items`
	getConfigAPI       = `/configuration/items`
	defaultContentType = "application/json"
	//Name is a variable of type string
	Name          = "configcenter"
	maxValue      = 256
	emptyDimeInfo = "Issue with regular expression or exceeded the max length"
)

//MemberDiscovery is a interface
type MemberDiscovery interface {
	ConfigurationInit([]string) error
	GetConfigServer() ([]string, error)
	RefreshMembers() error
	Shuffle() error
	GetWorkingConfigCenterIP([]string) ([]string, error)
}

//MemDiscovery is a struct
type MemDiscovery struct {
	ConfigServerAddresses []string
	//Logger                *log.Entry
	IsInit     bool
	TLSConfig  *tls.Config
	TenantName string
	EnableSSL  bool
	sync.RWMutex
	client *httpclient.URLClient
}

//Instance is a struct
type Instance struct {
	Status      string   `json:"status"`
	ServiceName string   `json:"serviceName"`
	IsHTTPS     bool     `json:"isHttps"`
	EntryPoints []string `json:"endpoints"`
}

//Members is a struct
type Members struct {
	Instances []Instance `json:"instances"`
}

//NewConfiCenterInit is a function to initialize the configcenter server
func (memDis *MemDiscovery) NewConfiCenterInit(tlsConfig *tls.Config, tenantName string, enableSSL bool) MemberDiscovery {
	if memDiscovery == nil {
		memDiscovery = memDis
		//memDiscovery.Logger = logger
		memDiscovery.TLSConfig = tlsConfig
		memDiscovery.TenantName = tenantName
		memDiscovery.EnableSSL = enableSSL
		var apiVersion string

		switch config.GlobalDefinition.Cse.Config.Client.APIVersion.Version {
		case "v2":
			apiVersion = "v2"
		case "V2":
			apiVersion = "v2"
		case "v3":
			apiVersion = "v3"
		case "V3":
			apiVersion = "v3"
		default:
			apiVersion = "v3"
		}
		//Update the API Base Path based on the Version
		updateAPIPath(apiVersion)

		//Initiate RestClient from http-client package
		options := &httpclient.URLClientOption{
			SSLEnabled: enableSSL,
			TLSConfig:  tlsConfig,
			Compressed: false,
			Verbose:    false,
		}
		memDiscovery.client, _ = httpclient.GetURLClient(options)
	}
	return memDiscovery
}

//HTTPDo Use http-client package for rest communication
func (memDis *MemDiscovery) HTTPDo(method string, rawURL string, headers http.Header, body []byte) (resp *http.Response, err error) {
	if len(headers) == 0 {
		headers = make(http.Header)
	}
	for k, v := range GetDefaultHeaders(memDis.TenantName) {
		headers[k] = v
	}
	return memDis.client.HttpDo(method, rawURL, headers, body)
}

//Update the Base PATH and HEADERS Based on the version of ConfigCenter used.
func updateAPIPath(apiVersion string) {

	//Check for the env Name in Container to get Domain Name
	//Default value is  "default"
	projectID, isExsist := os.LookupEnv(common.EnvProjectID)
	if !isExsist {
		projectID = "default"
	}
	switch apiVersion {
	case "v3":
		ConfigMembersPath = "/v3/" + projectID + members
		ConfigPath = "/v3/" + projectID + getConfigAPI
		ConfigRefreshPath = "/v3/" + projectID + dynamicConfigAPI
	case "v2":
		ConfigMembersPath = "/members"
		ConfigPath = "/configuration/v2/items"
		ConfigRefreshPath = "/configuration/v2/refresh/items"
	default:
		ConfigMembersPath = "/v3/" + projectID + members
		ConfigPath = "/v3/" + projectID + getConfigAPI
		ConfigRefreshPath = "/v3/" + projectID + dynamicConfigAPI
	}
}

//ConfigurationInit is a method for creating a configuration
func (memDis *MemDiscovery) ConfigurationInit(initConfigServer []string) error {
	if memDis.IsInit == true {
		return nil
	}

	if memDis.ConfigServerAddresses == nil {
		if initConfigServer == nil && len(initConfigServer) == 0 {
			err := errors.New(client.EmptyConfigServerConfig)
			lager.Logger.Error(client.EmptyConfigServerConfig, err)
			return err
		}

		memDis.ConfigServerAddresses = make([]string, 0)
		for _, server := range initConfigServer {
			memDis.ConfigServerAddresses = append(memDis.ConfigServerAddresses, server)
		}

		memDis.Shuffle()
	}

	memDis.IsInit = true
	return nil
}

//GetConfigServer is a method used for getting server configuration
func (memDis *MemDiscovery) GetConfigServer() ([]string, error) {
	if memDis.IsInit == false {
		err := errors.New(client.PackageInitError)
		lager.Logger.Error(client.PackageInitError, err)
		return nil, err
	}

	if len(memDis.ConfigServerAddresses) == 0 {
		err := errors.New(client.EmptyConfigServerMembers)
		lager.Logger.Error(client.EmptyConfigServerMembers, err)
		return nil, err
	}

	if config.GlobalDefinition.Cse.Config.Client.Autodiscovery {
		err := memDis.RefreshMembers()
		if err != nil {
			lager.Logger.Error("refresh member is failed", err)
			return nil, err
		}
	} else {
		tmpConfigAddrs := memDis.ConfigServerAddresses
		for key := range tmpConfigAddrs {
			if !strings.Contains(memDis.ConfigServerAddresses[key], "https") && memDis.EnableSSL {
				memDis.ConfigServerAddresses[key] = `https://` + memDis.ConfigServerAddresses[key]

			} else if !strings.Contains(memDis.ConfigServerAddresses[key], "http") {
				memDis.ConfigServerAddresses[key] = `http://` + memDis.ConfigServerAddresses[key]
			}
		}
	}

	err := memDis.Shuffle()
	if err != nil {
		lager.Logger.Error("member shuffle is failed", err)
		return nil, err
	}

	memDis.RLock()
	defer memDis.RUnlock()
	lager.Logger.Debugf("member server return %s", memDis.ConfigServerAddresses[0])
	return memDis.ConfigServerAddresses, nil
}

//RefreshMembers is a method
func (memDis *MemDiscovery) RefreshMembers() error {
	var (
		errorStatus bool
		errorInfo   error
		count       int
	)

	endpointMap := make(map[string]bool)

	if len(memDis.ConfigServerAddresses) == 0 {
		return nil
	}

	tmpConfigAddrs := memDis.ConfigServerAddresses
	confgCenterIP := len(tmpConfigAddrs)
	instances := new(Members)
	for _, host := range tmpConfigAddrs {
		errorStatus = false
		lager.Logger.Debugf("RefreshMembers hosts ", host)
		resp, err := memDis.HTTPDo("GET", host+ConfigMembersPath, nil, nil)
		if err != nil {
			errorStatus = true
			errorInfo = err
			count++
			if confgCenterIP > count {
				errorStatus = false
			}
			lager.Logger.Error("member request failed with error", err)
			continue
		}
		var body []byte
		body, err = ioutil.ReadAll(resp.Body)
		contentType := resp.Header.Get("Content-Type")
		if len(contentType) > 0 && (len(defaultContentType) > 0 && !strings.Contains(contentType, defaultContentType)) {
			lager.Logger.Error("config source member request failed with error", errors.New("content type mis match"))
			continue
		}
		error := serializers.Decode(defaultContentType, body, &instances)
		if error != nil {
			lager.Logger.Error("config source member request failed with error", errors.New("error in decoding the request:"+error.Error()))
			lager.Logger.Debugf("config source member request failed with error", error, "with body", body)
			continue
		}
		for _, instance := range instances.Instances {
			if instance.Status != StatusUP {
				continue
			}
			for _, entryPoint := range instance.EntryPoints {
				endpointMap[entryPoint] = memDis.EnableSSL
			}
		}
	}
	if errorStatus {
		return errorInfo
	}

	memDis.Lock()
	// flush old config
	memDis.ConfigServerAddresses = make([]string, 0)
	var entryPoint string
	for endPoint, isHTTPSEnable := range endpointMap {
		parsedEndpoint := strings.Split(endPoint, `://`)
		if len(parsedEndpoint) != 2 {
			continue
		}
		if isHTTPSEnable {
			entryPoint = `https://` + parsedEndpoint[1]
		} else {
			entryPoint = `http://` + parsedEndpoint[1]
		}
		memDis.ConfigServerAddresses = append(memDis.ConfigServerAddresses, entryPoint)
	}
	memDis.Unlock()
	return nil
}

//GetDefaultHeaders gets default headers
func GetDefaultHeaders(tenantName string) http.Header {
	headers := http.Header{
		HeaderContentType: []string{"application/json"},
		HeaderUserAgent:   []string{"cse-configcenter-client/1.0.0"},
		HeaderTenantName:  []string{tenantName},
	}
	if config.MicroserviceDefinition.ServiceDescription.Environment != "" {
		headers.Set(HeaderEnvironment, config.MicroserviceDefinition.ServiceDescription.Environment)
	}

	return headers
}

//Shuffle is a method to log error
func (memDis *MemDiscovery) Shuffle() error {
	if memDis.ConfigServerAddresses == nil || len(memDis.ConfigServerAddresses) == 0 {
		err := errors.New(client.EmptyConfigServerConfig)
		lager.Logger.Error(client.EmptyConfigServerConfig, err)
		return err
	}

	perm := rand.Perm(len(memDis.ConfigServerAddresses))

	memDis.Lock()
	defer memDis.Unlock()
	lager.Logger.Debugf("Before Suffled member %s ", memDis.ConfigServerAddresses)
	for i, v := range perm {
		lager.Logger.Debugf("shuffler %d %d", i, v)
		tmp := memDis.ConfigServerAddresses[v]
		memDis.ConfigServerAddresses[v] = memDis.ConfigServerAddresses[i]
		memDis.ConfigServerAddresses[i] = tmp
	}

	lager.Logger.Debugf("Suffled member %s", memDis.ConfigServerAddresses)
	return nil
}

//GetWorkingConfigCenterIP is a method which gets working configuration center IP
func (memDis *MemDiscovery) GetWorkingConfigCenterIP(entryPoint []string) ([]string, error) {
	instances := new(Members)
	ConfigServerAddresses := make([]string, 0)
	for _, server := range entryPoint {
		resp, err := memDis.HTTPDo("GET", server+ConfigMembersPath, nil, nil)
		if err != nil {
			lager.Logger.Error("config source member request failed with error", err)
			continue
		}
		var body []byte
		body, err = ioutil.ReadAll(resp.Body)
		contentType := resp.Header.Get("Content-Type")
		if len(contentType) > 0 && (len(defaultContentType) > 0 && !strings.Contains(contentType, defaultContentType)) {
			lager.Logger.Error("config source member request failed with error", errors.New("content type mis match"))
			continue
		}
		error := serializers.Decode(defaultContentType, body, &instances)
		if error != nil {
			lager.Logger.Error("config source member request failed with error", errors.New("error in decoding the request:"+error.Error()))
			lager.Logger.Debugf("config source member request failed with error", error, "with body", body)
			continue
		}

		ConfigServerAddresses = append(ConfigServerAddresses, server)
	}
	return ConfigServerAddresses, nil
}

// PullConfigs is the implementation of ConfigClient to pull all the configurations from Config-Server
func (memDis *MemDiscovery) PullConfigs(serviceName, version, app, env string) (map[string]interface{}, error) {

	// serviceName is the dimensionInfo passed from ConfigClient (small hack)
	configurations, error := memDis.pullConfigurationsFromServer(serviceName)
	if error != nil {
		return nil, error
	}
	return configurations, nil
}

// PullConfig is the implementation of ConfigClient to pull specific configurations from Config-Server
func (memDis *MemDiscovery) PullConfig(serviceName, version, app, env, key, contentType string) (interface{}, error) {

	// serviceName is the dimensionInfo passed from ConfigClient (small hack)
	// TODO use the contentType to return the configurations
	configurations, error := memDis.pullConfigurationsFromServer(serviceName)
	if error != nil {
		return nil, error
	}
	configurationsValue, ok := configurations[key]
	if !ok {
		lager.Logger.Error("Error in fetching the configurations for particular value", errors.New("No Key found : "+key))
	}

	return configurationsValue, nil
}

// Init intializes the client
func (memDis *MemDiscovery) Init() {
	err := memDis.InitConfigCenter()
	if err != nil {
		lager.Logger.Error("Error in intializing MemberDisocvery ", err)
	}
}

// pullConfigurationsFromServer pulls all the configuration from Config-Server based on dimesionInfo
func (memDis *MemDiscovery) pullConfigurationsFromServer(dimensionInfo string) (map[string]interface{}, error) {

	var count int
	type GetConfigAPI map[string]map[string]interface{}
	config := make(map[string]interface{})

	configServerHost, err := memDis.GetConfigServer()
	if err != nil {
		err := memDis.RefreshMembers()
		if err != nil {
			lager.Logger.Error("error in refreshing config client members", err)
			return nil, errors.New("error in refreshing config client members")
		}
		memDis.Shuffle()
		configServerHost, _ = memDis.GetConfigServer()
	}

	confgCenterIP := len(configServerHost)
	for _, server := range configServerHost {
		configAPIRes := make(GetConfigAPI)
		parsedDimensionInfo := strings.Replace(dimensionInfo, "#", "%23", -1)
		resp, err := memDis.HTTPDo("GET", server+ConfigPath+"?"+dimensionsInfo+"="+parsedDimensionInfo, nil, nil)
		if err != nil {
			count++
			if confgCenterIP <= count {
				return nil, err
			}
			lager.Logger.Error("config source item request failed with error", err)
			continue
		}
		var body []byte
		body, err = ioutil.ReadAll(resp.Body)
		contentType := resp.Header.Get("Content-Type")
		if len(contentType) > 0 && (len(defaultContentType) > 0 && !strings.Contains(contentType, defaultContentType)) {
			lager.Logger.Error("config source item request failed with error", errors.New("content type mis match"))
			continue
		}
		error := serializers.Decode(defaultContentType, body, &configAPIRes)
		if error != nil {
			lager.Logger.Error("config source item request failed with error", errors.New("error in decoding the request:"+error.Error()))
			lager.Logger.Debugf("config source item request failed with error", error, "with body", body)
			continue
		}
		for _, v := range configAPIRes {
			for key, value := range v {
				config[key] = value
			}
		}
	}
	return config, nil
}

// InitConfigCenter initialize config center
func (memDis *MemDiscovery) InitConfigCenter() error {
	configCenterURL, err := isConfigCenter()
	if err != nil {
		return nil
	}

	var enableSSL bool
	tlsConfig, tlsError := getTLSForClient(configCenterURL)
	if tlsError != nil {
		lager.Logger.Errorf(tlsError, "Get %s.%s TLS config failed, err:", Name, common.Consumer)
		return tlsError
	}
	/*This condition added because member discovery can have multiple ip's with IsHTTPS
	having both true and false value.*/
	if tlsConfig != nil {
		enableSSL = true
	}
	dimensionInfo := getUniqueIDForDimInfo()
	if dimensionInfo == "" {
		err := errors.New("empty dimension info: " + emptyDimeInfo)
		lager.Logger.Error("empty dimension info", err)
		return err
	}

	if config.GlobalDefinition.Cse.Config.Client.TenantName == "" {
		config.GlobalDefinition.Cse.Config.Client.TenantName = common.DefaultTenant
	}

	if config.GlobalDefinition.Cse.Config.Client.RefreshInterval == 0 {
		config.GlobalDefinition.Cse.Config.Client.RefreshInterval = 30
	}

	err = memDis.initConfigCenter(configCenterURL,
		dimensionInfo, config.GlobalDefinition.Cse.Config.Client.TenantName,
		enableSSL, tlsConfig)
	if err != nil {
		lager.Logger.Error("failed to init config center", err)
		return err
	}

	lager.Logger.Warnf("config center init success")
	return nil
}

func isConfigCenter() (string, error) {
	configCenterURL := config.GlobalDefinition.Cse.Config.Client.ServerURI
	if configCenterURL == "" {
		ccURL, err := endpoint.GetEndpointFromServiceCenter("default", "CseConfigCenter", "latest")
		if err != nil {
			lager.Logger.Errorf(err, "empty config center endpoint, please provide the config center endpoint")
			return "", err
		}
		configCenterURL = ccURL
	}
	return configCenterURL, nil
}

func getTLSForClient(configCenterURL string) (*tls.Config, error) {
	if !strings.Contains(configCenterURL, "://") {
		return nil, nil
	}
	ccURL, err := url.Parse(configCenterURL)
	if err != nil {
		lager.Logger.Error("Error occurred while parsing config center Server Uri", err)
		return nil, err
	}
	if ccURL.Scheme == common.HTTP {
		return nil, nil
	}

	sslTag := Name + "." + common.Consumer
	tlsConfig, sslConfig, err := chassisTLS.GetTLSConfigByService(Name, "", common.Consumer)
	if err != nil {
		if chassisTLS.IsSSLConfigNotExist(err) {
			return nil, fmt.Errorf("%s TLS mode, but no ssl config", sslTag)
		}
		return nil, err
	}
	lager.Logger.Warnf("%s TLS mode, verify peer: %t, cipher plugin: %s.",
		sslTag, sslConfig.VerifyPeer, sslConfig.CipherPlugin)

	return tlsConfig, nil
}

func getUniqueIDForDimInfo() string {
	serviceName := config.MicroserviceDefinition.ServiceDescription.Name
	version := config.MicroserviceDefinition.ServiceDescription.Version
	appName := config.MicroserviceDefinition.AppID
	if appName == "" {
		appName = config.GlobalDefinition.AppID
	}

	if appName != "" {
		serviceName = serviceName + "@" + appName
	}

	if version != "" {
		serviceName = serviceName + "#" + version
	}

	if len(serviceName) > maxValue {
		lager.Logger.Errorf(nil, "exceeded max value %d for dimensionInfo %s with length %d", maxValue, serviceName,
			len(serviceName))
		return ""
	}

	dimeExp := `\A([^\$\%\&\+\(/)\[\]\" "\"])*\z`
	dimRegexVar, err := regexp.Compile(dimeExp)
	if err != nil {
		lager.Logger.Error("not a valid regular expression", err)
		return ""
	}

	if !dimRegexVar.Match([]byte(serviceName)) {
		lager.Logger.Errorf(nil, "invalid value for dimension info, doesnot setisfy the regular expression for dimInfo:%s",
			serviceName)
		return ""
	}

	return serviceName
}

func (memDis *MemDiscovery) initConfigCenter(ccEndpoint, dimensionInfo, tenantName string, enableSSL bool, tlsConfig *tls.Config) error {
	memDiscovery := memDis.NewConfiCenterInit(tlsConfig, tenantName, enableSSL)

	configCenters := strings.Split(ccEndpoint, ",")
	cCenters := make([]string, 0)
	for _, value := range configCenters {
		value = strings.Replace(value, " ", "", -1)
		cCenters = append(cCenters, value)
	}

	memDiscovery.ConfigurationInit(cCenters)

	if enbledAutoDiscovery() {
		refreshError := memDiscovery.RefreshMembers()
		if refreshError != nil {
			lager.Logger.Error(client.ConfigServerMemRefreshError, refreshError)
			return errors.New(client.ConfigServerMemRefreshError)
		}
	}
	return nil
}

//EventListener is a struct
type EventListener struct {
	Name    string
	Factory goarchaius.ConfigurationFactory
}

//Event is a method
func (e EventListener) Event(event *core.Event) {
	value := e.Factory.GetConfigurationByKey(event.Key)
	lager.Logger.Infof("config value %s | %s", event.Key, value)
}

func enbledAutoDiscovery() bool {
	if config.GlobalDefinition.Cse.Config.Client.Autodiscovery {
		return true
	}
	return false
}

// PullConfigsByDI pulls the configuration for custom DimensionInfo
func (memDis *MemDiscovery) PullConfigsByDI(dimensionInfo, diInfo string) (map[string]map[string]interface{}, error) {
	// update dimensionInfo value
	type GetConfigAPI map[string]map[string]interface{}

	var (
		count int
	)
	configAPIRes := make(GetConfigAPI)
	configServerHost, err := memDis.GetConfigServer()
	if err != nil {
		err := memDis.RefreshMembers()
		if err != nil {
			lager.Logger.Error("error in refreshing config client members", err)
			return nil, errors.New("error in refreshing config client members")
		}
		memDis.Shuffle()
		configServerHost, _ = memDis.GetConfigServer()
	}

	confgCenterIP := len(configServerHost)
	for _, server := range configServerHost {
		parsedDimensionInfo := strings.Replace(diInfo, "#", "%23", -1)
		resp, err := memDis.HTTPDo("GET", server+ConfigPath+"?"+dimensionsInfo+"="+parsedDimensionInfo, nil, nil)
		if err != nil {
			count++
			if confgCenterIP <= count {
				return nil, err
			}
			lager.Logger.Error("config source item request failed with error", err)
			continue
		}
		var body []byte
		body, err = ioutil.ReadAll(resp.Body)
		contentType := resp.Header.Get("Content-Type")
		if len(contentType) > 0 && (len(defaultContentType) > 0 && !strings.Contains(contentType, defaultContentType)) {
			lager.Logger.Error("config source item request failed with error", errors.New("content type mis match"))
			continue
		}
		error := serializers.Decode(defaultContentType, body, &configAPIRes)
		if error != nil {
			lager.Logger.Error("config source item request failed with error", errors.New("error in decoding the request:"+error.Error()))
			lager.Logger.Debugf("config source item request failed with error", error, "with body", body)
			continue
		}
	}
	return configAPIRes, nil
}
