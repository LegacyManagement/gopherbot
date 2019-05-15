// Package rocket implements a connector for Rocket.Chat
package rocket

import (
	"sync"

	models "github.com/RocketChat/Rocket.Chat.Go.SDK/models"
	api "github.com/RocketChat/Rocket.Chat.Go.SDK/realtime"
	"github.com/lnxjedi/gopherbot/bot"
)

type config struct {
	Server   string // Rocket.Chat server to connect to
	Email    string // Rocket.Chat user email
	Password string // the initial userid
}

type rocketConnector struct {
	rt      *api.Client
	user    *models.User
	running bool
	bot.Handler
	sync.Mutex
	joinChannels map[string]struct{}
	subChannels  map[string]struct{}
}

func (rc *rocketConnector) Run(stop <-chan struct{}) {
	rc.Lock()
	// This should never happen, just a bit of defensive coding
	if rc.running {
		rc.Unlock()
		return
	}
	rc.running = true
	rc.Unlock()
	rc.subscribeChannels()
	<-stop
	rc.Lock()
	// TODO: loop on subscriptions and close
	rc.Unlock()
}

func (rc *rocketConnector) subscribeChannels() {
	rc.Lock()
	defer rc.Unlock()
	for want := range rc.joinChannels {
		if _, ok := rc.subChannels[want]; !ok {
			rc.subChannels[want] = struct{}{}
			if rid, err := rc.rt.GetChannelId(want); err != nil {
				if err := rc.rt.JoinChannel(rid); err != nil {
					rc.Log(bot.Error, "joining channel %s/%s: %v", want, rid, err)
				}
			} else {
				rc.Log(bot.Error, "Getting channel ID for %s: %v", want, err)
			}
		}
	}
	return
}

func (rc *rocketConnector) sendMessage(ch, msg string, f bot.MessageFormat) (ret bot.RetVal) {
	return bot.Ok
}
