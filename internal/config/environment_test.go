/*******************************************************************************
 * Copyright 2019 Dell Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License. You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software distributed under the License
 * is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express
 * or implied. See the License for the specific language governing permissions and limitations under
 * the License.
 *******************************************************************************/

package config

import (
	"os"
	"strconv"
	"testing"

	"github.com/pelletier/go-toml"
	"github.com/stretchr/testify/assert"
	"github.com/tuanldchainos/app-functions-sdk-go/internal/common"
)

const (
	envValue  = "envValue"
	rootKey   = "rootKey"
	rootValue = "rootValue"
	sub       = "sub"
	subKey    = "subKey"
	subValue  = "subValue"

	testToml = `
` + rootKey + `="` + rootValue + `"
[` + sub + `]
` + subKey + `="` + subValue + `"`
)

func newSUT(t *testing.T, env map[string]string) *environment {
	os.Clearenv()
	for k, v := range env {
		if err := os.Setenv(k, v); err != nil {
			t.Fail()
		}
	}
	return NewEnvironment()
}

func newOverrideFromEnvironmentSUT(t *testing.T, envKey string, envValue string) (*toml.Tree, *environment) {
	tree, err := toml.Load(testToml)
	if err != nil {
		t.Fail()
	}
	return tree, newSUT(t, map[string]string{envKey: envValue})
}

func TestKeyMatchOverwritesValue(t *testing.T) {
	var tests = []struct {
		name          string
		key           string
		envKey        string
		envValue      string
		expectedValue string
	}{
		{"generic root", rootKey, rootKey, envValue, envValue},
		{"generic sub", sub + "." + subKey, sub + "." + subKey, envValue, envValue},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tree, sut := newOverrideFromEnvironmentSUT(t, test.key, test.envValue)

			result := sut.OverrideFromEnvironment(tree)

			assert.Equal(t, test.envValue, result.Get(test.key))
		})
	}
}

func TestNonMatchingKeyDoesNotOverwritesValue(t *testing.T) {
	var tests = []struct {
		name          string
		key           string
		envKey        string
		envValue      string
		expectedValue string
	}{
		{"root", rootKey, rootKey, envValue, rootValue},
		{"sub", sub + "." + subKey, sub + "." + subKey, envValue, rootValue},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tree, sut := newOverrideFromEnvironmentSUT(t, test.key, test.envValue)

			result := sut.OverrideFromEnvironment(tree)

			assert.Equal(t, test.envValue, result.Get(test.key))
		})
	}
}

const (
	expectedRegistryTypeValue = "consul"
	expectedRegistryHostValue = "localhost"
	expectedRegistryPortValue = 8500

	expectedServiceProtocolValue = "http"
	expectedServiceHostValue     = "localhost"
	expectedServicePortValue     = 8500

	defaultHostValue = "defaultHost"
	defaultPortValue = 987654321
	defaultTypeValue = "defaultType"
)

func initializeTest(t *testing.T) common.RegistryInfo {
	os.Clearenv()
	return common.RegistryInfo{
		Host: defaultHostValue,
		Port: defaultPortValue,
		Type: defaultTypeValue,
	}
}

func TestEnvVariableUpdatesRegistryInfo(t *testing.T) {
	registryInfo := initializeTest(t)
	sut := newSUT(t, map[string]string{envKeyRegistryUrl: expectedRegistryTypeValue + "://" + expectedRegistryHostValue + ":" + strconv.Itoa(expectedRegistryPortValue)})

	registryInfo = sut.OverrideRegistryInfoFromEnvironment(registryInfo)

	assert.Equal(t, expectedRegistryHostValue, registryInfo.Host)
	assert.Equal(t, expectedRegistryPortValue, registryInfo.Port)
	assert.Equal(t, expectedRegistryTypeValue, registryInfo.Type)
}

func TestEnvVariableUpdatesServiceInfo(t *testing.T) {
	os.Clearenv()
	serviceInfo := common.ServiceInfo{
		Host:     defaultHostValue,
		Port:     defaultPortValue,
		Protocol: defaultTypeValue,
	}
	sut := newSUT(t, map[string]string{envKeyServiceUrl: expectedServiceProtocolValue + "://" + expectedServiceHostValue + ":" + strconv.Itoa(expectedServicePortValue)})

	serviceInfo = sut.OverrideServiceInfoFromEnvironment(serviceInfo)

	assert.Equal(t, expectedServiceHostValue, serviceInfo.Host)
	assert.Equal(t, expectedServicePortValue, serviceInfo.Port)
	assert.Equal(t, expectedServiceProtocolValue, serviceInfo.Protocol)
}

func TestNoEnvVariableDoesNotUpdateRegistryInfo(t *testing.T) {
	registryInfo := initializeTest(t)
	sut := newSUT(t, map[string]string{})

	registryInfo = sut.OverrideRegistryInfoFromEnvironment(registryInfo)

	assert.Equal(t, defaultHostValue, registryInfo.Host)
	assert.Equal(t, defaultPortValue, registryInfo.Port)
	assert.Equal(t, defaultTypeValue, registryInfo.Type)
}
