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

package appcontext

import (
	syscontext "context"
	"errors"
	"fmt"
	"time"

	"github.com/edgexfoundry/go-mod-core-contracts/clients"
	"github.com/edgexfoundry/go-mod-core-contracts/clients/command"
	"github.com/edgexfoundry/go-mod-core-contracts/clients/coredata"
	"github.com/edgexfoundry/go-mod-core-contracts/clients/logger"
	"github.com/edgexfoundry/go-mod-core-contracts/clients/notifications"
	"github.com/edgexfoundry/go-mod-core-contracts/models"
	"github.com/google/uuid"
	"github.com/tuanldchainos/app-functions-sdk-go/internal/common"
	"github.com/tuanldchainos/app-functions-sdk-go/internal/security"
	"github.com/tuanldchainos/app-functions-sdk-go/pkg/util"
)

// AppFunction is a type alias for func(edgexcontext *appcontext.Context, params ...interface{}) (bool, interface{})
type AppFunction = func(edgexcontext *Context, params ...interface{}) (bool, interface{})

// Context ...
type Context struct {
	// ID of the EdgeX Event -- will be filled for a received JSON Event
	EventID string
	// Checksum of the EdgeX Event -- will be filled for a received CBOR Event
	EventChecksum string
	// This is the ID used to track the EdgeX event through entire EdgeX framework.
	CorrelationID string
	// OutputData is used for specifying the data that is to be outputted. Leverage the .Complete() function to set.
	OutputData []byte
	// This holds the configuration for your service. This is the preferred way to access your custom application settings that have been set in the configuration.
	Configuration common.ConfigurationStruct
	// LoggingClient is exposed to allow logging following the preferred logging strategy within EdgeX.
	LoggingClient logger.LoggingClient
	// EventClient exposes Core Data's EventClient API
	EventClient coredata.EventClient
	// ValueDescriptorClient exposes Core Data's ValueDescriptor API
	ValueDescriptorClient coredata.ValueDescriptorClient
	// CommandClient exposes Core Commands's Command API
	CommandClient command.CommandClient
	// NotificationsClient exposes Support Notification's Notifications API
	NotificationsClient notifications.NotificationsClient
	// RetryData holds the data to be stored for later retry when the pipeline function returns an error
	RetryData []byte
	// SecretProvider exposes the support for getting and storing secrets
	SecretProvider *security.SecretProvider
}

// Complete is optional and provides a way to return the specified data.
// In the case of an HTTP Trigger, the data will be returned as the http response.
// In the case of the message bus trigger, the data will be placed on the specifed
// message bus publish topic and host in the configuration.
func (context *Context) Complete(output []byte) {
	context.OutputData = output
}

// MarkAsPushed will make a request to CoreData to mark the event that triggered the pipeline as pushed.
func (context *Context) MarkAsPushed() error {
	context.LoggingClient.Debug("Marking event as pushed")
	if context.EventClient == nil {
		return fmt.Errorf("unable to Mark As Pushed: '%s' is missing from Clients configuration", common.CoreDataClientName)
	}

	if context.EventID != "" {
		return context.EventClient.MarkPushed(syscontext.WithValue(syscontext.Background(), clients.CorrelationHeader, context.CorrelationID), context.EventID)
	} else if context.EventChecksum != "" {
		return context.EventClient.MarkPushedByChecksum(syscontext.WithValue(syscontext.Background(), clients.CorrelationHeader, context.CorrelationID), context.EventChecksum)
	} else {
		return errors.New("No EventID or EventChecksum Provided")
	}
}

// SetRetryData sets the RetryData to the specified payload to be stored for later retry
// when the pipeline function returns an error.
func (context *Context) SetRetryData(payload []byte) {
	context.RetryData = payload
}

// PushToCoreData pushes the provided value as an event to CoreData using the device name and reading name that have been set. If validation is turned on in
// CoreServices then your deviceName and readingName must exist in the CoreMetadata and be properly registered in EdgeX.
func (context *Context) PushToCoreData(deviceName string, readingName string, value interface{}) (*models.Event, error) {
	context.LoggingClient.Debug("Pushing to CoreData")
	now := time.Now().UnixNano()
	val, err := util.CoerceType(value)
	if err != nil {
		return nil, err
	}
	newReading := models.Reading{
		Value:  string(val),
		Origin: now,
		Device: deviceName,
		Name:   readingName,
	}

	readings := make([]models.Reading, 0, 1)
	readings = append(readings, newReading)

	newEdgeXEvent := &models.Event{
		Device:   deviceName,
		Origin:   now,
		Readings: readings,
	}

	correlation := uuid.New().String()
	ctx := syscontext.WithValue(syscontext.Background(), clients.CorrelationHeader, correlation)
	result, err := context.EventClient.Add(ctx, newEdgeXEvent)
	if err != nil {
		return nil, err
	}
	newEdgeXEvent.ID = result
	return newEdgeXEvent, nil
}

// GetSecrets retrieves secrets from a secret store.
// path specifies the type or location of the secrets to retrieve.
// keys specifies the secrets which to retrieve. If no keys are provided then all the keys associated with the
// specified path will be returned.
func (context *Context) GetSecrets(path string, keys ...string) (map[string]string, error) {
	return context.SecretProvider.GetSecrets(path, keys...)
}
