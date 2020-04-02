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

package runtime

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/tuanldchainos/app-functions-sdk-go/appcontext"
	"github.com/tuanldchainos/app-functions-sdk-go/internal/common"
	"github.com/tuanldchainos/app-functions-sdk-go/internal/store/contracts"
	"github.com/tuanldchainos/app-functions-sdk-go/internal/store/db/interfaces"
	"github.com/tuanldchainos/app-functions-sdk-go/internal/store/db/interfaces/mocks"
	"github.com/tuanldchainos/app-functions-sdk-go/pkg/transforms"
)

func TestProcessRetryItems(t *testing.T) {

	targetTransformWasCalled := false
	expectedPayload := "This is a sample payload"

	config := common.ConfigurationStruct{
		Writable: common.WritableInfo{
			LogLevel:        "DEBUG",
			StoreAndForward: common.StoreAndForwardInfo{MaxRetryCount: 10},
		},
	}

	transformPassthru := func(edgexcontext *appcontext.Context, params ...interface{}) (bool, interface{}) {
		return true, params[0]
	}

	successTransform := func(edgexcontext *appcontext.Context, params ...interface{}) (bool, interface{}) {
		targetTransformWasCalled = true

		actualPayload, ok := params[0].([]byte)

		require.True(t, ok, "Expected []byte payload")
		require.Equal(t, expectedPayload, string(actualPayload))

		return false, nil
	}

	failureTransform := func(edgexcontext *appcontext.Context, params ...interface{}) (bool, interface{}) {
		targetTransformWasCalled = true
		return false, errors.New("I failed")
	}

	runtime := GolangRuntime{}

	tests := []struct {
		Name                     string
		TargetTransform          appcontext.AppFunction
		TargetTransformWasCalled bool
		ExpectedPayload          string
		RetryCount               int
		ExpectedRetryCount       int
		RemoveCount              int
		BadVersion               bool
	}{
		{"Happy Path", successTransform, true, expectedPayload, 0, 0, 1, false},
		{"RetryCount Increased", failureTransform, true, expectedPayload, 4, 5, 0, false},
		{"Max Retries", failureTransform, true, expectedPayload, 9, 9, 1, false},
		{"Bad Version", successTransform, false, expectedPayload, 0, 0, 1, true},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			targetTransformWasCalled = false

			runtime.Initialize(creatMockStoreClient(), nil)
			runtime.SetTransforms([]appcontext.AppFunction{transformPassthru, transformPassthru, test.TargetTransform})

			version := runtime.storeForward.pipelineHash
			if test.BadVersion {
				version = "some bad version"
			}
			storedObject := contracts.NewStoredObject("dummy", []byte(test.ExpectedPayload), 2, version)
			storedObject.RetryCount = test.RetryCount

			removes, updates := runtime.storeForward.processRetryItems([]contracts.StoredObject{storedObject}, &config, common.EdgeXClients{LoggingClient: lc})
			assert.Equal(t, test.TargetTransformWasCalled, targetTransformWasCalled, "Target transform not called")
			if test.RetryCount != test.ExpectedRetryCount {
				if assert.True(t, len(updates) > 0, "Remove count not as expected") {
					assert.Equal(t, test.ExpectedRetryCount, updates[0].RetryCount, "Retry Count not as expected")
				}
			}
			assert.Equal(t, test.RemoveCount, len(removes), "Remove count not as expected")
		})
	}
}

