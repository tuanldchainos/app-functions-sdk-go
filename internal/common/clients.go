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

package common

import (
	"github.com/edgexfoundry/go-mod-core-contracts/clients/agent"
	"github.com/edgexfoundry/go-mod-core-contracts/clients/command"
	"github.com/edgexfoundry/go-mod-core-contracts/clients/coredata"
	"github.com/edgexfoundry/go-mod-core-contracts/clients/logger"
	"github.com/edgexfoundry/go-mod-core-contracts/clients/metadata"
	"github.com/edgexfoundry/go-mod-core-contracts/clients/notifications"
	"github.com/edgexfoundry/go-mod-core-contracts/clients/scheduler"
)

const (
	LoggingClientName       = "Logging"
	CoreCommandClientName   = "Command"
	CoreDataClientName      = "CoreData"
	NotificationsClientName = "Notifications"
	MetadataClientName      = "Metadata"
	SchedulerClientName     = "Scheduler"
	AgentClientName         = "Agent"
)

type EdgeXClients struct {
	LoggingClient          logger.LoggingClient
	EventClient            coredata.EventClient
	ReadingClient          coredata.ReadingClient
	CommandClient          command.CommandClient
	ValueDescriptorClient  coredata.ValueDescriptorClient
	NotificationsClient    notifications.NotificationsClient
	AddressableClient      metadata.AddressableClient
	DeviceClient           metadata.DeviceClient
	ProvisionWatcherClient metadata.ProvisionWatcherClient
	IntervalClient         scheduler.IntervalClient
	IntervalActionClient   scheduler.IntervalActionClient
	AgentClient            agent.AgentClient
}
