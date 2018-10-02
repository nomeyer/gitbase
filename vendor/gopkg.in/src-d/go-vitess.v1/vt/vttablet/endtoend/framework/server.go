/*
Copyright 2017 Google Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package framework

import (
	"fmt"
	"net"
	"net/http"
	"time"

	"golang.org/x/net/context"

	"gopkg.in/src-d/go-vitess.v1/mysql"
	"gopkg.in/src-d/go-vitess.v1/vt/dbconfigs"
	"gopkg.in/src-d/go-vitess.v1/vt/vtgate/fakerpcvtgateconn"
	"gopkg.in/src-d/go-vitess.v1/vt/vtgate/vtgateconn"
	"gopkg.in/src-d/go-vitess.v1/vt/vttablet/tabletserver"
	"gopkg.in/src-d/go-vitess.v1/vt/vttablet/tabletserver/tabletenv"

	querypb "gopkg.in/src-d/go-vitess.v1/vt/proto/query"
	topodatapb "gopkg.in/src-d/go-vitess.v1/vt/proto/topodata"
)

var (
	// Target is the target info for the server.
	Target querypb.Target
	// Server is the TabletServer for the framework.
	Server *tabletserver.TabletServer
	// ServerAddress is the http URL for the server.
	ServerAddress string
	// ResolveChan is the channel that sends dtids that are to be resolved.
	ResolveChan = make(chan string, 1)
)

// StartServer starts the server and initializes
// all the global variables. This function should only be called
// once at the beginning of the test.
func StartServer(connParams, connAppDebugParams mysql.ConnParams, dbName string) error {
	// Setup a fake vtgate server.
	protocol := "resolveTest"
	*vtgateconn.VtgateProtocol = protocol
	vtgateconn.RegisterDialer(protocol, func(context.Context, string) (vtgateconn.Impl, error) {
		return &txResolver{
			FakeVTGateConn: fakerpcvtgateconn.FakeVTGateConn{},
		}, nil
	})

	dbcfgs := dbconfigs.NewTestDBConfigs(connParams, connAppDebugParams, dbName)

	config := tabletenv.DefaultQsConfig
	config.EnableAutoCommit = true
	config.StrictTableACL = true
	config.TwoPCEnable = true
	config.TwoPCAbandonAge = 1
	config.TwoPCCoordinatorAddress = "fake"
	config.EnableHotRowProtection = true

	Target = querypb.Target{
		Keyspace:   "vttest",
		Shard:      "0",
		TabletType: topodatapb.TabletType_MASTER,
	}

	Server = tabletserver.NewTabletServerWithNilTopoServer(config)
	Server.Register()
	err := Server.StartService(Target, dbcfgs)
	if err != nil {
		return fmt.Errorf("could not start service: %v", err)
	}

	// Start http service.
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		return fmt.Errorf("could not start listener: %v", err)
	}
	ServerAddress = fmt.Sprintf("http://%s", ln.Addr().String())
	go http.Serve(ln, nil)
	for {
		time.Sleep(10 * time.Millisecond)
		response, err := http.Get(fmt.Sprintf("%s/debug/vars", ServerAddress))
		if err == nil {
			response.Body.Close()
			break
		}
	}
	return nil
}

// StopServer must be called once all the tests are done.
func StopServer() {
	Server.StopService()
}

// txReolver transmits dtids to be resolved through ResolveChan.
type txResolver struct {
	fakerpcvtgateconn.FakeVTGateConn
}

func (conn *txResolver) ResolveTransaction(ctx context.Context, dtid string) error {
	select {
	case ResolveChan <- dtid:
	default:
	}
	return nil
}
