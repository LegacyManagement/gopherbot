package cloudflarekv

import "github.com/lnxjedi/gopherbot/robot"

func init() {
	robot.RegisterRemoteBrain("cloudflare", remoteProvider)
}
