/********************************************************************************
 *  Copyright 2019 Dell Inc.
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

package security

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/tuanldchainos/app-functions-sdk-go/internal/common"
	"github.com/tuanldchainos/app-functions-sdk-go/internal/security/authtokenloader"
	"github.com/tuanldchainos/app-functions-sdk-go/internal/security/client"
	"github.com/tuanldchainos/app-functions-sdk-go/internal/security/fileioperformer"

	"github.com/edgexfoundry/go-mod-core-contracts/clients/logger"

	"github.com/edgexfoundry/go-mod-secrets/pkg"
	"github.com/edgexfoundry/go-mod-secrets/pkg/providers/vault"
)

// SecretProvider cache storage for the secrets
type SecretProvider struct {
	secretClient  pkg.SecretClient
	secretsCache  map[string]map[string]string // secret's path, key, value
	configuration *common.ConfigurationStruct
	cacheMuxtex   *sync.Mutex
	loggingClient logger.LoggingClient
}

// NewSecretProvider returns a new secret provider
func NewSecretProvider(loggingClient logger.LoggingClient, configuration *common.ConfigurationStruct) *SecretProvider {
	sp := &SecretProvider{
		secretsCache:  make(map[string]map[string]string),
		cacheMuxtex:   &sync.Mutex{},
		configuration: configuration,
		loggingClient: loggingClient,
	}

	return sp
}

// Initialize creates a SecretClient to be used for obtaining secrets from a secrets store manager.
func (s *SecretProvider) Initialize(ctx context.Context) bool {
	secretConfig, err := s.getSecretConfig(s.configuration.SecretStore)
	if err != nil {
		s.loggingClient.Error(fmt.Sprintf("unable to parse secret store configuration: %s", err.Error()))
		return false
	}

	// attempt to create a new SecretProvider client only if security is enabled.
	if s.isSecurityEnabled() {
		s.secretClient, err = client.NewVault(ctx, secretConfig, s.loggingClient).Get(s.configuration.SecretStore)
		if err != nil {
			s.loggingClient.Error(fmt.Sprintf("unable to create SecretClient: %s", err.Error()))
			return false
		}
	}

	return true
}

// getSecretConfig creates a SecretConfig based on the SecretStoreInfo configuration properties.
// If a tokenfile is present it will override the Authentication.AuthToken value.
func (s *SecretProvider) getSecretConfig(secretStoreInfo common.SecretStoreInfo) (vault.SecretConfig, error) {
	secretConfig := vault.SecretConfig{
		Host:           secretStoreInfo.Host,
		Port:           secretStoreInfo.Port,
		Path:           secretStoreInfo.Path,
		Protocol:       secretStoreInfo.Protocol,
		Namespace:      secretStoreInfo.Namespace,
		RootCaCertPath: secretStoreInfo.RootCaCertPath,
		ServerName:     secretStoreInfo.ServerName,
		Authentication: secretStoreInfo.Authentication,
	}

	if !s.isSecurityEnabled() || secretStoreInfo.TokenFile == "" {
		return secretConfig, nil
	}

	// only bother getting a token if security is enabled and the configuration-provided tokenfile is not empty.
	fileIoPerformer := fileioperformer.NewDefaultFileIoPerformer()
	authTokenLoader := authtokenloader.NewAuthTokenLoader(fileIoPerformer)

	token, err := authTokenLoader.Load(secretStoreInfo.TokenFile)
	if err != nil {
		return secretConfig, err
	}
	secretConfig.Authentication.AuthToken = token
	return secretConfig, nil
}

// isSecurityEnabled determines if security has been enabled.
func (s *SecretProvider) isSecurityEnabled() bool {
	env := os.Getenv("EDGEX_SECURITY_SECRET_STORE")
	return env != "false"
}
