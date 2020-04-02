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

package transforms

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/tuanldchainos/app-functions-sdk-go/pkg/util"

	"github.com/edgexfoundry/go-mod-core-contracts/clients"
	"github.com/tuanldchainos/app-functions-sdk-go/appcontext"
)

// HTTPSender ...
type HTTPSender struct {
	URL            string
	MimeType       string
	PersistOnError bool
}

// NewHTTPSender creates, initializes and returns a new instance of HTTPSender
func NewHTTPSender(url string, mimeType string, persistOnError bool) HTTPSender {
	return HTTPSender{
		URL:            url,
		MimeType:       mimeType,
		PersistOnError: persistOnError,
	}
}

// HTTPPost will send data from the previous function to the specified Endpoint via http POST.
// If no previous function exists, then the event that triggered the pipeline will be used.
// An empty string for the mimetype will default to application/json.
func (sender HTTPSender) HTTPPost(edgexcontext *appcontext.Context, params ...interface{}) (bool, interface{}) {
	if len(params) < 1 {
		// We didn't receive a result
		return false, errors.New("No Data Received")
	}
	if sender.MimeType == "" {
		sender.MimeType = "application/json"
	}
	exportData, err := util.CoerceType(params[0])
	if err != nil {
		return false, err
	}

	edgexcontext.LoggingClient.Debug("POSTing data")
	response, err := http.Post(sender.URL, sender.MimeType, bytes.NewReader(exportData))
	if err != nil {
		sender.setRetryData(edgexcontext, exportData)
		return false, err
	}
	defer response.Body.Close()
	edgexcontext.LoggingClient.Debug(fmt.Sprintf("Response: %s", response.Status))
	edgexcontext.LoggingClient.Debug(fmt.Sprintf("Sent data: %s", string(exportData)))
	bodyBytes, errReadingBody := ioutil.ReadAll(response.Body)
	if errReadingBody != nil {
		sender.setRetryData(edgexcontext, exportData)
		return false, errReadingBody
	}

	edgexcontext.LoggingClient.Trace("Data exported", "Transport", "HTTP", clients.CorrelationHeader, edgexcontext.CorrelationID)

	// continues the pipeline if we get a 2xx response, stops pipeline if non-2xx response
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		sender.setRetryData(edgexcontext, exportData)
		return false, fmt.Errorf("export failed with %d HTTP status code", response.StatusCode)
	}

	return true, bodyBytes

}

func (sender HTTPSender) setRetryData(ctx *appcontext.Context, exportData []byte) {
	if sender.PersistOnError {
		ctx.RetryData = exportData
	}
}