func TestDoStoreAndForwardRetry(t *testing.T) {
	serviceKey := "AppService-UnitTest"
	payload := []byte("My Payload")
	config := common.ConfigurationStruct{
		Writable: common.WritableInfo{
			LogLevel:        "DEBUG",
			StoreAndForward: common.StoreAndForwardInfo{MaxRetryCount: 10}},
	}

	httpPost := transforms.NewHTTPSender("http://nowhere", "", true).HTTPPost
	successTransform := func(edgexcontext *appcontext.Context, params ...interface{}) (bool, interface{}) {
		return false, nil
	}
	transformPassthru := func(edgexcontext *appcontext.Context, params ...interface{}) (bool, interface{}) {
		return true, params[0]
	}

	tests := []struct {
		Name                string
		TargetTransform     appcontext.AppFunction
		RetryCount          int
		ExpectedRetryCount  int
		ExpectedObjectCount int
	}{
		{"RetryCount Increased", httpPost, 1, 2, 1},
		{"Max Retries", httpPost, 9, 0, 0},
		{"Retry Success", successTransform, 1, 0, 0},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			runtime := GolangRuntime{ServiceKey: serviceKey}
			runtime.Initialize(creatMockStoreClient(), nil)
			runtime.SetTransforms([]appcontext.AppFunction{transformPassthru, test.TargetTransform})

			object := contracts.NewStoredObject(serviceKey, payload, 1, runtime.storeForward.calculatePipelineHash())
			object.CorrelationID = "CorrelationID"
			object.EventID = "CorrelationID"
			object.EventChecksum = "CorrelationID"
			object.RetryCount = test.RetryCount

			mockStoreObject(object)

			// Target of this test
			runtime.storeForward.retryStoredData(serviceKey, &config, common.EdgeXClients{LoggingClient: lc})

			objects := mockRetrieveObjects(serviceKey)
			if assert.Equal(t, test.ExpectedObjectCount, len(objects)) && test.ExpectedObjectCount > 0 {
				assert.Equal(t, test.ExpectedRetryCount, objects[0].RetryCount)
				assert.Equal(t, serviceKey, objects[0].AppServiceKey, "AppServiceKey not as expected")
				assert.Equal(t, object.CorrelationID, objects[0].CorrelationID, "CorrelationID not as expected")
				assert.Equal(t, object.EventID, objects[0].EventID, "EventID not as expected")
				assert.Equal(t, object.EventChecksum, objects[0].EventChecksum, "EventChecksum not as expected")
			}
		})
	}
}

var mockObjectStore map[string]contracts.StoredObject

func creatMockStoreClient() interfaces.StoreClient {
	mockObjectStore = make(map[string]contracts.StoredObject)
	storeClient := &mocks.StoreClient{}
	storeClient.Mock.On("Store", mock.Anything).Return(mockStoreObject)
	storeClient.Mock.On("RemoveFromStore", mock.Anything).Return(mockRemoveObject)
	storeClient.Mock.On("Update", mock.Anything).Return(mockUpdateObject)
	storeClient.Mock.On("RetrieveFromStore", mock.Anything).Return(mockRetrieveObjects, nil)

	return storeClient
}

func mockStoreObject(object contracts.StoredObject) (string, error) {
	if err := validateContract(false, object); err != nil {
		return "", err
	}

	if object.ID == "" {
		object.ID = uuid.New().String()
	}

	mockObjectStore[object.ID] = object

	return object.ID, nil
}

func mockUpdateObject(object contracts.StoredObject) error {

	if err := validateContract(true, object); err != nil {
		return err
	}

	mockObjectStore[object.ID] = object
	return nil
}

func mockRemoveObject(object contracts.StoredObject) error {
	if err := validateContract(true, object); err != nil {
		return err
	}

	delete(mockObjectStore, object.ID)
	return nil
}

func mockRetrieveObjects(serviceKey string) []contracts.StoredObject {
	var objects []contracts.StoredObject
	for _, item := range mockObjectStore {
		if item.AppServiceKey == serviceKey {
			objects = append(objects, item)
		}
	}

	return objects
}

// TODO remove this and use verify func on StoredObject when it is available
func validateContract(IDRequired bool, o contracts.StoredObject) error {
	if IDRequired {
		if o.ID == "" {
			return errors.New("invalid contract, ID cannot be empty")
		}
	}
	if o.AppServiceKey == "" {
		return errors.New("invalid contract, app service key cannot be empty")
	}
	if len(o.Payload) == 0 {
		return errors.New("invalid contract, payload cannot be empty")
	}
	if o.Version == "" {
		return errors.New("invalid contract, version cannot be empty")
	}

	return nil
}
