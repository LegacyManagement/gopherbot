package bot

import (
	"strings"

	"github.com/lnxjedi/gopherbot/robot"
)

func sendReadyMessageIfConfigured() {
	currentCfg.RLock()
	message := currentCfg.readyMessage
	channel := currentCfg.readyChannel
	defaultJobChannel := currentCfg.defaultJobChannel
	protocol := currentCfg.defaultProtocol
	format := currentCfg.defaultMessageFormat
	currentCfg.RUnlock()

	if strings.TrimSpace(message) == "" {
		return
	}
	channel = strings.TrimSpace(channel)
	if channel == "" {
		channel = strings.TrimSpace(defaultJobChannel)
	}
	if channel == "" {
		Log(robot.Error, "ReadyMessage configured but no ReadyChannel or DefaultJobChannel is available")
		return
	}
	if interfaces.Connector == nil {
		Log(robot.Error, "ReadyMessage configured but connector runtime is unavailable")
		return
	}

	msgObject := &robot.ConnectorMessage{Protocol: protocol}
	if ret := interfaces.SendProtocolChannelThreadMessage(channel, "", message, format, msgObject); ret != robot.Ok {
		Log(robot.Error, "Sending ReadyMessage to channel '%s' failed: %s", channel, ret)
	}
}
