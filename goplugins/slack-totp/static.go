package slack_totp

import "github.com/lnxjedi/gopherbot/bot"

func init() {
	bot.RegisterPreload("goplugins/slack-totp.so")
	bot.RegisterPlugin("slack-totp", totphandler)
}
