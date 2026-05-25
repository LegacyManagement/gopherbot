package bot

import (
	"testing"
	"time"

	"github.com/lnxjedi/gopherbot/robot"
)

type startupGateCaptureConnector struct {
	user string
	msg  string
}

func (c *startupGateCaptureConnector) GetProtocolUserAttribute(user, attr string) (string, robot.RetVal) {
	return "", robot.AttributeNotFound
}
func (c *startupGateCaptureConnector) MessageHeard(user, channel string)  {}
func (c *startupGateCaptureConnector) DefaultHelp() []string              { return nil }
func (c *startupGateCaptureConnector) JoinChannel(ch string) robot.RetVal { return robot.Ok }
func (c *startupGateCaptureConnector) SendProtocolChannelThreadMessage(channel, thread, msg string, format robot.MessageFormat, msgObject *robot.ConnectorMessage) robot.RetVal {
	c.msg = msg
	return robot.Ok
}
func (c *startupGateCaptureConnector) SendProtocolUserChannelThreadMessage(user, username, channel, thread, msg string, format robot.MessageFormat, msgObject *robot.ConnectorMessage) robot.RetVal {
	c.user = user
	c.msg = msg
	return robot.Ok
}
func (c *startupGateCaptureConnector) SendProtocolUserMessage(user, msg string, format robot.MessageFormat, msgObject *robot.ConnectorMessage) robot.RetVal {
	c.user = user
	c.msg = msg
	return robot.Ok
}
func (c *startupGateCaptureConnector) Reload() error                   { return nil }
func (c *startupGateCaptureConnector) Run(stopchannel <-chan struct{}) {}

func TestIncomingCommandDuringStartupGateGetsStartingMessage(t *testing.T) {
	conn := &startupGateCaptureConnector{}
	oldConnector := interfaces.Connector
	interfaces.Connector = conn
	defer func() { interfaces.Connector = oldConnector }()

	currentCfg.Lock()
	oldCfg := currentCfg.configuration
	oldTasks := currentCfg.taskList
	oldIgnoreUnlisted := currentCfg.ignoreUnlistedUsers
	currentCfg.configuration = &configuration{}
	currentCfg.taskList = &taskList{}
	currentCfg.ignoreUnlistedUsers = false
	currentCfg.Unlock()
	currentUCMaps.Lock()
	oldMaps := currentUCMaps.ucmap
	currentUCMaps.ucmap = &userChanMaps{
		userIDProto:   map[string]map[string]*UserInfo{},
		directoryUser: map[string]bool{"alice": true},
		user:          map[string]*DirectoryUser{},
	}
	currentUCMaps.Unlock()
	defer func() {
		currentCfg.Lock()
		currentCfg.configuration = oldCfg
		currentCfg.taskList = oldTasks
		currentCfg.ignoreUnlistedUsers = oldIgnoreUnlisted
		currentCfg.Unlock()
		currentUCMaps.Lock()
		currentUCMaps.ucmap = oldMaps
		currentUCMaps.Unlock()
	}()

	resetStartupGate()
	defer releaseStartupGate()
	handle.IncomingMessage(&robot.ConnectorMessage{
		Protocol:      "test",
		UserName:      "alice",
		UserID:        "u1",
		ValidatedUser: true,
		DirectMessage: true,
		MessageText:   "help",
	})

	deadline := time.After(100 * time.Millisecond)
	for conn.msg == "" {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for startup gate response")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if conn.msg != startupGateMessage {
		t.Fatalf("startup gate message = %q, want %q", conn.msg, startupGateMessage)
	}
	if conn.user != "<u1>" {
		t.Fatalf("startup gate response user = %q, want <u1>", conn.user)
	}
}
