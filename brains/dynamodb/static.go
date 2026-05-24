package dynamobrain

import "github.com/lnxjedi/gopherbot/robot"

func init() {
	robot.RegisterRemoteBrain("dynamo", remoteProvider)
}
